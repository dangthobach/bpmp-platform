package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type Repository interface {
	Create(context.Context, domain.Actor, domain.Profile, domain.Version) (domain.Profile, error)
	List(context.Context, string, string, string, int) ([]domain.Profile, error)
	Get(context.Context, string, string) (domain.Profile, []domain.Version, error)
	ProfileOwner(context.Context, string, string) (domain.Owner, error)
	AddDraft(context.Context, domain.Actor, string, int64, domain.Version) (domain.Profile, error)
	Publish(context.Context, domain.Actor, string, string, int64, string, time.Time) (domain.Profile, error)
	Rollback(context.Context, domain.Actor, string, string, int64, domain.Version, time.Time) (domain.Profile, error)
	Resolve(context.Context, domain.ResolutionLookup) (domain.ResolvedConfiguration, error)
}

func (s *Service) Resolve(
	ctx context.Context,
	lookup domain.ResolutionLookup,
) (domain.ResolvedConfiguration, error) {
	lookup.TenantID = strings.TrimSpace(lookup.TenantID)
	lookup.WorkflowType = strings.TrimSpace(lookup.WorkflowType)
	lookup.WorkflowVersion = strings.TrimSpace(lookup.WorkflowVersion)
	lookup.PlatformReference = strings.TrimSpace(lookup.PlatformReference)
	lookup.EnvironmentReference = strings.TrimSpace(lookup.EnvironmentReference)
	lookup.InstanceID = strings.TrimSpace(lookup.InstanceID)
	if lookup.TenantID == "" || domain.ValidateOwner(lookup.Owner) != nil ||
		(lookup.Owner == domain.OwnerEngine && (lookup.WorkflowType == "" ||
			lookup.WorkflowVersion == "")) ||
		lookup.PlatformReference == "" ||
		lookup.EnvironmentReference == "" {
		return domain.ResolvedConfiguration{}, domain.ErrInvalid
	}
	return s.repository.Resolve(ctx, lookup)
}

type Config struct {
	ReadCapability   string
	ManageCapability string
	DefaultPageSize  int
	MaxPageSize      int
}

type Service struct {
	repository Repository
	config     Config
	now        func() time.Time
	newID      func() string
}

type CreateInput struct {
	Name          string
	Owner         domain.Owner
	Scope         domain.Scope
	SchemaVersion uint32
	PolicyVersion string
	Reason        string
	Values        []byte
}

type DraftInput struct {
	ExpectedVersion int64
	SchemaVersion   uint32
	PolicyVersion   string
	Reason          string
	Values          []byte
}

type Page struct {
	Profiles      []domain.Profile `json:"profiles"`
	NextPageToken string           `json:"next_page_token"`
}

func New(repository Repository, config Config) (*Service, error) {
	if repository == nil ||
		strings.TrimSpace(config.ReadCapability) == "" ||
		strings.TrimSpace(config.ManageCapability) == "" ||
		config.DefaultPageSize <= 0 ||
		config.MaxPageSize < config.DefaultPageSize {
		return nil, errors.New("configuration service dependencies are invalid")
	}
	return &Service{
		repository: repository,
		config:     config,
		now:        time.Now,
		newID:      func() string { return uuid.NewString() },
	}, nil
}

