package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type manifest struct {
	OwnerID         string            `json:"owner_id"`
	ManifestVersion string            `json:"manifest_version"`
	Entries         []json.RawMessage `json:"entries"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("authz-owner-register", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	authzURL := flags.String("authz-url", "", "Authz server HTTPS origin")
	audience := flags.String("audience", "", "JWT audience configured by Authz")
	manifestPath := flags.String("manifest", "", "owner manifest JSON file")
	keyPath := flags.String("private-key", "", "raw Ed25519 seed or private key trusted by Authz JWKS")
	keyID := flags.String("key-id", "", "kid associated with the owner key in Authz JWKS")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *authzURL == "" || *audience == "" || *manifestPath == "" || *keyPath == "" || *keyID == "" {
		return errors.New("usage: authz-owner-register --authz-url https://authz.example --audience authz-admin --manifest owner-manifest.json --private-key owner.key --key-id owner-k1")
	}
	endpoint, err := ownerRegistrationURL(*authzURL)
	if err != nil {
		return err
	}
	var ownerManifest manifest
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read owner manifest: %w", err)
	}
	if err := json.Unmarshal(manifestBytes, &ownerManifest); err != nil {
		return fmt.Errorf("decode owner manifest: %w", err)
	}
	if strings.TrimSpace(ownerManifest.OwnerID) == "" || strings.TrimSpace(ownerManifest.ManifestVersion) == "" || len(ownerManifest.Entries) == 0 {
		return errors.New("owner manifest requires owner_id, manifest_version, and entries")
	}
	for _, entry := range ownerManifest.Entries {
		if !json.Valid(entry) {
			return errors.New("owner manifest contains an invalid entry")
		}
	}
	keyBytes, err := os.ReadFile(*keyPath)
	if err != nil {
		return fmt.Errorf("read owner signing key: %w", err)
	}
	privateKey, err := parsePrivateKey(keyBytes)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":          ownerManifest.OwnerID,
		"capabilities": []string{"authz.catalog.owner-register"},
		"aud":          []string{*audience},
		"iat":          now.Unix(),
		"exp":          now.Add(time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = *keyID
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		return fmt.Errorf("sign owner registration token: %w", err)
	}
	body, err := json.Marshal(ownerManifest)
	if err != nil {
		return fmt.Errorf("encode owner registration: %w", err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create owner registration request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+signedToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("register owner manifest: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read owner registration response: %w", err)
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return fmt.Errorf("Authz rejected owner registration (%s): %s", response.Status, strings.TrimSpace(string(responseBody)))
	}
	fmt.Printf("Owner manifest registered: %s v%s\n", ownerManifest.OwnerID, ownerManifest.ManifestVersion)
	return nil
}

func ownerRegistrationURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return "", errors.New("Authz URL must be an HTTPS origin without credentials, path, query, or fragment")
	}
	return parsed.ResolveReference(&url.URL{Path: "/admin/v1/catalog/owner-manifests"}).String(), nil
}

func parsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	switch len(data) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(data), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(data), nil
	default:
		return nil, errors.New("owner key must be a raw 32-byte seed or 64-byte Ed25519 private key")
	}
}
