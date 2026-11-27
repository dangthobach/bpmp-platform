package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

var (
	ErrInvalidKey        = errors.New("encryption key must be 32 bytes for AES-256-GCM")
	ErrUnknownKeyRef     = errors.New("encryption key reference is unknown")
	ErrInvalidScope      = errors.New("tenant_id and delivery_id are required for AAD")
	ErrCiphertextInvalid = errors.New("ciphertext is malformed or failed GCM authentication")
)

type KeyRing struct {
	activeKeyRef string
	keys         map[string][]byte
	pepper       []byte
}

func NewKeyRing(activeKeyRef string, keys map[string][]byte, addressPepper []byte) (*KeyRing, error) {
	if strings.TrimSpace(activeKeyRef) == "" || len(keys) == 0 {
		return nil, ErrUnknownKeyRef
	}
	copied := make(map[string][]byte, len(keys))
	for ref, raw := range keys {
		if strings.TrimSpace(ref) == "" {
			return nil, ErrUnknownKeyRef
		}
		if len(raw) != 32 {
			return nil, ErrInvalidKey
		}
		copied[ref] = append([]byte(nil), raw...)
	}
	if _, ok := copied[activeKeyRef]; !ok {
		return nil, ErrUnknownKeyRef
	}
	pepper := append([]byte(nil), addressPepper...)
	if len(pepper) == 0 {
		pepper = copied[activeKeyRef]
	}
	return &KeyRing{
		activeKeyRef: activeKeyRef,
		keys:         copied,
		pepper:       pepper,
	}, nil
}

func (k *KeyRing) ActiveKeyRef() string {
	return k.activeKeyRef
}

func BuildAAD(tenantID, deliveryID string) ([]byte, error) {
	t := strings.TrimSpace(tenantID)
	d := strings.TrimSpace(deliveryID)
	if t == "" || d == "" {
		return nil, ErrInvalidScope
	}
	return []byte(t + ":" + d), nil
}

func (k *KeyRing) Encrypt(tenantID, deliveryID string, plaintext []byte) (ciphertext []byte, keyRef string, err error) {
	aad, err := BuildAAD(tenantID, deliveryID)
	if err != nil {
		return nil, "", err
	}
	if len(plaintext) == 0 {
		return nil, k.activeKeyRef, nil
	}
	key := k.keys[k.activeKeyRef]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, "", fmt.Errorf("generate GCM nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, aad)
	return sealed, k.activeKeyRef, nil
}

func (k *KeyRing) Decrypt(tenantID, deliveryID, keyRef string, ciphertext []byte) ([]byte, error) {
	aad, err := BuildAAD(tenantID, deliveryID)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 {
		return nil, nil
	}
	key, ok := k.keys[keyRef]
	if !ok {
		return nil, ErrUnknownKeyRef
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrCiphertextInvalid
	}
	nonce := ciphertext[:gcm.NonceSize()]
	body := ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, body, aad)
	if err != nil {
		return nil, ErrCiphertextInvalid
	}
	return plaintext, nil
}

func (k *KeyRing) HashAddress(tenantID, channel, address string) string {
	normalized := NormalizeAddress(channel, address)
	mac := hmac.New(sha256.New, k.pepper)
	_, _ = mac.Write([]byte(strings.TrimSpace(tenantID)))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write([]byte(strings.ToUpper(strings.TrimSpace(channel))))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

func NormalizeAddress(channel, raw string) string {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToUpper(strings.TrimSpace(channel)) {
	case "EMAIL":
		return strings.ToLower(trimmed)
	case "SMS", "ZNS":
		var b strings.Builder
		for i, r := range trimmed {
			if r == '+' && i == 0 {
				b.WriteRune(r)
				continue
			}
			if unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
		cleaned := b.String()
		if strings.HasPrefix(cleaned, "+84") && len(cleaned) > 3 {
			return "0" + cleaned[3:]
		}
		return cleaned
	default:
		return trimmed
	}
}

func MaskAddress(channel, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	upperChan := strings.ToUpper(strings.TrimSpace(channel))
	if upperChan == "EMAIL" || strings.Contains(trimmed, "@") {
		at := strings.IndexByte(trimmed, '@')
		if at > 0 && at < len(trimmed)-1 {
			localRunes := []rune(trimmed[:at])
			domain := strings.ToLower(trimmed[at+1:])
			return string(localRunes[0]) + "***@" + domain
		}
	}
	if upperChan == "SMS" || upperChan == "ZNS" {
		norm := NormalizeAddress(upperChan, trimmed)
		runes := []rune(norm)
		if len(runes) >= 7 {
			return string(runes[:3]) + "****" + string(runes[len(runes)-3:])
		}
	}
	runes := []rune(trimmed)
	if len(runes) <= 2 {
		return string(runes[0]) + "***"
	}
	if len(runes) >= 7 {
		return string(runes[:3]) + "****" + string(runes[len(runes)-3:])
	}
	return string(runes[0]) + "***"
}
