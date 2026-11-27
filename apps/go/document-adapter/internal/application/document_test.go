package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signCallback(secret []byte, ts string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func newTestService(t *testing.T, clock *time.Time) (*Service, []byte, []byte) {
	t.Helper()
	primary := []byte("av-callback-secret-primary-v1")
	rotated := []byte("av-callback-secret-rotated-v2")
	svc, err := NewService(Config{
		CSPEndpoint:      "https://csp.example.internal",
		URLSigningSecret: []byte("doc-url-signing-secret-123456"),
		CallbackSecrets: map[string][][]byte{
			"clamav": {primary, rotated},
		},
		Now: func() time.Time { return *clock },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, primary, rotated
}

func TestDocumentUploadQuarantineScanDownloadAndHoldLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	svc, primarySecret, rotatedSecret := newTestService(t, &now)
	ctx := context.Background()

	actor := Actor{
		TenantID:      "tenant-a",
		ActorID:       "user-1",
		CorrelationID: "corr-1",
		Capabilities:  []string{"document.read", "document.write", "document.download", "document.hold"},
	}

	// 1. 50 MiB boundary enforcement: 50 MiB + 1 byte must fail, exact 50 MiB succeeds.
	_, _, err := svc.CreateUploadSession(ctx, actor, CreateUploadRequest{
		OwnerRefType:      "CASE",
		OwnerRefID:        "case-1",
		FileName:          "too-big.pdf",
		DeclaredMIME:      "application/pdf",
		DeclaredSizeBytes: MaxUploadSizeBytes + 1,
		IdempotencyKey:    "idem-big",
	})
	if !errors.Is(err, ErrSizeLimitExceeded) {
		t.Fatalf("expected ErrSizeLimitExceeded, got %v", err)
	}

	session, dup, err := svc.CreateUploadSession(ctx, actor, CreateUploadRequest{
		OwnerRefType:      "CASE",
		OwnerRefID:        "case-1",
		FileName:          "contract.pdf",
		DeclaredMIME:      "application/pdf",
		DeclaredSizeBytes: MaxUploadSizeBytes,
		IdempotencyKey:    "idem-1",
	})
	if err != nil || dup {
		t.Fatalf("CreateUploadSession: err=%v dup=%v", err, dup)
	}
	if !strings.Contains(session.SignedUploadURL, "sig=") {
		t.Fatalf("expected signed upload URL, got %s", session.SignedUploadURL)
	}

	// Idempotent replay returns same session; changed payload conflicts.
	replay, dup, err := svc.CreateUploadSession(ctx, actor, CreateUploadRequest{
		OwnerRefType:      "CASE",
		OwnerRefID:        "case-1",
		FileName:          "contract.pdf",
		DeclaredMIME:      "application/pdf",
		DeclaredSizeBytes: MaxUploadSizeBytes,
		IdempotencyKey:    "idem-1",
	})
	if err != nil || !dup || replay.ID != session.ID {
		t.Fatalf("expected idempotent replay, got err=%v dup=%v id=%s", err, dup, replay.ID)
	}
	_, _, err = svc.CreateUploadSession(ctx, actor, CreateUploadRequest{
		OwnerRefType:      "CASE",
		OwnerRefID:        "case-1",
		FileName:          "other.pdf",
		DeclaredMIME:      "application/pdf",
		DeclaredSizeBytes: 1024,
		IdempotencyKey:    "idem-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on changed payload, got %v", err)
	}

	// 2. MIME mismatch rejected on finalize.
	validHash := strings.Repeat("a", 64)
	_, _, err = svc.FinalizeUpload(ctx, actor, FinalizeUploadRequest{
		UploadID:        session.ID,
		ExpectedVersion: session.Version,
		ActualSizeBytes: MaxUploadSizeBytes,
		DetectedMIME:    "image/png",
		ContentSHA256:   validHash,
		IdempotencyKey:  "fin-mismatch",
	})
	if !errors.Is(err, ErrUnsupportedMIME) {
		t.Fatalf("expected ErrUnsupportedMIME, got %v", err)
	}

	// 3. Valid finalize transitions to SCANNING (quarantined), download & OCR blocked.
	doc, _, err := svc.FinalizeUpload(ctx, actor, FinalizeUploadRequest{
		UploadID:        session.ID,
		ExpectedVersion: session.Version,
		ActualSizeBytes: MaxUploadSizeBytes,
		DetectedMIME:    "application/pdf",
		ContentSHA256:   validHash,
		IdempotencyKey:  "fin-1",
	})
	if err != nil || doc.Status != StatusScanning {
		t.Fatalf("FinalizeUpload: err=%v status=%s", err, doc.Status)
	}
	if _, err := svc.IssueDownloadURL(ctx, actor, doc.ID); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("quarantined doc must not be downloadable, got %v", err)
	}
	if _, err := svc.TriggerOCR(ctx, actor, doc.ID); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("quarantined doc must not trigger OCR, got %v", err)
	}

	// 4. Scan callback: stale timestamp & invalid signature rejected; rotated key accepted.
	cbPayload, _ := json.Marshal(ScanCallbackPayload{
		ExternalEventID: "av-evt-1",
		TenantID:        "tenant-a",
		DocumentID:      doc.ID,
		DocumentVersion: doc.Version,
		Verdict:         "CLEAN",
	})
	staleTS := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	if _, err := svc.ProcessScanCallback(ctx, "clamav", staleTS, signCallback(primarySecret, staleTS, cbPayload), cbPayload); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("expected ErrStaleTimestamp, got %v", err)
	}
	goodTS := strconv.FormatInt(now.Unix(), 10)
	if _, err := svc.ProcessScanCallback(ctx, "clamav", goodTS, "deadbeef", cbPayload); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}

	receipt, err := svc.ProcessScanCallback(ctx, "clamav", goodTS, signCallback(rotatedSecret, goodTS, cbPayload), cbPayload)
	if err != nil || receipt.Status != "APPLIED" {
		t.Fatalf("expected APPLIED with rotated key, got err=%v receipt=%+v", err, receipt)
	}

	// Duplicate callback returns same receipt; changed digest with same event ID conflicts.
	dupReceipt, err := svc.ProcessScanCallback(ctx, "clamav", goodTS, signCallback(primarySecret, goodTS, cbPayload), cbPayload)
	if err != nil || dupReceipt.Status != "DUPLICATE" || dupReceipt.ReceiptID != receipt.ReceiptID {
		t.Fatalf("expected DUPLICATE with same receipt, got err=%v receipt=%+v", err, dupReceipt)
	}
	tamperedPayload, _ := json.Marshal(ScanCallbackPayload{
		ExternalEventID: "av-evt-1",
		TenantID:        "tenant-a",
		DocumentID:      doc.ID,
		DocumentVersion: doc.Version,
		Verdict:         "REJECTED",
	})
	if _, err := svc.ProcessScanCallback(ctx, "clamav", goodTS, signCallback(primarySecret, goodTS, tamperedPayload), tamperedPayload); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on tampered callback digest, got %v", err)
	}

	// Late/stale callback cannot downgrade CLEAN document.
	latePayload, _ := json.Marshal(ScanCallbackPayload{
		ExternalEventID: "av-evt-late",
		TenantID:        "tenant-a",
		DocumentID:      doc.ID,
		DocumentVersion: 1,
		Verdict:         "REJECTED",
	})
	lateReceipt, err := svc.ProcessScanCallback(ctx, "clamav", goodTS, signCallback(primarySecret, goodTS, latePayload), latePayload)
	if err != nil || lateReceipt.Status != "IGNORED_STALE" {
		t.Fatalf("expected IGNORED_STALE for late callback, got err=%v receipt=%+v", err, lateReceipt)
	}

	// 5. Clean document allows signed download & OCR; cross-tenant denied.
	dl, err := svc.IssueDownloadURL(ctx, actor, doc.ID)
	if err != nil || !strings.Contains(dl.URL, "sig=") {
		t.Fatalf("IssueDownloadURL: err=%v dl=%+v", err, dl)
	}
	otherTenant := Actor{TenantID: "tenant-b", ActorID: "user-2", Capabilities: []string{"document.read", "document.download"}}
	if _, err := svc.IssueDownloadURL(ctx, otherTenant, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-tenant denial (ErrNotFound), got %v", err)
	}
	ocrDoc, err := svc.TriggerOCR(ctx, actor, doc.ID)
	if err != nil || ocrDoc.OCRStatus != "QUEUED" {
		t.Fatalf("TriggerOCR: err=%v status=%s", err, ocrDoc.OCRStatus)
	}

	// 6. Legal hold blocks deletion until released.
	heldDoc, err := svc.SetLegalHold(ctx, actor, doc.ID, true, ocrDoc.Version)
	if err != nil || !heldDoc.LegalHold {
		t.Fatalf("SetLegalHold: err=%v", err)
	}
	if _, err := svc.DeleteDocument(ctx, actor, doc.ID, heldDoc.Version); !errors.Is(err, ErrLegalHoldActive) {
		t.Fatalf("expected ErrLegalHoldActive, got %v", err)
	}
}

