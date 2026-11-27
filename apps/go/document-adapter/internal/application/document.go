package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxUploadSizeBytes int64 = 50 * 1024 * 1024 // 50 MiB boundary per ODS architecture plan
	DefaultUploadTTL         = 15 * time.Minute
	DefaultDownloadTTL       = 5 * time.Minute
	DefaultMaxCallbackSkew   = 5 * time.Minute
)

var (
	ErrInvalidInput      = errors.New("invalid input")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrExpired           = errors.New("upload session expired")
	ErrSizeLimitExceeded = errors.New("file size exceeds 50 MiB limit")
	ErrUnsupportedMIME   = errors.New("unsupported MIME type")
	ErrQuarantined       = errors.New("document is not clean and cannot be accessed")
	ErrLegalHoldActive   = errors.New("document is under active legal hold")
	ErrInvalidSignature  = errors.New("invalid callback signature")
	ErrStaleTimestamp    = errors.New("callback timestamp outside allowed skew window")
)

type Status string

const (
	StatusUploadPending Status = "UPLOAD_PENDING"
	StatusQuarantined   Status = "QUARANTINED"
	StatusScanning      Status = "SCANNING"
	StatusClean         Status = "CLEAN"
	StatusRejected      Status = "REJECTED"
	StatusExpired       Status = "EXPIRED"
	StatusDeleted       Status = "DELETED"
)

type Actor struct {
	TenantID      string
	ActorID       string
	CorrelationID string
	Capabilities  []string
}

