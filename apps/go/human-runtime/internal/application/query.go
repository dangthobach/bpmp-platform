package application

import (
	"context"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/human-runtime/internal/domain"
)

type PageCursor struct {
	UpdatedAt  time.Time
	WorkItemID string
}
type CaseView struct{ Case domain.Case }
type AuditCursor struct {
	OccurredAt time.Time
	AuditID    string
}
type AuditRecord struct {
	AuditID       string
	WorkItemID    string
	CaseID        string
	ActorID       string
	Action        string
	OccurredAt    time.Time
	CommandID     string
	CorrelationID string
	FromVersion   int64
	ToVersion     int64
	DetailsJSON   []byte
}

type QueryPort interface {
	GetWorkItem(context.Context, string, string) (domain.WorkItem, error)
	ListWorkItems(context.Context, string, string, []string, int, *PageCursor) ([]domain.WorkItem, *PageCursor, error)
	GetCase(context.Context, string, string) (CaseView, error)
	ListAuditRecords(context.Context, string, string, string, int, *AuditCursor) ([]AuditRecord, *AuditCursor, error)
}

type ActorIdentity struct {
	ActorID      string
	Groups       map[string]struct{}
	Capabilities map[string]struct{}
}
type ActorVerificationRequest struct {
	TenantID    string
	CommandID   string
	EvaluatedAt time.Time
	Credential  ActorCredential
}

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

func NormalizePageSize(limit int) int {
	if limit <= 0 || limit > MaxPageSize {
		return DefaultPageSize
	}
	return limit
}

// BuildWorkItemPage converts a limit+1 keyset query result into a bounded page.
func BuildWorkItemPage(items []domain.WorkItem, limit int) ([]domain.WorkItem, *PageCursor) {
	limit = NormalizePageSize(limit)
	if len(items) <= limit {
		return items, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	return items, &PageCursor{UpdatedAt: last.UpdatedAt, WorkItemID: last.ID}
}

type ActorVerifier interface {
	VerifyActor(context.Context, ActorVerificationRequest) (ActorIdentity, error)
}
