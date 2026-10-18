package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type QueryExportStatus int32

const (
	QueryExportPending QueryExportStatus = iota + 1
	QueryExportCompleted
	QueryExportFailed
	QueryExportCancelled
)
const (
	maxExportRows  = 100000
	maxExportBytes = 16 << 20
)

type QueryExport struct {
	TenantID, ID, OwnerSubject, ErrorCode string
	Status                                QueryExportStatus
	Query                                 ProcessQuery
	MaxRows                               int
	RowCount                              uint64
	CSV                                   []byte
	CreatedAt, ExpiresAt                  time.Time
}
type QueryExportStore interface {
	CreateQueryExport(context.Context, QueryExport) (QueryExport, error)
	GetQueryExport(context.Context, string, string, string) (QueryExport, error)
	CancelQueryExport(context.Context, string, string, string) (QueryExport, error)
	ClaimQueryExport(context.Context) (QueryExport, error)
	CompleteQueryExport(context.Context, string, string, []byte, uint64, string) error
}

func (s *Service) CreateQueryExport(ctx context.Context, tenant string, query ProcessQuery, maxRows int, credential QueryCredential) (QueryExport, error) {
	store, ok := s.store.(QueryExportStore)
	if !ok {
		return QueryExport{}, fmt.Errorf("query export store is unavailable")
	}
	if maxRows == 0 {
		maxRows = 10000
	}
	if maxRows < 1 || maxRows > maxExportRows {
		return QueryExport{}, invalidQuery("max_rows", "export_limit", "export row limit exceeds policy")
	}
	query.TenantID, query.Credential = tenant, credential
	_, requested, normalized, err := s.QueryInstances(ctx, query)
	if err != nil {
		return QueryExport{}, err
	}
	normalized.Limit = min(requested, 500)
	normalized.Credential = QueryCredential{}
	normalized.After = nil
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		return QueryExport{}, err
	}
	item := QueryExport{TenantID: tenant, ID: hex.EncodeToString(idBytes), OwnerSubject: normalized.AuthorizationSubject, Status: QueryExportPending, Query: normalized, MaxRows: maxRows, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	return store.CreateQueryExport(ctx, item)
}

func (s *Service) GetQueryExport(ctx context.Context, tenant, id string, credential QueryCredential) (QueryExport, error) {
	store, owner, queryCtx, err := s.exportAccess(ctx, tenant, credential)
	if err != nil {
		return QueryExport{}, err
	}
	defer queryCtx.cancel()
	return store.GetQueryExport(queryCtx.ctx, tenant, id, owner)
}
func (s *Service) CancelQueryExport(ctx context.Context, tenant, id string, credential QueryCredential) (QueryExport, error) {
	store, owner, queryCtx, err := s.exportAccess(ctx, tenant, credential)
	if err != nil {
		return QueryExport{}, err
	}
	defer queryCtx.cancel()
	return store.CancelQueryExport(queryCtx.ctx, tenant, id, owner)
}

type exportContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *Service) exportAccess(ctx context.Context, tenant string, credential QueryCredential) (QueryExportStore, string, exportContext, error) {
	store, ok := s.store.(QueryExportStore)
	if !ok {
		return nil, "", exportContext{}, fmt.Errorf("query export store is unavailable")
	}
	policy, err := s.policy.Policy(tenant)
	if err != nil {
		return nil, "", exportContext{}, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, policy.QueryTimeout)
	auth, err := s.authorizer.AuthorizeQuery(queryCtx, QueryAuthorizationRequest{TenantID: tenant, Credential: credential, Policy: policy})
	if err != nil {
		cancel()
		return nil, "", exportContext{}, err
	}
	if auth.SubjectID == "" {
		cancel()
		return nil, "", exportContext{}, ErrQueryForbidden
	}
	return store, auth.SubjectID, exportContext{queryCtx, cancel}, nil
}