func (a Actor) HasCapability(cap string) bool {
	for _, c := range a.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

type UploadSession struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenant_id"`
	OwnerRefType        string    `json:"owner_ref_type"`
	OwnerRefID          string    `json:"owner_ref_id"`
	FileName            string    `json:"file_name"`
	DeclaredMIME        string    `json:"declared_mime"`
	DeclaredSizeBytes   int64     `json:"declared_size_bytes"`
	QuarantineObjectKey string    `json:"quarantine_object_key"`
	SignedUploadURL     string    `json:"signed_upload_url"`
	Status              Status    `json:"status"`
	Version             int64     `json:"version"`
	ExpiresAt           time.Time `json:"expires_at"`
	CreatedBy           string    `json:"created_by"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Document struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	UploadSessionID string     `json:"upload_session_id"`
	OwnerRefType    string     `json:"owner_ref_type"`
	OwnerRefID      string     `json:"owner_ref_id"`
	FileName        string     `json:"file_name"`
	MIMEType        string     `json:"mime_type"`
	SizeBytes       int64      `json:"size_bytes"`
	ContentSHA256   string     `json:"content_sha256"`
	StorageKey      string     `json:"storage_object_key"`
	Status          Status     `json:"status"`
	LegalHold       bool       `json:"legal_hold"`
	RetentionUntil  *time.Time `json:"retention_until,omitempty"`
	OCRStatus       string     `json:"ocr_status"`
	Version         int64      `json:"version"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type OutboxEvent struct {
	EventID          string    `json:"event_id"`
	TenantID         string    `json:"tenant_id"`
	AggregateType    string    `json:"aggregate_type"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion int64     `json:"aggregate_version"`
	EventType        string    `json:"event_type"`
	CorrelationID    string    `json:"correlation_id"`
	PayloadJSON      string    `json:"payload_json"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type AuditRecord struct {
	AuditID       string    `json:"audit_id"`
	TenantID      string    `json:"tenant_id"`
	ActorID       string    `json:"actor_id"`
	DocumentID    string    `json:"document_id"`
	Action        string    `json:"action"`
	CorrelationID string    `json:"correlation_id"`
	Details       string    `json:"details"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type ScanCallbackReceipt struct {
	ReceiptID string `json:"receipt_id"`
	Status    string `json:"status"` // APPLIED | DUPLICATE | IGNORED_STALE | REJECTED_CONFLICT
}

type CreateUploadRequest struct {
	OwnerRefType      string `json:"owner_ref_type"`
	OwnerRefID        string `json:"owner_ref_id"`
	FileName          string `json:"file_name"`
	DeclaredMIME      string `json:"declared_mime"`
	DeclaredSizeBytes int64  `json:"declared_size_bytes"`
	IdempotencyKey    string `json:"-"`
}

type FinalizeUploadRequest struct {
	UploadID        string `json:"-"`
	ExpectedVersion int64  `json:"expected_version"`
	ActualSizeBytes int64  `json:"actual_size_bytes"`
	DetectedMIME    string `json:"detected_mime"`
	ContentSHA256   string `json:"content_sha256"`
	IdempotencyKey  string `json:"-"`
}

type ScanCallbackPayload struct {
	ExternalEventID string `json:"external_event_id"`
	TenantID        string `json:"tenant_id"`
	DocumentID      string `json:"document_id"`
	DocumentVersion int64  `json:"document_version"`
	Verdict         string `json:"verdict"` // CLEAN | REJECTED
	Reason          string `json:"reason,omitempty"`
}

type DownloadURLResponse struct {
	DocumentID string    `json:"document_id"`
	URL        string    `json:"url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type ListDocumentsFilter struct {
	OwnerRefType string
	OwnerRefID   string
	Status       Status
	Limit        int
	AfterID      string
}

type ListDocumentsResponse struct {
	Documents []Document `json:"documents"`
	NextID    string     `json:"next_id,omitempty"`
	AsOf      time.Time  `json:"as_of"`
}

type Config struct {
	CSPEndpoint          string
	URLSigningSecret     []byte
	CallbackSecrets      map[string][][]byte // providerKey -> list of active/rotated HMAC secrets
	AllowedMIMETypes     map[string]bool
	UploadTTL            time.Duration
	DownloadTTL          time.Duration
	MaxCallbackSkew      time.Duration
	Now                  func() time.Time
}

func DefaultAllowedMIMETypes() map[string]bool {
	return map[string]bool{
		"application/pdf": true,
		"image/png":       true,
		"image/jpeg":      true,
		"text/plain":      true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       true,
	}
}

type idempotencyEntry struct {
	Digest   string
	Response []byte
}

type callbackInboxEntry struct {
	Digest    string
	ReceiptID string
	Status    string
}

type Service struct {
	mu           sync.RWMutex
	cfg          Config
	seq          int64
	uploads      map[string]*UploadSession
	documents    map[string]*Document
	idempotency  map[string]idempotencyEntry
	callbackBox  map[string]callbackInboxEntry
	outbox       []OutboxEvent
	outboxSink   func(context.Context, OutboxEvent) error
	audit        []AuditRecord
}

func (s *Service) SetOutboxSink(sink func(context.Context, OutboxEvent) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outboxSink = sink
}

func NewService(cfg Config) (*Service, error) {
	if len(cfg.URLSigningSecret) < 16 {
		return nil, fmt.Errorf("%w: URL signing secret must be at least 16 bytes", ErrInvalidInput)
	}
	if cfg.CSPEndpoint == "" {
		cfg.CSPEndpoint = "https://csp.internal.local"
	}
	if len(cfg.AllowedMIMETypes) == 0 {
		cfg.AllowedMIMETypes = DefaultAllowedMIMETypes()
	}
	if cfg.UploadTTL <= 0 {
		cfg.UploadTTL = DefaultUploadTTL
	}
	if cfg.DownloadTTL <= 0 {
		cfg.DownloadTTL = DefaultDownloadTTL
	}
	if cfg.MaxCallbackSkew <= 0 {
		cfg.MaxCallbackSkew = DefaultMaxCallbackSkew
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{
		cfg:         cfg,
		uploads:     make(map[string]*UploadSession),
		documents:   make(map[string]*Document),
		idempotency: make(map[string]idempotencyEntry),
		callbackBox: make(map[string]callbackInboxEntry),
	}, nil
}

func (s *Service) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%06d", prefix, s.seq)
}

func digestJSON(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Service) signURL(action, tenantID, objectKey string, expiresAt time.Time) string {
	exp := strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, s.cfg.URLSigningSecret)
	mac.Write([]byte(action + "\n" + tenantID + "\n" + objectKey + "\n" + exp))
	sig := hex.EncodeToString(mac.Sum(nil))
	q := url.Values{}
	q.Set("exp", exp)
	q.Set("sig", sig)
	return strings.TrimRight(s.cfg.CSPEndpoint, "/") + "/" + action + "/" + url.PathEscape(objectKey) + "?" + q.Encode()
}

func (s *Service) CreateUploadSession(_ context.Context, actor Actor, req CreateUploadRequest) (UploadSession, bool, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return UploadSession{}, false, ErrUnauthorized
	}
	if !actor.HasCapability("document.write") {
		return UploadSession{}, false, ErrForbidden
	}
	if req.OwnerRefType == "" || req.OwnerRefID == "" || strings.TrimSpace(req.FileName) == "" || req.IdempotencyKey == "" {
		return UploadSession{}, false, fmt.Errorf("%w: owner_ref, file_name, and idempotency_key are required", ErrInvalidInput)
	}
	if req.DeclaredSizeBytes <= 0 {
		return UploadSession{}, false, fmt.Errorf("%w: declared_size_bytes must be positive", ErrInvalidInput)
	}
	if req.DeclaredSizeBytes > MaxUploadSizeBytes {
		return UploadSession{}, false, ErrSizeLimitExceeded
	}
	if !s.cfg.AllowedMIMETypes[strings.ToLower(strings.TrimSpace(req.DeclaredMIME))] {
		return UploadSession{}, false, ErrUnsupportedMIME
	}

	reqDigest := digestJSON(req)
	idemKey := actor.TenantID + "|" + actor.ActorID + "|create_upload|" + req.IdempotencyKey

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.idempotency[idemKey]; ok {
		if existing.Digest != reqDigest {
			return UploadSession{}, false, fmt.Errorf("%w: idempotency key reused with different payload", ErrConflict)
		}
		var replay UploadSession
		_ = json.Unmarshal(existing.Response, &replay)
		return replay, true, nil
	}

	now := s.cfg.Now().UTC()
	id := s.nextID("upl")
	objectKey := fmt.Sprintf("quarantine/%s/%s/%s", actor.TenantID, id, sanitizeFileName(req.FileName))
	expiresAt := now.Add(s.cfg.UploadTTL)
	session := &UploadSession{
		ID:                  id,
		TenantID:            actor.TenantID,
		OwnerRefType:        req.OwnerRefType,
		OwnerRefID:          req.OwnerRefID,
		FileName:            req.FileName,
		DeclaredMIME:        strings.ToLower(strings.TrimSpace(req.DeclaredMIME)),
		DeclaredSizeBytes:   req.DeclaredSizeBytes,
		QuarantineObjectKey: objectKey,
		SignedUploadURL:     s.signURL("upload", actor.TenantID, objectKey, expiresAt),
		Status:              StatusUploadPending,
		Version:             1,
		ExpiresAt:           expiresAt,
		CreatedBy:           actor.ActorID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	s.uploads[id] = session
	s.appendAudit(actor, id, "UPLOAD_SESSION_CREATED", fmt.Sprintf("size=%d mime=%s", req.DeclaredSizeBytes, session.DeclaredMIME), now)

	encoded, _ := json.Marshal(session)
	s.idempotency[idemKey] = idempotencyEntry{Digest: reqDigest, Response: encoded}
	return *session, false, nil
}

func (s *Service) FinalizeUpload(_ context.Context, actor Actor, req FinalizeUploadRequest) (Document, bool, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Document{}, false, ErrUnauthorized
	}
	if !actor.HasCapability("document.write") {
		return Document{}, false, ErrForbidden
	}
	if req.UploadID == "" || req.ExpectedVersion <= 0 || req.IdempotencyKey == "" || len(req.ContentSHA256) != 64 {
		return Document{}, false, fmt.Errorf("%w: upload_id, expected_version, 64-hex content_sha256, and idempotency_key are required", ErrInvalidInput)
	}
	if _, err := hex.DecodeString(req.ContentSHA256); err != nil {
		return Document{}, false, fmt.Errorf("%w: content_sha256 must be valid hex", ErrInvalidInput)
	}

	reqDigest := digestJSON(req)
	idemKey := actor.TenantID + "|" + actor.ActorID + "|finalize_upload|" + req.IdempotencyKey

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.idempotency[idemKey]; ok {
		if existing.Digest != reqDigest {
			return Document{}, false, fmt.Errorf("%w: idempotency key reused with different payload", ErrConflict)
		}
		var replay Document
		_ = json.Unmarshal(existing.Response, &replay)
		return replay, true, nil
	}

	session, ok := s.uploads[req.UploadID]
	if !ok || session.TenantID != actor.TenantID {
		return Document{}, false, ErrNotFound
	}
	now := s.cfg.Now().UTC()
	if now.After(session.ExpiresAt) && session.Status == StatusUploadPending {
		session.Status = StatusExpired
		session.Version++
		session.UpdatedAt = now
		return Document{}, false, ErrExpired
	}
	if session.Status != StatusUploadPending {
		return Document{}, false, fmt.Errorf("%w: upload session is %s", ErrConflict, session.Status)
	}
	if session.Version != req.ExpectedVersion {
		return Document{}, false, fmt.Errorf("%w: expected version %d, current %d", ErrConflict, req.ExpectedVersion, session.Version)
	}
	if req.ActualSizeBytes <= 0 || req.ActualSizeBytes > MaxUploadSizeBytes || req.ActualSizeBytes != session.DeclaredSizeBytes {
		return Document{}, false, fmt.Errorf("%w: actual size mismatch or exceeds 50 MiB", ErrInvalidInput)
	}
	detectedMIME := strings.ToLower(strings.TrimSpace(req.DetectedMIME))
	if detectedMIME != session.DeclaredMIME || !s.cfg.AllowedMIMETypes[detectedMIME] {
		return Document{}, false, fmt.Errorf("%w: detected MIME %q does not match declared %q", ErrUnsupportedMIME, detectedMIME, session.DeclaredMIME)
	}

	session.Status = StatusScanning
	session.Version++
	session.UpdatedAt = now

	docID := s.nextID("doc")
	doc := &Document{
		ID:              docID,
		TenantID:        actor.TenantID,
		UploadSessionID: session.ID,
		OwnerRefType:    session.OwnerRefType,
		OwnerRefID:      session.OwnerRefID,
		FileName:        session.FileName,
		MIMEType:        detectedMIME,
		SizeBytes:       req.ActualSizeBytes,
		ContentSHA256:   strings.ToLower(req.ContentSHA256),
		StorageKey:      session.QuarantineObjectKey,
		Status:          StatusScanning,
		OCRStatus:       "NOT_REQUESTED",
		Version:         1,
		CreatedBy:       actor.ActorID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.documents[docID] = doc
	s.appendOutbox(actor.TenantID, docID, doc.Version, "DocumentScanRequested", actor.CorrelationID, map[string]any{
		"document_id":    docID,
		"content_sha256": doc.ContentSHA256,
		"storage_key":    doc.StorageKey,
	}, now)
	s.appendAudit(actor, docID, "UPLOAD_FINALIZED_QUARANTINED", "enqueued for AV scan", now)

	encoded, _ := json.Marshal(doc)
	s.idempotency[idemKey] = idempotencyEntry{Digest: reqDigest, Response: encoded}
	return *doc, false, nil
}

func (s *Service) ProcessScanCallback(_ context.Context, providerKey, timestampHeader, signatureHex string, rawBody []byte) (ScanCallbackReceipt, error) {
	secrets := s.cfg.CallbackSecrets[providerKey]
	if len(secrets) == 0 {
		return ScanCallbackReceipt{}, ErrInvalidSignature
	}
	tsSec, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return ScanCallbackReceipt{}, ErrStaleTimestamp
	}
	now := s.cfg.Now().UTC()
	ts := time.Unix(tsSec, 0).UTC()
	diff := now.Sub(ts)
	if diff < 0 {
		diff = -diff
	}
	if diff > s.cfg.MaxCallbackSkew {
		return ScanCallbackReceipt{}, ErrStaleTimestamp
	}

	if !verifyAnyHMAC(secrets, timestampHeader, rawBody, signatureHex) {
		return ScanCallbackReceipt{}, ErrInvalidSignature
	}

	var payload ScanCallbackPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return ScanCallbackReceipt{}, fmt.Errorf("%w: invalid callback json", ErrInvalidInput)
	}
	if payload.ExternalEventID == "" || payload.TenantID == "" || payload.DocumentID == "" || payload.DocumentVersion <= 0 {
		return ScanCallbackReceipt{}, fmt.Errorf("%w: missing callback identifiers", ErrInvalidInput)
	}
	if payload.Verdict != "CLEAN" && payload.Verdict != "REJECTED" {
		return ScanCallbackReceipt{}, fmt.Errorf("%w: verdict must be CLEAN or REJECTED", ErrInvalidInput)
	}

	sum := sha256.Sum256(rawBody)
	payloadDigest := hex.EncodeToString(sum[:])
	inboxKey := providerKey + "|" + payload.ExternalEventID

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.callbackBox[inboxKey]; ok {
		if existing.Digest == payloadDigest {
			return ScanCallbackReceipt{ReceiptID: existing.ReceiptID, Status: "DUPLICATE"}, nil
		}
		receiptID := s.nextID("rcpt")
		s.appendAudit(Actor{TenantID: payload.TenantID, ActorID: "scanner:" + providerKey}, payload.DocumentID, "SCAN_CALLBACK_CONFLICT", "external_event_id reused with different digest", now)
		return ScanCallbackReceipt{ReceiptID: receiptID, Status: "REJECTED_CONFLICT"}, ErrConflict
	}

	doc, ok := s.documents[payload.DocumentID]
	if !ok || doc.TenantID != payload.TenantID {
		return ScanCallbackReceipt{}, ErrNotFound
	}

	receiptID := s.nextID("rcpt")
	// Late/stale callback cannot overwrite a newer version or already-finalized document.
	if payload.DocumentVersion < doc.Version || (doc.Status != StatusScanning && doc.Status != StatusQuarantined) {
		s.callbackBox[inboxKey] = callbackInboxEntry{Digest: payloadDigest, ReceiptID: receiptID, Status: "IGNORED_STALE"}
		s.appendAudit(Actor{TenantID: payload.TenantID, ActorID: "scanner:" + providerKey}, doc.ID, "SCAN_CALLBACK_IGNORED_STALE", fmt.Sprintf("cb_ver=%d cur_ver=%d status=%s", payload.DocumentVersion, doc.Version, doc.Status), now)
		return ScanCallbackReceipt{ReceiptID: receiptID, Status: "IGNORED_STALE"}, nil
	}

	doc.Version++
	doc.UpdatedAt = now
	if payload.Verdict == "CLEAN" {
		doc.Status = StatusClean
		doc.StorageKey = fmt.Sprintf("clean/%s/%s/%s", doc.TenantID, doc.ID, sanitizeFileName(doc.FileName))
		s.appendOutbox(doc.TenantID, doc.ID, doc.Version, "DocumentAvailable", payload.ExternalEventID, map[string]any{
			"document_id":    doc.ID,
			"owner_ref_type": doc.OwnerRefType,
			"owner_ref_id":   doc.OwnerRefID,
			"content_sha256": doc.ContentSHA256,
		}, now)
	} else {
		doc.Status = StatusRejected
		s.appendOutbox(doc.TenantID, doc.ID, doc.Version, "DocumentQuarantinedRejected", payload.ExternalEventID, map[string]any{
			"document_id": doc.ID,
			"reason":      payload.Reason,
		}, now)
	}

	s.callbackBox[inboxKey] = callbackInboxEntry{Digest: payloadDigest, ReceiptID: receiptID, Status: "APPLIED"}
	s.appendAudit(Actor{TenantID: doc.TenantID, ActorID: "scanner:" + providerKey}, doc.ID, "SCAN_VERDICT_"+payload.Verdict, payload.Reason, now)
	return ScanCallbackReceipt{ReceiptID: receiptID, Status: "APPLIED"}, nil
}