func (s *Service) Create(ctx context.Context, actor domain.Actor, input CreateInput) (domain.Profile, error) {
	if err := s.authorize(actor, s.config.ManageCapability); err != nil {
		return domain.Profile{}, err
	}
	name := strings.TrimSpace(input.Name)
	reason := strings.TrimSpace(input.Reason)
	policyVersion := strings.TrimSpace(input.PolicyVersion)
	if name == "" || len(name) > 160 || reason == "" || policyVersion == "" ||
		input.SchemaVersion == 0 || domain.ValidateOwner(input.Owner) != nil ||
		domain.ValidateScope(input.Scope) != nil {
		return domain.Profile{}, domain.ErrInvalid
	}
	_, canonical, hash, err := domain.ParsePolicy(input.Owner, input.Values)
	if err != nil {
		return domain.Profile{}, err
	}
	now := s.now().UTC()
	profileID := s.newID()
	versionID := s.newID()
	profile := domain.Profile{
		ID:               profileID,
		TenantID:         actor.TenantID,
		Owner:            input.Owner,
		Name:             name,
		Scope:            input.Scope,
		AggregateVersion: 1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	version := domain.Version{
		ID:            versionID,
		ProfileID:     profileID,
		TenantID:      actor.TenantID,
		Ordinal:       1,
		ConfigVersion: versionID,
		PolicyVersion: policyVersion,
		SchemaVersion: input.SchemaVersion,
		Status:        domain.StatusDraft,
		ValuesJSON:    canonical,
		ContentHash:   hash,
		Reason:        reason,
		CreatedAt:     now,
		CreatedBy:     actor.ActorID,
	}
	actor.RequestDigest = digest(struct {
		Name, Owner, ScopeType, ScopeReference, PolicyVersion, Reason string
		SchemaVersion                                                 uint32
		ContentHash                                                   [32]byte
	}{name, string(input.Owner), string(input.Scope.Type), input.Scope.Reference, policyVersion, reason, input.SchemaVersion, hash})
	return s.repository.Create(ctx, actor, profile, version)
}

func (s *Service) AddDraft(ctx context.Context, actor domain.Actor, profileID string, input DraftInput) (domain.Profile, error) {
	if err := s.authorize(actor, s.config.ManageCapability); err != nil {
		return domain.Profile{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	policyVersion := strings.TrimSpace(input.PolicyVersion)
	if profileID == "" || input.ExpectedVersion <= 0 || input.SchemaVersion == 0 ||
		reason == "" || policyVersion == "" {
		return domain.Profile{}, domain.ErrInvalid
	}
	owner, err := s.repository.ProfileOwner(ctx, actor.TenantID, profileID)
	if err != nil {
		return domain.Profile{}, err
	}
	_, canonical, hash, err := domain.ParsePolicy(owner, input.Values)
	if err != nil {
		return domain.Profile{}, err
	}
	now := s.now().UTC()
	versionID := s.newID()
	version := domain.Version{
		ID:            versionID,
		ProfileID:     profileID,
		TenantID:      actor.TenantID,
		ConfigVersion: versionID,
		PolicyVersion: policyVersion,
		SchemaVersion: input.SchemaVersion,
		Status:        domain.StatusDraft,
		ValuesJSON:    canonical,
		ContentHash:   hash,
		Reason:        reason,
		CreatedAt:     now,
		CreatedBy:     actor.ActorID,
	}
	actor.RequestDigest = digest(struct {
		ProfileID, PolicyVersion, Reason string
		ExpectedVersion                  int64
		SchemaVersion                    uint32
		ContentHash                      [32]byte
	}{profileID, policyVersion, reason, input.ExpectedVersion, input.SchemaVersion, hash})
	return s.repository.AddDraft(ctx, actor, profileID, input.ExpectedVersion, version)
}

func (s *Service) Publish(ctx context.Context, actor domain.Actor, profileID, versionID string, expectedVersion int64, reason string) (domain.Profile, error) {
	if err := s.authorize(actor, s.config.ManageCapability); err != nil {
		return domain.Profile{}, err
	}
	if profileID == "" || versionID == "" || expectedVersion <= 0 || strings.TrimSpace(reason) == "" {
		return domain.Profile{}, domain.ErrInvalid
	}
	reason = strings.TrimSpace(reason)
	actor.RequestDigest = digest(struct {
		ProfileID, VersionID, Reason string
		ExpectedVersion              int64
	}{profileID, versionID, reason, expectedVersion})
	return s.repository.Publish(ctx, actor, profileID, versionID, expectedVersion, reason, s.now().UTC())
}

func (s *Service) Rollback(ctx context.Context, actor domain.Actor, profileID, targetVersionID string, expectedVersion int64, policyVersion, reason string) (domain.Profile, error) {
	if err := s.authorize(actor, s.config.ManageCapability); err != nil {
		return domain.Profile{}, err
	}
	if profileID == "" || targetVersionID == "" || expectedVersion <= 0 ||
		strings.TrimSpace(policyVersion) == "" || strings.TrimSpace(reason) == "" {
		return domain.Profile{}, domain.ErrInvalid
	}
	now := s.now().UTC()
	versionID := s.newID()
	version := domain.Version{
		ID:            versionID,
		ProfileID:     profileID,
		TenantID:      actor.TenantID,
		ConfigVersion: versionID,
		PolicyVersion: strings.TrimSpace(policyVersion),
		Status:        domain.StatusPublished,
		Reason:        strings.TrimSpace(reason),
		CreatedAt:     now,
		CreatedBy:     actor.ActorID,
	}
	actor.RequestDigest = digest(struct {
		ProfileID, TargetVersionID, PolicyVersion, Reason string
		ExpectedVersion                                   int64
	}{profileID, targetVersionID, version.PolicyVersion, version.Reason, expectedVersion})
	return s.repository.Rollback(ctx, actor, profileID, targetVersionID, expectedVersion, version, now)
}

func (s *Service) Get(ctx context.Context, actor domain.Actor, profileID string) (domain.Profile, []domain.Version, error) {
	if err := s.authorize(actor, s.config.ReadCapability); err != nil {
		return domain.Profile{}, nil, err
	}
	if profileID == "" {
		return domain.Profile{}, nil, domain.ErrInvalid
	}
	return s.repository.Get(ctx, actor.TenantID, profileID)
}

func (s *Service) List(ctx context.Context, actor domain.Actor, pageSize int, token string) (Page, error) {
	if err := s.authorize(actor, s.config.ReadCapability); err != nil {
		return Page{}, err
	}
	if pageSize == 0 {
		pageSize = s.config.DefaultPageSize
	}
	if pageSize < 0 || pageSize > s.config.MaxPageSize {
		return Page{}, domain.ErrInvalid
	}
	afterName, afterID, err := decodeCursor(token)
	if err != nil {
		return Page{}, err
	}
	items, err := s.repository.List(ctx, actor.TenantID, afterName, afterID, pageSize+1)
	if err != nil {
		return Page{}, err
	}
	page := Page{Profiles: items}
	if len(items) > pageSize {
		last := items[pageSize-1]
		page.Profiles = items[:pageSize]
		page.NextPageToken = encodeCursor(last.Name, last.ID)
	}
	return page, nil
}

func (s *Service) authorize(actor domain.Actor, capability string) error {
	if strings.TrimSpace(actor.TenantID) == "" ||
		strings.TrimSpace(actor.ActorID) == "" ||
		strings.TrimSpace(actor.CorrelationID) == "" {
		return domain.ErrUnauthorized
	}
	if _, ok := actor.Capabilities[capability]; !ok {
		return domain.ErrUnauthorized
	}
	return nil
}

func digest(value any) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%v", value)))
}

func encodeCursor(name, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(len(name)) + ":" + name + id))
}

func decodeCursor(token string) (string, string, error) {
	if token == "" {
		return "", "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", "", domain.ErrInvalid
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return "", "", domain.ErrInvalid
	}
	length, err := strconv.Atoi(parts[0])
	if err != nil || length <= 0 || length >= len(parts[1]) {
		return "", "", domain.ErrInvalid
	}
	return parts[1][:length], parts[1][length:], nil
}
