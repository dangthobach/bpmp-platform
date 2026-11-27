package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

type stubDispatcher struct {
	fn func(ctx context.Context, ep Endpoint, del Delivery) DispatchOutcome
}

func (d stubDispatcher) Dispatch(ctx context.Context, ep Endpoint, del Delivery) DispatchOutcome {
	return d.fn(ctx, ep, del)
}

func signIntCallback(secret []byte, ts, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts + "." + eventID + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSSRFGuardAndSecretRefIsolation(t *testing.T) {
	svc := NewService(Config{AllowedEgressHosts: []string{"api.t24.bank.example"}})
	actor := Actor{TenantID: "t1", ActorID: "admin-1", Capabilities: []string{"integration.endpoint.write", "integration.endpoint.read"}}

	blocked := []string{
		"http://api.t24.bank.example/v1",
		"https://127.0.0.1/admin",
		"https://169.254.169.254/latest/meta-data",
		"https://10.0.0.12/internal",
		"https://evil.untrusted.example/hook",
	}
	for _, u := range blocked {
		if _, err := svc.CreateEndpoint(context.Background(), actor, CreateEndpointRequest{
			Name: "bad", SystemType: SystemT24ESB, BaseURL: u, SecretRef: "vault://secret/t24",
		}); !errors.Is(err, ErrSSRFBlocked) {
			t.Fatalf("expected ErrSSRFBlocked for %s, got %v", u, err)
		}
	}

	ep, err := svc.CreateEndpoint(context.Background(), actor, CreateEndpointRequest{
		Name: "T24 Core", SystemType: SystemT24ESB, BaseURL: "https://api.t24.bank.example/esb", SecretRef: "vault://kv/t24-prod",
	})
	if err != nil {
		t.Fatalf("expected allowed endpoint, got %v", err)
	}
	if ep.SecretRef != "vault://kv/t24-prod" {
		t.Fatalf("unexpected secret_ref %s", ep.SecretRef)
	}
}

func TestCircuitBreakerBulkheadReconcileAndManualRetry(t *testing.T) {
	now := time.Date(2026, 11, 27, 11, 0, 0, 0, time.UTC)
	mode := "timeout"
	svc := NewService(Config{
		AllowedEgressHosts: []string{"aml.bank.example"},
		Now:                func() time.Time { return now },
		Dispatcher: stubDispatcher{fn: func(ctx context.Context, ep Endpoint, del Delivery) DispatchOutcome {
			switch mode {
			case "timeout":
				return DispatchOutcome{ResponseClass: "TIMEOUT", Ambiguous: true, Err: context.DeadlineExceeded}
			case "5xx":
				return DispatchOutcome{ResponseClass: "5xx", Err: errors.New("upstream 503")}
			default:
				return DispatchOutcome{ResponseClass: "2xx"}
			}
		}},
	})

	actor := Actor{
		TenantID: "t1", ActorID: "ops-1", CorrelationID: "corr-9",
		Capabilities: []string{"integration.endpoint.write", "integration.delivery.write", "integration.delivery.read", "integration.delivery.retry"},
	}
	ep, err := svc.CreateEndpoint(context.Background(), actor, CreateEndpointRequest{
		Name: "AML Check", SystemType: SystemAML, BaseURL: "https://aml.bank.example/v1", SecretRef: "vault://aml",
		CircuitFailureThreshold: 2, CircuitOpenSeconds: 30, BulkheadMaxConcurrency: 2, MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	// Ambiguous timeout enters RECONCILING with original downstream idempotency key preserved.
	del, err := svc.DispatchOperation(context.Background(), actor, DispatchRequest{
		EndpointID: ep.ID, DownstreamIdempotencyKey: "aml-key-1", PayloadDigest: "sha256:abc", RedactedSummary: "AML screen case-1",
	})
	if err != nil || del.Status != DeliveryReconciling {
		t.Fatalf("expected RECONCILING, got err=%v status=%s", err, del.Status)
	}

	// Second failure opens the circuit breaker.
	mode = "5xx"
	_, _ = svc.DispatchOperation(context.Background(), actor, DispatchRequest{
		EndpointID: ep.ID, DownstreamIdempotencyKey: "aml-key-2", PayloadDigest: "sha256:def", RedactedSummary: "AML screen case-2",
	})
	// Third call immediately fails fast with ErrCircuitOpen (no cascade).
	if _, err := svc.DispatchOperation(context.Background(), actor, DispatchRequest{
		EndpointID: ep.ID, DownstreamIdempotencyKey: "aml-key-3", PayloadDigest: "sha256:ghi", RedactedSummary: "AML screen case-3",
	}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	// Advance clock past circuit open window -> HALF_OPEN -> manual retry succeeds with same key.
	now = now.Add(31 * time.Second)
	mode = "ok"
	retried, dup, err := svc.RetryDelivery(context.Background(), actor, RetryDeliveryRequest{
		DeliveryID: del.ID, ExpectedVersion: del.Version, Reason: "AML recovered", IdempotencyKey: "retry-1",
	})
	if err != nil || dup || retried.Status != DeliverySucceeded || retried.DownstreamIdempotencyKey != "aml-key-1" {
		t.Fatalf("expected SUCCEEDED retry with preserved key, got err=%v dup=%v del=%+v", err, dup, retried)
	}
	// Replay of same manual retry key returns cached result.
	replayed, dup, err := svc.RetryDelivery(context.Background(), actor, RetryDeliveryRequest{
		DeliveryID: del.ID, ExpectedVersion: del.Version, Reason: "AML recovered", IdempotencyKey: "retry-1",
	})
	if err != nil || !dup || replayed.ID != retried.ID {
		t.Fatalf("expected idempotent retry replay, got err=%v dup=%v", err, dup)
	}
}

func TestCallbackIngressSignatureRotationDedupAndConflict(t *testing.T) {
	now := time.Date(2026, 11, 27, 11, 0, 0, 0, time.UTC)
	oldSecret := []byte("old-hmac-secret")
	newSecret := []byte("new-hmac-secret")
	svc := NewService(Config{
		Now: func() time.Time { return now },
		Bindings: []CallbackBinding{
			{BindingID: "bind-1", ProviderKey: "rpa-bot", TenantID: "t1", Secrets: [][]byte{oldSecret, newSecret}},
		},
	})

	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"job":"rpa-42","result":"DONE"}`)

	// Unknown binding fails with ErrInvalidSignature (no tenant disclosure).
	if _, err := svc.ProcessCallback(context.Background(), "unknown", "rpa-bot", ts, signIntCallback(newSecret, ts, "ev-1", body), "ev-1", body); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for unknown binding, got %v", err)
	}

	// Rotated secret accepted.
	r1, err := svc.ProcessCallback(context.Background(), "bind-1", "rpa-bot", ts, signIntCallback(newSecret, ts, "ev-1", body), "ev-1", body)
	if err != nil || r1.Status != "ACCEPTED" {
		t.Fatalf("expected ACCEPTED, got err=%v r1=%+v", err, r1)
	}

	// Duplicate returns same receipt.
	r2, err := svc.ProcessCallback(context.Background(), "bind-1", "rpa-bot", ts, signIntCallback(oldSecret, ts, "ev-1", body), "ev-1", body)
	if err != nil || r2.Status != "DUPLICATE" || r2.ReceiptID != r1.ReceiptID {
		t.Fatalf("expected DUPLICATE with same receipt, got err=%v r2=%+v", err, r2)
	}

	// Same event ID with changed payload conflicts.
	changed := []byte(`{"job":"rpa-42","result":"FAILED"}`)
	if _, err := svc.ProcessCallback(context.Background(), "bind-1", "rpa-bot", ts, signIntCallback(newSecret, ts, "ev-1", changed), "ev-1", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on digest mismatch, got %v", err)
	}
}