func (s *Service) IssueDownloadURL(_ context.Context, actor Actor, documentID string) (DownloadURLResponse, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return DownloadURLResponse{}, ErrUnauthorized
	}
	if !actor.HasCapability("document.download") && !actor.HasCapability("document.read") {
		return DownloadURLResponse{}, ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	doc, ok := s.documents[documentID]
	if !ok || doc.TenantID != actor.TenantID {
		return DownloadURLResponse{}, ErrNotFound
	}
	if doc.Status != StatusClean {
		return DownloadURLResponse{}, ErrQuarantined
	}
	now := s.cfg.Now().UTC()
	if doc.RetentionUntil != nil && now.After(*doc.RetentionUntil) && !doc.LegalHold {
		return DownloadURLResponse{}, fmt.Errorf("%w: document retention window expired", ErrForbidden)
	}
	expiresAt := now.Add(s.cfg.DownloadTTL)
	signed := s.signURL("download", doc.TenantID, doc.StorageKey, expiresAt)
	s.appendAudit(actor, doc.ID, "DOWNLOAD_URL_ISSUED", "ttl="+s.cfg.DownloadTTL.String(), now)
	return DownloadURLResponse{
		DocumentID: doc.ID,
		URL:        signed,
		ExpiresAt:  expiresAt,
	}, nil
}

