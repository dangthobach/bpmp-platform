package application

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const maxSavedDashboardBytes = 64 * 1024

type SavedDashboard struct {
	TenantID, ID, Name, OwnerSubject, ConfigJSON string
	Version                                      uint64
	CreatedAt, UpdatedAt                         time.Time
}

type SavedDashboardStore interface {
	SaveDashboard(context.Context, SavedDashboard, uint64) (SavedDashboard, error)
	ListDashboards(context.Context, string, string, time.Time, string, int) ([]SavedDashboard, error)
	DeleteDashboard(context.Context, string, string, string, uint64) error
}

var savedDashboardIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (s *Service) SaveDashboard(ctx context.Context, item SavedDashboard, expectedVersion uint64, credential QueryCredential) (SavedDashboard, error) {
	store, ok := s.store.(SavedDashboardStore)
	if !ok {
		return SavedDashboard{}, fmt.Errorf("saved dashboard store is unavailable")
	}
	if item.TenantID == "" || !savedDashboardIDPattern.MatchString(item.ID) || strings.TrimSpace(item.Name) == "" || len([]byte(item.Name)) > 256 {
		return SavedDashboard{}, invalidQuery("dashboard", "invalid_dashboard", "dashboard id or name is invalid")
	}
	if err := validateDashboardConfig(item.ConfigJSON); err != nil {
		return SavedDashboard{}, err
	}
	policy, err := s.policy.Policy(item.TenantID)
	if err != nil {
		return SavedDashboard{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: item.TenantID, Credential: credential, Policy: policy})
	if err != nil {
		return SavedDashboard{}, err
	}
	if auth.SubjectID == "" {
		return SavedDashboard{}, ErrQueryForbidden
	}
	item.OwnerSubject = auth.SubjectID
	return store.SaveDashboard(queryCtx, item, expectedVersion)
}

func (s *Service) ListDashboards(ctx context.Context, tenant string, credential QueryCredential, afterTime time.Time, afterID string, limit int) ([]SavedDashboard, int, string, error) {
	store, ok := s.store.(SavedDashboardStore)
	if !ok {
		return nil, 0, "", fmt.Errorf("saved dashboard store is unavailable")
	}
	policy, err := s.policy.Policy(tenant)
	if err != nil {
		return nil, 0, "", err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: tenant, Credential: credential, Policy: policy})
	if err != nil {
		return nil, 0, "", err
	}
	if auth.SubjectID == "" {
		return nil, 0, "", ErrQueryForbidden
	}
	if limit == 0 {
		limit = int(policy.QueryDefaultPageSize)
	}
	if limit < 1 || uint32(limit) > policy.QueryMaxPageSize {
		return nil, 0, "", invalidQuery("page_size", "page_limit", "page size exceeds tenant policy")
	}
	items, err := store.ListDashboards(queryCtx, tenant, auth.SubjectID, afterTime, afterID, limit+1)
	return items, limit, policy.QueryCursorSigningKeyID, err
}

func (s *Service) DeleteDashboard(ctx context.Context, tenant, id string, expected uint64, credential QueryCredential) error {
	store, ok := s.store.(SavedDashboardStore)
	if !ok {
		return fmt.Errorf("saved dashboard store is unavailable")
	}
	if !savedDashboardIDPattern.MatchString(id) || expected == 0 {
		return invalidQuery("dashboard", "invalid_delete", "id and expected version are required")
	}
	policy, err := s.policy.Policy(tenant)
	if err != nil {
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	defer cancel()
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: tenant, Credential: credential, Policy: policy})
	if err != nil {
		return err
	}
	if auth.SubjectID == "" {
		return ErrQueryForbidden
	}
	return store.DeleteDashboard(queryCtx, tenant, id, auth.SubjectID, expected)
}

func validateDashboardConfig(raw string) error {
	if len([]byte(raw)) == 0 || len([]byte(raw)) > maxSavedDashboardBytes {
		return invalidQuery("config_json", "dashboard_size", "dashboard configuration must be between 1 byte and 64 KiB")
	}
	var document struct {
		SchemaVersion int               `json:"schema_version"`
		Widgets       []json.RawMessage `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil || document.SchemaVersion != 1 || len(document.Widgets) == 0 || len(document.Widgets) > 24 {
		return invalidQuery("config_json", "dashboard_document", "dashboard must be a schema_version 1 object with 1 to 24 widgets")
	}
	widgetIDs := make(map[string]struct{}, len(document.Widgets))
	for index, rawWidget := range document.Widgets {
		var widget struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal(rawWidget, &widget) != nil || !savedDashboardIDPattern.MatchString(widget.ID) || strings.TrimSpace(widget.Type) == "" || len(widget.Type) > 64 {
			return invalidQuery(fmt.Sprintf("config_json.widgets[%d]", index), "dashboard_widget", "each widget requires a stable id and type")
		}
		if _, duplicate := widgetIDs[widget.ID]; duplicate {
			return invalidQuery(fmt.Sprintf("config_json.widgets[%d].id", index), "duplicate_widget_id", "widget ids must be unique within a dashboard")
		}
		widgetIDs[widget.ID] = struct{}{}
	}
	return nil
}
