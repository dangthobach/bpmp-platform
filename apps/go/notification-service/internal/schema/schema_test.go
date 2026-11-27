package schema

import (
	"errors"
	"testing"
)

func TestCompileValidateSplitAndMask(t *testing.T) {
	raw := []byte(`{
		"type": "object",
		"required": ["otpCode", "customerEmail", "amount", "actionUrl"],
		"properties": {
			"otpCode": {"type": "string", "pattern": "^[0-9]{6}$", "secret": true, "example": "654321"},
			"customerEmail": {"type": "string", "pii": true, "example": "alice@example.com"},
			"amount": {"type": "money", "example": {"amount": "2500000", "currency": "VND"}},
			"actionUrl": {"type": "url", "example": "https://portal.bpmp.local/verify"},
			"items": {
				"type": "array",
				"maxItems": 5,
				"items": {
					"type": "object",
					"required": ["name"],
					"properties": {
						"name": {"type": "string", "example": "Loan Doc"}
					}
				}
			}
		}
	}`)

	cs, err := Compile(raw)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	if !cs.HasSecrets() {
		t.Fatal("expected HasSecrets() == true")
	}

	clean, secrets, err := cs.ValidateAndSplit([]byte(`{
		"otpCode": "123456",
		"customerEmail": "bob@example.com",
		"amount": {"amount": "1500000.50", "currency": "VND"},
		"actionUrl": "https://portal.bpmp.local/cases/1"
	}`))
	if err != nil {
		t.Fatalf("validate and split: %v", err)
	}
	if _, leaked := clean["otpCode"]; leaked {
		t.Fatal("secret field otpCode must not remain in cleanData")
	}
	if secrets["otpCode"] != "123456" {
		t.Fatalf("expected otpCode in secrets, got %#v", secrets)
	}

	masked := cs.MaskData(clean, true)
	if masked["otpCode"] != "******" {
		t.Fatalf("expected erased secret to render as ******, got %#v", masked["otpCode"])
	}
	if masked["customerEmail"] != "b***@example.com" {
		t.Fatalf("expected masked PII email, got %#v", masked["customerEmail"])
	}

	// Reject non-HTTPS url!
	if _, err := cs.ValidatePayload([]byte(`{
		"otpCode": "123456",
		"customerEmail": "bob@example.com",
		"amount": {"amount": "100", "currency": "VND"},
		"actionUrl": "http://insecure.example.com"
	}`)); err == nil {
		t.Fatal("http:// URL must be rejected")
	}

	// Reject array without maxItems in schema!
	if _, err := Compile([]byte(`{
		"type": "object",
		"properties": {"items": {"type": "array", "items": {"type": "string"}}}
	}`)); err == nil {
		t.Fatal("array without maxItems must be rejected")
	}
}

func TestBackwardCompatibilityGate(t *testing.T) {
	v1 := []byte(`{
		"type": "object",
		"required": ["caseCode"],
		"properties": {
			"caseCode": {"type": "string", "maxLength": 32, "example": "CS-01"},
			"status": {"type": "string", "enum": ["SUBMITTED", "APPROVED"], "example": "SUBMITTED"}
		}
	}`)

	// Non-breaking: add optional field, widen maxLength, add enum value.
	v2Valid := []byte(`{
		"type": "object",
		"required": ["caseCode"],
		"properties": {
			"caseCode": {"type": "string", "maxLength": 64, "example": "CS-01"},
			"status": {"type": "string", "enum": ["SUBMITTED", "APPROVED", "REJECTED"], "example": "SUBMITTED"},
			"note": {"type": "string", "example": "Optional note"}
		}
	}`)
	if err := CheckBackwardCompatible(v1, v2Valid); err != nil {
		t.Fatalf("expected compatible evolution, got: %v", err)
	}

	breakingCases := map[string][]byte{
		"removed field": []byte(`{
			"type": "object",
			"required": ["caseCode"],
			"properties": {"caseCode": {"type": "string", "maxLength": 32}}
		}`),
		"changed type": []byte(`{
			"type": "object",
			"required": ["caseCode"],
			"properties": {
				"caseCode": {"type": "integer"},
				"status": {"type": "string", "enum": ["SUBMITTED", "APPROVED"]}
			}
		}`),
		"added required field": []byte(`{
			"type": "object",
			"required": ["caseCode", "branchId"],
			"properties": {
				"caseCode": {"type": "string", "maxLength": 32},
				"status": {"type": "string", "enum": ["SUBMITTED", "APPROVED"]},
				"branchId": {"type": "string"}
			}
		}`),
		"shrunk maxLength": []byte(`{
			"type": "object",
			"required": ["caseCode"],
			"properties": {
				"caseCode": {"type": "string", "maxLength": 16},
				"status": {"type": "string", "enum": ["SUBMITTED", "APPROVED"]}
			}
		}`),
	}

	for name, candidate := range breakingCases {
		t.Run(name, func(t *testing.T) {
			err := CheckBackwardCompatible(v1, candidate)
			if !errors.Is(err, ErrIncompatibleSchema) {
				t.Fatalf("expected ErrIncompatibleSchema for %s, got %v", name, err)
			}
		})
	}
}
