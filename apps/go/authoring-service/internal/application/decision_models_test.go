package application

import (
	"context"
	"testing"
	"time"

	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
)

type decisionTestActors struct{}

func (decisionTestActors) Verify(context.Context, string, string, *authv1.ActorProof, time.Time) (Actor, error) {
	return Actor{ID: "9e7d510b-66cf-4c14-940b-1ebcc982a145"}, nil
}

type decisionTestCompiler struct {
	tenant, model, source string
	revision              int64
	diagnostics           []Diagnostic
}

func (*decisionTestCompiler) ValidateBPMN(context.Context, string, string, int64, string) ([]Diagnostic, error) {
	return nil, nil
}
func (c *decisionTestCompiler) ValidateDMN(_ context.Context, tenant, model string, revision int64, source string) ([]Diagnostic, error) {
	c.tenant, c.model, c.revision, c.source = tenant, model, revision, source
	return c.diagnostics, nil
}

type decisionTestRepository struct {
	Repository
	model                                            DecisionModel
	draft                                            DecisionDraft
	savedStatus                                      string
	savedDiagnostics                                 []Diagnostic
	saveActor, saveCommand, saveKey, saveCorrelation string
	savedDigest                                      []byte
}

func (r *decisionTestRepository) GetDecisionModel(context.Context, string, string) (DecisionModel, DecisionDraft, error) {
	return r.model, r.draft, nil
}
func (r *decisionTestRepository) SaveDecisionValidation(_ context.Context, _, _ string, _ int64, status string, diagnostics []Diagnostic, actor, command, key, correlation string, digest []byte, _ time.Time) error {
	r.savedStatus, r.savedDiagnostics, r.saveActor, r.saveCommand, r.saveKey, r.saveCorrelation, r.savedDigest = status, diagnostics, actor, command, key, correlation, digest
	return nil
}

// Compile-time guard keeps the fake aligned with the production Authorizer contract.
type decisionTestPolicy struct{}

func (decisionTestPolicy) Authorize(context.Context, string, Actor, string, string, AuthorizationRequirements) error {
	return nil
}

func TestValidateDecisionDraftCompilesAndPersistsExactRevision(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	source := "<definitions id=\"decision\"/>"
	expectedDiagnostic := []Diagnostic{{Code: "DMN_OK", Severity: "INFO", Message: "checked"}}
	repo := &decisionTestRepository{model: DecisionModel{ID: "decision-id", TenantID: "tenant-id", Revision: 7}, draft: DecisionDraft{DecisionModelID: "decision-id", Revision: 7, DMNXML: source}}
	compiler := &decisionTestCompiler{diagnostics: expectedDiagnostic}
	svc, err := New(repo, decisionTestActors{}, decisionTestPolicy{}, compiler, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ValidateDecisionDraft(context.Background(), "tenant-id", "decision-id", "command-id", "idempotency-key", "correlation-id", 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if compiler.tenant != "tenant-id" || compiler.model != "decision-id" || compiler.revision != 7 || compiler.source != source {
		t.Fatalf("compiler received wrong persisted revision: %+v", compiler)
	}
	if result.Validation != "VALID" || result.Revision != 7 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "DMN_OK" {
		t.Fatalf("unexpected validation result: %+v", result)
	}
	if repo.savedStatus != "VALID" || repo.saveActor != "9e7d510b-66cf-4c14-940b-1ebcc982a145" || repo.saveCommand != "command-id" || repo.saveKey != "idempotency-key" || repo.saveCorrelation != "correlation-id" || len(repo.savedDigest) != 32 {
		t.Fatalf("validation evidence was not persisted with command identity: %+v", repo)
	}
}

func TestValidateDecisionDraftRejectsStaleRevisionBeforeCompiler(t *testing.T) {
	repo := &decisionTestRepository{model: DecisionModel{ID: "decision-id", TenantID: "tenant-id", Revision: 8}, draft: DecisionDraft{DecisionModelID: "decision-id", Revision: 8, DMNXML: "<definitions/>"}}
	compiler := &decisionTestCompiler{}
	svc, err := New(repo, decisionTestActors{}, decisionTestPolicy{}, compiler, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ValidateDecisionDraft(context.Background(), "tenant-id", "decision-id", "command-id", "key", "correlation", 7, nil)
	if err != ErrConflict {
		t.Fatalf("expected stale revision conflict, got %v", err)
	}
	if compiler.model != "" {
		t.Fatal("compiler ran for stale revision")
	}
}
