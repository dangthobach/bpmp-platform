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
type ActorVerifier interface {
	VerifyActor(context.Context, ActorVerificationRequest) (ActorIdentity, error)
}