func (s *Service) RunExportWorker(ctx context.Context) error {
	store, ok := s.store.(QueryExportStore)
	if !ok {
		return fmt.Errorf("query export store is unavailable")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			item, err := store.ClaimQueryExport(ctx)
			if err == ErrNotFound {
				continue
			}
			if err != nil {
				return err
			}
			data, count, code := s.renderExport(ctx, item)
			if err = store.CompleteQueryExport(ctx, item.TenantID, item.ID, data, count, code); err != nil {
				return err
			}
		}
	}
}

func (s *Service) renderExport(ctx context.Context, item QueryExport) ([]byte, uint64, string) {
	query := item.Query
	query.ExpandEvents = nil
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	fields := query.SelectedFields
	if len(fields) == 0 {
		fields = []ProcessField{ProcessInstanceID, ProcessWorkflowType, ProcessWorkflowVersion, ProcessStatus, ProcessUpdatedAt}
	}
	readable := fields[:0]
	for _, field := range fields {
		if query.Fields == nil || query.Fields.CanRead(ProcessFieldName(field)) { readable = append(readable, field) }
	}
	fields = readable
	if len(fields) == 0 { return nil, 0, "no_readable_fields" }
	headers := make([]string, 0, len(fields))
	for _, field := range fields {
		headers = append(headers, ProcessFieldName(field))
	}
	if err := writer.Write(headers); err != nil {
		return nil, 0, "csv_write"
	}
	var count uint64
	for count < uint64(item.MaxRows) {
		query.Limit = min(500, item.MaxRows-int(count))
		rows, err := s.store.QueryInstances(ctx, query)
		if err != nil {
			return nil, count, "query_failed"
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			record := make([]string, 0, len(fields))
			for _, field := range fields {
				record = append(record, exportField(row, field))
			}
			if err = writer.Write(record); err != nil {
				return nil, count, "csv_write"
			}
			count++
			if buffer.Len() > maxExportBytes {
				return nil, count, "export_too_large"
			}
			if count >= uint64(item.MaxRows) {
				break
			}
		}
		if len(rows) < query.Limit || count >= uint64(item.MaxRows) {
			break
		}
		cursor, err := CursorFor(query, rows[len(rows)-1])
		if err != nil {
			return nil, count, "cursor_failed"
		}
		query.After = cursor
	}
	writer.Flush()
	if writer.Error() != nil {
		return nil, count, "csv_write"
	}
	return buffer.Bytes(), count, ""
}

func exportField(row CompletedProcess, field ProcessField) string {
	var value string
	switch field {
	case ProcessInstanceID:
		value = row.Instance.InstanceID
	case ProcessWorkflowType:
		value = row.Instance.WorkflowType
	case ProcessWorkflowVersion:
		value = row.Instance.WorkflowVersion
	case ProcessStatus:
		value = row.Instance.Status
	case ProcessActiveNodeID:
		value = row.Instance.ActiveNodeID
	case ProcessLastEventSequence:
		value = strconv.FormatUint(row.Instance.LastEventSequence, 10)
	case ProcessStartedAt:
		value = row.Instance.StartedAt.UTC().Format(time.RFC3339Nano)
	case ProcessUpdatedAt:
		value = row.Instance.UpdatedAt.UTC().Format(time.RFC3339Nano)
	case ProcessCompletedAt:
		if row.Instance.CompletedAt != nil {
			value = row.Instance.CompletedAt.UTC().Format(time.RFC3339Nano)
		}
	case ProcessDurationMS:
		value = strconv.FormatUint(row.DurationMS, 10)
	case ProcessEventCount:
		value = strconv.FormatUint(row.EventCount, 10)
	case ProcessConfigVersion:
		value = row.Instance.ConfigVersion
	case ProcessPolicyVersion:
		value = row.Instance.PolicyVersion
	case ProcessVersion:
		value = strconv.FormatUint(row.Instance.Version, 10)
	case ProcessInitiatorActorID:
		value = row.Instance.InitiatorActorID
	case ProcessIsDeleted:
		value = strconv.FormatBool(row.Instance.IsDeleted)
	}
	if strings.HasPrefix(value, "=") || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "@") {
		return "'" + value
	}
	return value
}