func (s *Service) SetLegalHold(_ context.Context, actor Actor, documentID string, hold bool, expectedVersion int64) (Document, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Document{}, ErrUnauthorized
	}
	if !actor.HasCapability("document.hold") {
		return Document{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[documentID]
	if !ok || doc.TenantID != actor.TenantID {
		return Document{}, ErrNotFound
	}
	if doc.Version != expectedVersion {
		return Document{}, ErrConflict
	}
	now := s.cfg.Now().UTC()
	doc.LegalHold = hold
	doc.Version++
	doc.UpdatedAt = now
	s.appendAudit(actor, doc.ID, "LEGAL_HOLD_UPDATED", fmt.Sprintf("hold=%v", hold), now)
	return *doc, nil
}

func (s *Service) DeleteDocument(_ context.Context, actor Actor, documentID string, expectedVersion int64) (Document, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Document{}, ErrUnauthorized
	}
	if !actor.HasCapability("document.write") {
		return Document{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[documentID]
	if !ok || doc.TenantID != actor.TenantID {
		return Document{}, ErrNotFound
	}
	if doc.LegalHold {
		return Document{}, ErrLegalHoldActive
	}
	if doc.Version != expectedVersion {
		return Document{}, ErrConflict
	}
	now := s.cfg.Now().UTC()
	doc.Status = StatusDeleted
	doc.Version++
	doc.UpdatedAt = now
	s.appendOutbox(doc.TenantID, doc.ID, doc.Version, "DocumentDeleted", actor.CorrelationID, map[string]any{"document_id": doc.ID}, now)
	s.appendAudit(actor, doc.ID, "DOCUMENT_DELETED", "", now)
	return *doc, nil
}

func (s *Service) TriggerOCR(_ context.Context, actor Actor, documentID string) (Document, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Document{}, ErrUnauthorized
	}
	if !actor.HasCapability("document.write") {
		return Document{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[documentID]
	if !ok || doc.TenantID != actor.TenantID {
		return Document{}, ErrNotFound
	}
	if doc.Status != StatusClean {
		return Document{}, ErrQuarantined
	}
	now := s.cfg.Now().UTC()
	doc.OCRStatus = "QUEUED"
	doc.Version++
	doc.UpdatedAt = now
	s.appendOutbox(doc.TenantID, doc.ID, doc.Version, "DocumentOCRRequested", actor.CorrelationID, map[string]any{
		"document_id": doc.ID,
		"mime_type":   doc.MIMEType,
	}, now)
	s.appendAudit(actor, doc.ID, "OCR_TRIGGERED", "", now)
	return *doc, nil
}

func (s *Service) GetDocument(_ context.Context, actor Actor, documentID string) (Document, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Document{}, ErrUnauthorized
	}
	if !actor.HasCapability("document.read") {
		return Document{}, ErrForbidden
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.documents[documentID]
	if !ok || doc.TenantID != actor.TenantID {
		return Document{}, ErrNotFound
	}
	return *doc, nil
}

func (s *Service) ListDocuments(_ context.Context, actor Actor, filter ListDocumentsFilter) (ListDocumentsResponse, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return ListDocumentsResponse{}, ErrUnauthorized
	}
	if !actor.HasCapability("document.read") {
		return ListDocumentsResponse{}, ErrForbidden
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []Document
	for _, doc := range s.documents {
		if doc.TenantID != actor.TenantID {
			continue
		}
		if filter.OwnerRefType != "" && doc.OwnerRefType != filter.OwnerRefType {
			continue
		}
		if filter.OwnerRefID != "" && doc.OwnerRefID != filter.OwnerRefID {
			continue
		}
		if filter.Status != "" && doc.Status != filter.Status {
			continue
		}
		matched = append(matched, *doc)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID > matched[j].ID })

	if filter.AfterID != "" {
		filtered := matched[:0]
		for _, doc := range matched {
			if doc.ID < filter.AfterID {
				filtered = append(filtered, doc)
			}
		}
		matched = filtered
	}

	nextID := ""
	if len(matched) > limit {
		matched = matched[:limit]
		nextID = matched[len(matched)-1].ID
	}
	return ListDocumentsResponse{
		Documents: matched,
		NextID:    nextID,
		AsOf:      s.cfg.Now().UTC(),
	}, nil
}

func (s *Service) OutboxEvents() []OutboxEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]OutboxEvent, len(s.outbox))
	copy(cp, s.outbox)
	return cp
}

