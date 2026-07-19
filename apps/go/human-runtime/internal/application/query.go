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

type QueryPort interface {
	GetWorkItem(context.Context, string, string) (domain.WorkItem, error)
	ListWorkItems(context.Context, string, string, []string, int, *PageCursor) ([]domain.WorkItem, *PageCursor, error)
	GetCase(context.Context, string, string) (CaseView, error)
}

type ActorIdentity struct {
	ActorID string
	Groups  map[string]struct{}
}
type ActorVerificationRequest struct {
	TenantID   string
	CommandID  string
	EvaluatedAt time.Time
	Credential ActorCredential
}
type ActorVerifier interface {
	VerifyActor(context.Context, ActorVerificationRequest) (ActorIdentity, error)
}
