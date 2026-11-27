package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidInput     = errors.New("invalid input")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrSSRFBlocked      = errors.New("destination blocked by SSRF/egress policy")
	ErrCircuitOpen      = errors.New("endpoint circuit breaker is open")
	ErrBulkheadFull     = errors.New("endpoint bulkhead capacity exceeded")
	ErrInvalidSignature = errors.New("invalid callback signature")
	ErrStaleTimestamp   = errors.New("callback timestamp outside skew window")
)

type SystemType string

const (
	SystemT24ESB  SystemType = "T24_ESB"
	SystemAML     SystemType = "AML"
	SystemRPA     SystemType = "RPA"
	SystemIDPOCR  SystemType = "IDP_OCR"
	SystemWebhook SystemType = "WEBHOOK"
)

type DeliveryStatus string

const (
	DeliveryQueued         DeliveryStatus = "QUEUED"
	DeliveryDispatching    DeliveryStatus = "DISPATCHING"
	DeliveryRetryWait      DeliveryStatus = "RETRY_WAIT"
	DeliverySucceeded      DeliveryStatus = "SUCCEEDED"
	DeliveryFailedTerminal DeliveryStatus = "FAILED_TERMINAL"
	DeliveryReconciling    DeliveryStatus = "RECONCILING"
	DeliveryCompensated    DeliveryStatus = "COMPENSATED"
)

type CircuitState string

