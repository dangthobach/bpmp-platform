package crypto

import (
	"bytes"
	"testing"
)

func TestAESGCMBindsTenantAndDeliveryAAD(t *testing.T) {
	key1 := bytes.Repeat([]byte{0x11}, 32)
	key2 := bytes.Repeat([]byte{0x22}, 32)
	oldRing, err := NewKeyRing("k1", map[string][]byte{"k1": key1}, key1)
	if err != nil {
		t.Fatal(err)
	}
	ct, ref, err := oldRing.Encrypt("tenant-a", "delivery-1", []byte(`{"otp":"123456"}`))
	if err != nil || ref != "k1" {
		t.Fatalf("encrypt failed: err=%v ref=%s", err, ref)
	}

	// Rotated keyring still decrypts k1 ciphertexts.
	rotated, err := NewKeyRing("k2", map[string][]byte{"k1": key1, "k2": key2}, key2)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := rotated.Decrypt("tenant-a", "delivery-1", ref, ct)
	if err != nil || string(pt) != `{"otp":"123456"}` {
		t.Fatalf("decrypt failed: err=%v pt=%q", err, pt)
	}

	// Cross-tenant or cross-delivery ciphertext swap must fail GCM authentication!
	if _, err := rotated.Decrypt("tenant-b", "delivery-1", ref, ct); err == nil {
		t.Fatal("expected cross-tenant AAD mismatch to fail")
	}
	if _, err := rotated.Decrypt("tenant-a", "delivery-2", ref, ct); err == nil {
		t.Fatal("expected cross-delivery AAD mismatch to fail")
	}
}

func TestMaskAndHashAddress(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, 32)
	ring, err := NewKeyRing("k1", map[string][]byte{"k1": key}, key)
	if err != nil {
		t.Fatal(err)
	}
	if got := MaskAddress("EMAIL", "nguyen.van.a@Example.COM"); got != "n***@example.com" {
		t.Fatalf("unexpected email mask: %q", got)
	}
	if got := MaskAddress("SMS", "+84 901 234 123"); got != "090****123" {
		t.Fatalf("unexpected phone mask: %q", got)
	}
	h1 := ring.HashAddress("tenant-a", "SMS", "+84901234123")
	h2 := ring.HashAddress("tenant-a", "SMS", "090-1234-123")
	hOtherTenant := ring.HashAddress("tenant-b", "SMS", "0901234123")
	if h1 != h2 {
		t.Fatalf("expected normalized phone hashes to match: %s vs %s", h1, h2)
	}
	if h1 == hOtherTenant {
		t.Fatal("address hash must be scoped by tenant")
	}
}