func (s *Service) AuditRecords() []AuditRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]AuditRecord, len(s.audit))
	copy(cp, s.audit)
	return cp
}

func (s *Service) appendOutbox(tenantID, aggID string, version int64, eventType, corrID string, payload map[string]any, now time.Time) {
	b, _ := json.Marshal(payload)
	evt := OutboxEvent{
		EventID:          s.nextID("evt"),
		TenantID:         tenantID,
		AggregateType:    "Document",
		AggregateID:      aggID,
		AggregateVersion: version,
		EventType:        eventType,
		CorrelationID:    corrID,
		PayloadJSON:      string(b),
		OccurredAt:       now,
	}
	s.outbox = append(s.outbox, evt)
	if s.outboxSink != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = s.outboxSink(ctx, evt)
		cancel()
	}
}

func (s *Service) appendAudit(actor Actor, docID, action, details string, now time.Time) {
	s.audit = append(s.audit, AuditRecord{
		AuditID:       s.nextID("aud"),
		TenantID:      actor.TenantID,
		ActorID:       actor.ActorID,
		DocumentID:    docID,
		Action:        action,
		CorrelationID: actor.CorrelationID,
		Details:       details,
		OccurredAt:    now,
	})
}

func verifyAnyHMAC(secrets [][]byte, timestampHeader string, rawBody []byte, signatureHex string) bool {
	provided, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil || len(provided) == 0 {
		return false
	}
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(timestampHeader))
		mac.Write([]byte("."))
		mac.Write(rawBody)
		if hmac.Equal(mac.Sum(nil), provided) {
			return true
		}
	}
	return false
}

func sanitizeFileName(name string) string {
	clean := strings.ReplaceAll(name, "/", "_")
	clean = strings.ReplaceAll(clean, "\\", "_")
	clean = strings.ReplaceAll(clean, "..", "_")
	if len(clean) > 120 {
		clean = clean[:120]
	}
	return clean
}
