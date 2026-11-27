package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type InAppSink interface {
	DeliverInApp(ctx context.Context, msg RenderedMessage) (messageID string, err error)
}

type InAppDriver struct {
	sink InAppSink
}

func NewInAppDriver(sink InAppSink) *InAppDriver {
	return &InAppDriver{sink: sink}
}

func (d *InAppDriver) Kind() string { return "inapp" }

func (d *InAppDriver) ConfigSchema() []byte {
	return []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":true}`)
}

func (d *InAppDriver) Open(_ context.Context, _ ProviderRevision, _ SecretResolver) (Sender, error) {
	return &inAppSender{sink: d.sink}, nil
}

type inAppSender struct {
	sink InAppSink
}

func (s *inAppSender) Close() error { return nil }

func (s *inAppSender) Send(ctx context.Context, msg RenderedMessage) (SendResult, error) {
	started := time.Now()
	if strings.TrimSpace(msg.RecipientSubjectRef) == "" {
		return SendResult{
			Outcome:   OutcomePermanent,
			ErrorCode: "INAPP_SUBJECT_REQUIRED",
			Latency:   time.Since(started),
		}, errors.New("in-app notification requires an authenticated subject_ref")
	}
	title := strings.TrimSpace(msg.Subject)
	if title == "" {
		title = strings.TrimSpace(msg.TextBody)
	}
	if title == "" {
		return SendResult{
			Outcome:   OutcomePermanent,
			ErrorCode: "INAPP_TITLE_EMPTY",
			Latency:   time.Since(started),
		}, errors.New("in-app notification requires title or subject")
	}
	if s.sink == nil {
		return SendResult{
			Outcome:            OutcomeOK,
			ProviderMessageID:  "inapp-" + uuid.NewString(),
			ProviderStatusCode: "STORED",
			Latency:            time.Since(started),
		}, nil
	}
	msgID, err := s.sink.DeliverInApp(ctx, msg)
	if err != nil {
		return SendResult{
			Outcome:   OutcomeRetryable,
			ErrorCode: "INAPP_PERSIST_FAILED",
			Latency:   time.Since(started),
		}, err
	}
	return SendResult{
		Outcome:            OutcomeOK,
		ProviderMessageID:  msgID,
		ProviderStatusCode: "STORED",
		Latency:            time.Since(started),
	}, nil
}

const mockConfigSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "preset": {"type": "string", "enum": ["default", "zalo-zns-mock"]},
    "latency_ms": {"type": "integer", "minimum": 0, "maximum": 30000},
    "forced_outcome": {"type": "string", "enum": ["OK", "RETRYABLE", "THROTTLED", "PERMANENT", "AUTH_FAILED", "AMBIGUOUS"]},
    "retry_after_ms": {"type": "integer", "minimum": 0, "maximum": 300000},
    "require_provider_template": {"type": "boolean"}
  },
  "additionalProperties": false
}`

type MockConfig struct {
	Preset                  string `json:"preset"`
	LatencyMS               int    `json:"latency_ms"`
	ForcedOutcome           string `json:"forced_outcome"`
	RetryAfterMS            int    `json:"retry_after_ms"`
	RequireProviderTemplate bool   `json:"require_provider_template"`
}

type MockDriver struct{}

func NewMockDriver() *MockDriver { return &MockDriver{} }

func (d *MockDriver) Kind() string { return "mock" }

func (d *MockDriver) ConfigSchema() []byte { return []byte(mockConfigSchema) }

func (d *MockDriver) Open(_ context.Context, rev ProviderRevision, _ SecretResolver) (Sender, error) {
	var cfg MockConfig
	if len(rev.ConfigJSON) > 0 {
		dec := json.NewDecoder(bytes.NewReader(rev.ConfigJSON))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("invalid mock config: %w", err)
		}
	}
	if cfg.Preset == "" && rev.PresetKey != "" {
		cfg.Preset = rev.PresetKey
	}
	if cfg.Preset == "zalo-zns-mock" {
		cfg.RequireProviderTemplate = true
	}
	if cfg.ForcedOutcome == "" {
		cfg.ForcedOutcome = string(OutcomeOK)
	}
	switch OutcomeClass(cfg.ForcedOutcome) {
	case OutcomeOK, OutcomeRetryable, OutcomeThrottled, OutcomePermanent, OutcomeAuthFailed, OutcomeAmbiguous:
	default:
		return nil, errors.New("unsupported mock forced_outcome")
	}
	return &mockSender{cfg: cfg, preset: cfg.Preset}, nil
}

type mockSender struct {
	cfg    MockConfig
	preset string
}

func (s *mockSender) Close() error { return nil }

func (s *mockSender) Send(ctx context.Context, msg RenderedMessage) (SendResult, error) {
	started := time.Now()
	if s.cfg.LatencyMS > 0 {
		timer := time.NewTimer(time.Duration(s.cfg.LatencyMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return SendResult{Outcome: OutcomeAmbiguous, ErrorCode: "MOCK_CONTEXT_CANCELLED", Latency: time.Since(started)}, ctx.Err()
		case <-timer.C:
		}
	}
	if s.cfg.RequireProviderTemplate || strings.EqualFold(msg.Channel, "ZNS") {
		if strings.TrimSpace(msg.ProviderTemplateID) == "" {
			return SendResult{
				Outcome:            OutcomePermanent,
				ProviderStatusCode: "400",
				ErrorCode:          "ZNS_PROVIDER_TEMPLATE_ID_REQUIRED",
				Latency:            time.Since(started),
			}, errors.New("Zalo ZNS mock requires provider_template_id")
		}
	}
	outcome := OutcomeClass(s.cfg.ForcedOutcome)
	if outcome != OutcomeOK {
		return SendResult{
			Outcome:            outcome,
			ProviderStatusCode: "MOCK_" + string(outcome),
			ErrorCode:          "MOCK_" + string(outcome),
			RetryAfter:         time.Duration(s.cfg.RetryAfterMS) * time.Millisecond,
			Latency:            time.Since(started),
		}, fmt.Errorf("mock provider returned %s", outcome)
	}
	prefix := "mock-"
	if s.preset == "zalo-zns-mock" || strings.EqualFold(msg.Channel, "ZNS") {
		prefix = "zns-mock-"
	}
	return SendResult{
		Outcome:            OutcomeOK,
		ProviderMessageID:  prefix + uuid.NewString(),
		ProviderStatusCode: "200",
		Latency:            time.Since(started),
	}, nil
}