const (
	CircuitClosed   CircuitState = "CLOSED"
	CircuitOpen     CircuitState = "OPEN"
	CircuitHalfOpen CircuitState = "HALF_OPEN"
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

type Endpoint struct {
	ID                      string       `json:"id"`
	TenantID                string       `json:"tenant_id"`
	Name                    string       `json:"name"`
	SystemType              SystemType   `json:"system_type"`
	BaseURL                 string       `json:"base_url"`
	SecretRef               string       `json:"secret_ref"` // reference only; never raw secret material
	AuthMode                string       `json:"auth_mode"`
	TimeoutMS               int          `json:"timeout_ms"`
	MaxAttempts             int          `json:"max_attempts"`
	CircuitFailureThreshold int          `json:"circuit_failure_threshold"`
	CircuitOpenSeconds      int          `json:"circuit_open_seconds"`
	BulkheadMaxConcurrency  int          `json:"bulkhead_max_concurrency"`
	Status                  string       `json:"status"`
	CircuitState            CircuitState `json:"circuit_state"`
	Version                 int64        `json:"version"`
	CreatedAt               time.Time    `json:"created_at"`
	UpdatedAt               time.Time    `json:"updated_at"`
}

type Delivery struct {
	ID                       string         `json:"id"`
	TenantID                 string         `json:"tenant_id"`
	EndpointID               string         `json:"endpoint_id"`
	CorrelationID            string         `json:"correlation_id"`
	DownstreamIdempotencyKey string         `json:"downstream_idempotency_key"`
	PayloadDigest            string         `json:"payload_digest"`
	RedactedSummary          string         `json:"redacted_summary"`
	Status                   DeliveryStatus `json:"status"`
	AttemptCount             int            `json:"attempt_count"`
	MaxAttempts              int            `json:"max_attempts"`
	NextAttemptAt            *time.Time     `json:"next_attempt_at,omitempty"`
	LastResponseClass        string         `json:"last_response_class,omitempty"`
	Version                  int64          `json:"version"`
	CreatedAt                time.Time      `json:"created_at"`
	UpdatedAt                time.Time      `json:"updated_at"`
}

type CallbackBinding struct {
	BindingID   string
	ProviderKey string
	TenantID    string
	Secrets     [][]byte // active + rotated overlap secrets
}

type CallbackReceipt struct {
	ReceiptID string `json:"receipt_id"`
	Status    string `json:"status"` // ACCEPTED | DUPLICATE
}

type OutboxEvent struct {
	EventID          string    `json:"event_id"`
	TenantID         string    `json:"tenant_id"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion int64     `json:"aggregate_version"`
	EventType        string    `json:"event_type"`
	CorrelationID    string    `json:"correlation_id"`
	PayloadJSON      string    `json:"payload_json"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type DispatchOutcome struct {
	ResponseClass string // "2xx", "4xx", "429", "5xx", "TIMEOUT"
	Ambiguous     bool   // true when request may have committed at provider before timeout
	Err           error
}

type Dispatcher interface {
	Dispatch(ctx context.Context, ep Endpoint, del Delivery) DispatchOutcome
}

type CreateEndpointRequest struct {
	Name                    string     `json:"name"`
	SystemType              SystemType `json:"system_type"`
	BaseURL                 string     `json:"base_url"`
	SecretRef               string     `json:"secret_ref"`
	AuthMode                string     `json:"auth_mode"`
	TimeoutMS               int        `json:"timeout_ms"`
	MaxAttempts             int        `json:"max_attempts"`
	CircuitFailureThreshold int        `json:"circuit_failure_threshold"`
	CircuitOpenSeconds      int        `json:"circuit_open_seconds"`
	BulkheadMaxConcurrency  int        `json:"bulkhead_max_concurrency"`
	IdempotencyKey          string     `json:"-"`
}

type DispatchRequest struct {
	EndpointID               string `json:"endpoint_id"`
	DownstreamIdempotencyKey string `json:"downstream_idempotency_key"`
	PayloadDigest            string `json:"payload_digest"`
	RedactedSummary          string `json:"redacted_summary"`
}

type RetryDeliveryRequest struct {
	DeliveryID      string `json:"-"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"-"`
}

type Config struct {
	AllowedEgressHosts []string
	Bindings           []CallbackBinding
	Dispatcher         Dispatcher
	MaxCallbackSkew    time.Duration
	Now                func() time.Time
}

type circuitTracker struct {
	state           CircuitState
	consecutiveFail int
	openedAt        time.Time
}

type callbackInboxEntry struct {
	Digest    string
	ReceiptID string
}

type idempotencyEntry struct {
	Digest   string
	Response []byte
}

type Service struct {
	mu           sync.Mutex
	cfg          Config
	seq          int64
	allowedHosts map[string]bool
	bindings     map[string]CallbackBinding
	endpoints    map[string]*Endpoint
	circuits     map[string]*circuitTracker
	bulkheads    map[string]chan struct{}
	deliveries   map[string]*Delivery
	callbackBox  map[string]callbackInboxEntry
	idempotency  map[string]idempotencyEntry
	outbox       []OutboxEvent
	outboxSink   func(context.Context, OutboxEvent) error
}

func (s *Service) SetOutboxSink(sink func(context.Context, OutboxEvent) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outboxSink = sink
}

func NewService(cfg Config) *Service {
	if cfg.MaxCallbackSkew <= 0 {
		cfg.MaxCallbackSkew = 5 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	allowed := make(map[string]bool, len(cfg.AllowedEgressHosts))
	for _, h := range cfg.AllowedEgressHosts {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	bindings := make(map[string]CallbackBinding, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		bindings[b.BindingID+"|"+b.ProviderKey] = b
	}
	return &Service{
		cfg:          cfg,
		allowedHosts: allowed,
		bindings:     bindings,
		endpoints:    make(map[string]*Endpoint),
		circuits:     make(map[string]*circuitTracker),
		bulkheads:    make(map[string]chan struct{}),
		deliveries:   make(map[string]*Delivery),
		callbackBox:  make(map[string]callbackInboxEntry),
		idempotency:  make(map[string]idempotencyEntry),
	}
}

func (s *Service) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%06d", prefix, s.seq)
}

func (s *Service) validateEgressURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: must be an HTTPS URL without userinfo", ErrSSRFBlocked)
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		if !s.allowedHosts[host] {
			return fmt.Errorf("%w: internal host %q not allowlisted", ErrSSRFBlocked, host)
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("%w: private/loopback/link-local IP %s denied", ErrSSRFBlocked, host)
		}
	}
	if len(s.allowedHosts) > 0 && !s.allowedHosts[host] {
		return fmt.Errorf("%w: host %q outside egress allowlist", ErrSSRFBlocked, host)
	}
	return nil
}

func (s *Service) CreateEndpoint(_ context.Context, actor Actor, req CreateEndpointRequest) (Endpoint, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Endpoint{}, ErrUnauthorized
	}
	if !actor.HasCapability("integration.endpoint.write") {
		return Endpoint{}, ErrForbidden
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.SecretRef) == "" {
		return Endpoint{}, fmt.Errorf("%w: name and secret_ref are required", ErrInvalidInput)
	}
	if err := s.validateEgressURL(req.BaseURL); err != nil {
		return Endpoint{}, err
	}
	if req.TimeoutMS <= 0 || req.TimeoutMS > 30000 {
		req.TimeoutMS = 5000
	}
	if req.MaxAttempts <= 0 || req.MaxAttempts > 10 {
		req.MaxAttempts = 3
	}
	if req.CircuitFailureThreshold <= 0 {
		req.CircuitFailureThreshold = 5
	}
	if req.CircuitOpenSeconds <= 0 {
		req.CircuitOpenSeconds = 30
	}
	if req.BulkheadMaxConcurrency <= 0 {
		req.BulkheadMaxConcurrency = 8
	}
	if req.AuthMode == "" {
		req.AuthMode = "MTLS"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now().UTC()
	id := s.nextID("ep")
	ep := &Endpoint{
		ID:                      id,
		TenantID:                actor.TenantID,
		Name:                    strings.TrimSpace(req.Name),
		SystemType:              req.SystemType,
		BaseURL:                 strings.TrimSpace(req.BaseURL),
		SecretRef:               strings.TrimSpace(req.SecretRef),
		AuthMode:                req.AuthMode,
		TimeoutMS:               req.TimeoutMS,
		MaxAttempts:             req.MaxAttempts,
		CircuitFailureThreshold: req.CircuitFailureThreshold,
		CircuitOpenSeconds:      req.CircuitOpenSeconds,
		BulkheadMaxConcurrency:  req.BulkheadMaxConcurrency,
		Status:                  "ACTIVE",
		CircuitState:            CircuitClosed,
		Version:                 1,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	s.endpoints[id] = ep
	s.circuits[id] = &circuitTracker{state: CircuitClosed}
	s.bulkheads[id] = make(chan struct{}, ep.BulkheadMaxConcurrency)
	return *ep, nil
}

func (s *Service) ListEndpoints(_ context.Context, actor Actor) ([]Endpoint, error) {
	if actor.TenantID == "" {
		return nil, ErrUnauthorized
	}
	if !actor.HasCapability("integration.endpoint.read") {
		return nil, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Endpoint, 0)
	for _, ep := range s.endpoints {
		if ep.TenantID == actor.TenantID {
			cp := *ep
			if c := s.circuits[ep.ID]; c != nil {
				cp.CircuitState = s.effectiveCircuitState(c, ep)
			}
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Service) effectiveCircuitState(c *circuitTracker, ep *Endpoint) CircuitState {
	if c.state == CircuitOpen && s.cfg.Now().UTC().Sub(c.openedAt) >= time.Duration(ep.CircuitOpenSeconds)*time.Second {
		return CircuitHalfOpen
	}
	return c.state
}

func (s *Service) DispatchOperation(ctx context.Context, actor Actor, req DispatchRequest) (Delivery, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Delivery{}, ErrUnauthorized
	}
	if !actor.HasCapability("integration.delivery.write") && !actor.HasCapability("integration.endpoint.write") {
		return Delivery{}, ErrForbidden
	}
	if req.EndpointID == "" || req.DownstreamIdempotencyKey == "" || req.PayloadDigest == "" {
		return Delivery{}, fmt.Errorf("%w: endpoint_id, downstream_idempotency_key, and payload_digest are required", ErrInvalidInput)
	}

	s.mu.Lock()
	ep, ok := s.endpoints[req.EndpointID]
	if !ok || ep.TenantID != actor.TenantID || ep.Status != "ACTIVE" {
		s.mu.Unlock()
		return Delivery{}, ErrNotFound
	}
	now := s.cfg.Now().UTC()
	del := &Delivery{
		ID:                       s.nextID("del"),
		TenantID:                 actor.TenantID,
		EndpointID:               ep.ID,
		CorrelationID:            actor.CorrelationID,
		DownstreamIdempotencyKey: req.DownstreamIdempotencyKey,
		PayloadDigest:            req.PayloadDigest,
		RedactedSummary:          req.RedactedSummary,
		Status:                   DeliveryQueued,
		MaxAttempts:              ep.MaxAttempts,
		Version:                  1,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	s.deliveries[del.ID] = del
	s.mu.Unlock()

	return s.executeAttempt(ctx, ep.ID, del.ID)
}

func (s *Service) executeAttempt(ctx context.Context, endpointID, deliveryID string) (Delivery, error) {
	s.mu.Lock()
	ep := s.endpoints[endpointID]
	del := s.deliveries[deliveryID]
	circuit := s.circuits[endpointID]
	bulkhead := s.bulkheads[endpointID]

	state := s.effectiveCircuitState(circuit, ep)
	if state == CircuitOpen {
		now := s.cfg.Now().UTC()
		del.Status = DeliveryRetryWait
		del.LastResponseClass = "CIRCUIT_OPEN"
		next := now.Add(time.Duration(ep.CircuitOpenSeconds) * time.Second)
		del.NextAttemptAt = &next
		del.Version++
		del.UpdatedAt = now
		cp := *del
		s.mu.Unlock()
		return cp, ErrCircuitOpen
	}

	// Non-blocking bulkhead acquisition (fail-fast overflow shedding).
	select {
	case bulkhead <- struct{}{}:
	default:
		cp := *del
		s.mu.Unlock()
		return cp, ErrBulkheadFull
	}

	now := s.cfg.Now().UTC()
	del.Status = DeliveryDispatching
	del.AttemptCount++
	del.Version++
	del.UpdatedAt = now
	epCopy := *ep
	delCopy := *del
	dispatcher := s.cfg.Dispatcher
	s.mu.Unlock()

	defer func() { <-bulkhead }()

	dispatchCtx, cancel := context.WithTimeout(ctx, time.Duration(epCopy.TimeoutMS)*time.Millisecond)
	defer cancel()

	var outcome DispatchOutcome
	if dispatcher != nil {
		outcome = dispatcher.Dispatch(dispatchCtx, epCopy, delCopy)
	} else {
		outcome = DispatchOutcome{ResponseClass: "2xx"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	doneAt := s.cfg.Now().UTC()
	del.LastResponseClass = outcome.ResponseClass
	del.UpdatedAt = doneAt
	del.Version++

	if outcome.Err == nil && outcome.ResponseClass == "2xx" {
		del.Status = DeliverySucceeded
		del.NextAttemptAt = nil
		circuit.consecutiveFail = 0
		circuit.state = CircuitClosed
		ep.CircuitState = CircuitClosed
		s.appendOutbox(del.TenantID, del.ID, del.Version, "IntegrationDeliverySucceeded", del.CorrelationID, map[string]any{
			"delivery_id": del.ID, "endpoint_id": del.EndpointID,
		}, doneAt)
		return *del, nil
	}

	circuit.consecutiveFail++
	if circuit.consecutiveFail >= ep.CircuitFailureThreshold {
		circuit.state = CircuitOpen
		circuit.openedAt = doneAt
		ep.CircuitState = CircuitOpen
	}

	if outcome.Ambiguous {
		del.Status = DeliveryReconciling
		next := doneAt.Add(10 * time.Second)
		del.NextAttemptAt = &next
		return *del, nil
	}
	if outcome.ResponseClass == "4xx" || del.AttemptCount >= del.MaxAttempts {
		del.Status = DeliveryFailedTerminal
		del.NextAttemptAt = nil
		s.appendOutbox(del.TenantID, del.ID, del.Version, "IntegrationDeliveryFailedTerminal", del.CorrelationID, map[string]any{
			"delivery_id": del.ID, "response_class": outcome.ResponseClass,
		}, doneAt)
		return *del, nil
	}

	del.Status = DeliveryRetryWait
	backoff := time.Duration(1<<min(del.AttemptCount, 5)) * time.Second
	next := doneAt.Add(backoff)
	del.NextAttemptAt = &next
	return *del, nil
}

func (s *Service) RetryDelivery(ctx context.Context, actor Actor, req RetryDeliveryRequest) (Delivery, bool, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Delivery{}, false, ErrUnauthorized
	}
	if !actor.HasCapability("integration.delivery.retry") {
		return Delivery{}, false, ErrForbidden
	}
	if req.DeliveryID == "" || req.ExpectedVersion <= 0 || req.IdempotencyKey == "" || strings.TrimSpace(req.Reason) == "" {
		return Delivery{}, false, fmt.Errorf("%w: delivery_id, expected_version, idempotency_key, and reason are required", ErrInvalidInput)
	}

	digestRaw, _ := json.Marshal(req)
	reqDigest := sha256Hex(digestRaw)
	idemKey := actor.TenantID + "|" + actor.ActorID + "|retry_delivery|" + req.IdempotencyKey

	s.mu.Lock()
	if existing, ok := s.idempotency[idemKey]; ok {
		s.mu.Unlock()
		if existing.Digest != reqDigest {
			return Delivery{}, false, ErrConflict
		}
		var replay Delivery
		_ = json.Unmarshal(existing.Response, &replay)
		return replay, true, nil
	}
	del, ok := s.deliveries[req.DeliveryID]
	if !ok || del.TenantID != actor.TenantID {
		s.mu.Unlock()
		return Delivery{}, false, ErrNotFound
	}
	if del.Version != req.ExpectedVersion {
		s.mu.Unlock()
		return Delivery{}, false, ErrConflict
	}
	if del.Status == DeliverySucceeded || del.Status == DeliveryCompensated {
		s.mu.Unlock()
		return Delivery{}, false, fmt.Errorf("%w: delivery already terminal (%s)", ErrConflict, del.Status)
	}
	if del.AttemptCount >= del.MaxAttempts {
		del.MaxAttempts = del.AttemptCount + 1 // explicit operator-authorized bounded retry attempt
	}
	endpointID := del.EndpointID
	s.mu.Unlock()

	updated, err := s.executeAttempt(ctx, endpointID, req.DeliveryID)
	encoded, _ := json.Marshal(updated)
	s.mu.Lock()
	s.idempotency[idemKey] = idempotencyEntry{Digest: reqDigest, Response: encoded}
	s.mu.Unlock()
	return updated, false, err
}

func (s *Service) CompensateDelivery(_ context.Context, actor Actor, deliveryID string, expectedVersion int64, reason string) (Delivery, error) {
	if actor.TenantID == "" || actor.ActorID == "" {
		return Delivery{}, ErrUnauthorized
	}
	if !actor.HasCapability("integration.delivery.retry") {
		return Delivery{}, ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return Delivery{}, fmt.Errorf("%w: compensation reason required", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	del, ok := s.deliveries[deliveryID]
	if !ok || del.TenantID != actor.TenantID {
		return Delivery{}, ErrNotFound
	}
	if del.Version != expectedVersion {
		return Delivery{}, ErrConflict
	}
	now := s.cfg.Now().UTC()
	del.Status = DeliveryCompensated
	del.NextAttemptAt = nil
	del.Version++
	del.UpdatedAt = now
	s.appendOutbox(del.TenantID, del.ID, del.Version, "IntegrationDeliveryCompensated", actor.CorrelationID, map[string]any{
		"delivery_id": del.ID, "reason": reason,
	}, now)
	return *del, nil
}

func (s *Service) ProcessCallback(_ context.Context, bindingID, providerKey, timestampHeader, signatureHex, externalEventID string, rawBody []byte) (CallbackReceipt, error) {
	binding, ok := s.bindings[bindingID+"|"+providerKey]
	if !ok {
		return CallbackReceipt{}, ErrInvalidSignature // never disclose tenant/binding existence
	}
	tsSec, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return CallbackReceipt{}, ErrStaleTimestamp
	}
	now := s.cfg.Now().UTC()
	diff := now.Sub(time.Unix(tsSec, 0).UTC())
	if diff < 0 {
		diff = -diff
	}
	if diff > s.cfg.MaxCallbackSkew {
		return CallbackReceipt{}, ErrStaleTimestamp
	}
	if !verifyAnyHMAC(binding.Secrets, timestampHeader, externalEventID, rawBody, signatureHex) {
		return CallbackReceipt{}, ErrInvalidSignature
	}
	if strings.TrimSpace(externalEventID) == "" {
		return CallbackReceipt{}, fmt.Errorf("%w: external_event_id required", ErrInvalidInput)
	}

	digest := sha256Hex(rawBody)
	inboxKey := bindingID + "|" + providerKey + "|" + externalEventID

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, exists := s.callbackBox[inboxKey]; exists {
		if existing.Digest == digest {
			return CallbackReceipt{ReceiptID: existing.ReceiptID, Status: "DUPLICATE"}, nil
		}
		return CallbackReceipt{}, ErrConflict
	}
	receiptID := s.nextID("cb")
	s.callbackBox[inboxKey] = callbackInboxEntry{Digest: digest, ReceiptID: receiptID}
	s.appendOutbox(binding.TenantID, receiptID, 1, "IntegrationCallbackVerified", externalEventID, map[string]any{
		"binding_id": bindingID, "provider_key": providerKey, "external_event_id": externalEventID, "payload_digest": digest,
	}, now)
	return CallbackReceipt{ReceiptID: receiptID, Status: "ACCEPTED"}, nil
}

func (s *Service) GetDelivery(_ context.Context, actor Actor, deliveryID string) (Delivery, error) {
	if actor.TenantID == "" {
		return Delivery{}, ErrUnauthorized
	}
	if !actor.HasCapability("integration.delivery.read") {
		return Delivery{}, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	del, ok := s.deliveries[deliveryID]
	if !ok || del.TenantID != actor.TenantID {
		return Delivery{}, ErrNotFound
	}
	return *del, nil
}

func (s *Service) ListDeliveries(_ context.Context, actor Actor) ([]Delivery, error) {
	if actor.TenantID == "" {
		return nil, ErrUnauthorized
	}
	if !actor.HasCapability("integration.delivery.read") {
		return nil, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Delivery, 0)
	for _, del := range s.deliveries {
		if del.TenantID == actor.TenantID {
			out = append(out, *del)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (s *Service) OutboxEvents() []OutboxEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]OutboxEvent, len(s.outbox))
	copy(cp, s.outbox)
	return cp
}

func (s *Service) appendOutbox(tenantID, aggID string, version int64, eventType, corrID string, payload map[string]any, now time.Time) {
	b, _ := json.Marshal(payload)
	evt := OutboxEvent{
		EventID:          s.nextID("evt"),
		TenantID:         tenantID,
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

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func verifyAnyHMAC(secrets [][]byte, ts, eventID string, rawBody []byte, sigHex string) bool {
	provided, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil || len(provided) == 0 {
		return false
	}
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(ts + "." + eventID + "."))
		mac.Write(rawBody)
		if hmac.Equal(mac.Sum(nil), provided) {
			return true
		}
	}
	return false
}