func TestExpiredUploadAndMalwareRejection(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	svc, primarySecret, _ := newTestService(t, &now)
	ctx := context.Background()
	actor := Actor{TenantID: "tenant-a", ActorID: "user-1", Capabilities: []string{"document.read", "document.write", "document.download"}}

	// Expired upload cannot finalize.
	expSess, _, err := svc.CreateUploadSession(ctx, actor, CreateUploadRequest{
		OwnerRefType: "CASE", OwnerRefID: "c1", FileName: "exp.pdf", DeclaredMIME: "application/pdf", DeclaredSizeBytes: 100, IdempotencyKey: "exp-1",
	})
	if err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	now = now.Add(DefaultUploadTTL + time.Minute)
	if _, _, err := svc.FinalizeUpload(ctx, actor, FinalizeUploadRequest{
		UploadID: expSess.ID, ExpectedVersion: expSess.Version, ActualSizeBytes: 100, DetectedMIME: "application/pdf", ContentSHA256: strings.Repeat("b", 64), IdempotencyKey: "fin-exp",
	}); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}

	// Malware verdict keeps document quarantined/rejected and non-downloadable.
	malSess, _, err := svc.CreateUploadSession(ctx, actor, CreateUploadRequest{
		OwnerRefType: "CASE", OwnerRefID: "c2", FileName: "malware.pdf", DeclaredMIME: "application/pdf", DeclaredSizeBytes: 200, IdempotencyKey: "mal-1",
	})
	if err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	malDoc, _, err := svc.FinalizeUpload(ctx, actor, FinalizeUploadRequest{
		UploadID: malSess.ID, ExpectedVersion: malSess.Version, ActualSizeBytes: 200, DetectedMIME: "application/pdf", ContentSHA256: strings.Repeat("c", 64), IdempotencyKey: "fin-mal",
	})
	if err != nil {
		t.Fatalf("FinalizeUpload: %v", err)
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	cb, _ := json.Marshal(ScanCallbackPayload{
		ExternalEventID: "av-mal-1", TenantID: "tenant-a", DocumentID: malDoc.ID, DocumentVersion: malDoc.Version, Verdict: "REJECTED", Reason: "EICAR-Test-Signature",
	})
	if _, err := svc.ProcessScanCallback(ctx, "clamav", ts, signCallback(primarySecret, ts, cb), cb); err != nil {
		t.Fatalf("ProcessScanCallback: %v", err)
	}
	if _, err := svc.IssueDownloadURL(ctx, actor, malDoc.ID); !errors.Is(err, ErrQuarantined) {
		t.Fatalf("malware-rejected doc must not be downloadable, got %v", err)
	}
}
