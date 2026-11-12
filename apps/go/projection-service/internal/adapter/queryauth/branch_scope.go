package queryauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	authorizationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"google.golang.org/protobuf/proto"
)

type BranchDescendants struct {
	BranchID            string   `json:"branch_id"`
	OrganizationID      string   `json:"organization_id"`
	OrganizationVersion int64    `json:"organization_version"`
	DescendantBranchIDs []string `json:"descendant_branch_ids"`
}

type BranchScopeResolver interface {
	ResolveDescendants(context.Context, string, []string) (map[string][]string, error)
}

type HTTPBranchScopeResolver struct {
	endpoint     string
	client       *http.Client
	workloadID   string
	signingKeyID string
	signingKey   ed25519.PrivateKey
	proofTTL     time.Duration
}

func NewHTTPBranchScopeResolver(baseURL, caPath string, identity tls.Certificate, timeout time.Duration, workloadID, signingKeyID, signingKeyPath string, proofTTL time.Duration) (*HTTPBranchScopeResolver, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || caPath == "" || timeout <= 0 || strings.TrimSpace(workloadID) == "" || strings.TrimSpace(signingKeyID) == "" || proofTTL <= 0 || proofTTL > 5*time.Minute {
		return nil, errors.New("branch scope resolver requires HTTPS, mTLS, workload signing identity, and bounded positive timeouts")
	}
	keyBytes, err := os.ReadFile(signingKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read branch scope workload signing key: %w", err)
	}
	var signingKey ed25519.PrivateKey
	switch len(keyBytes) {
	case ed25519.SeedSize:
		signingKey = ed25519.NewKeyFromSeed(keyBytes)
	case ed25519.PrivateKeySize:
		signingKey = ed25519.PrivateKey(keyBytes)
	default:
		return nil, errors.New("branch scope workload signing key must be a 32-byte seed or 64-byte Ed25519 private key")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read branch scope resolver CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("branch scope resolver CA file contains no certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{identity},
	}
	return &HTTPBranchScopeResolver{
		endpoint:     strings.TrimRight(baseURL, "/") + "/internal/v1/branch-scopes/descendants",
		client:       &http.Client{Transport: transport, Timeout: timeout},
		workloadID:   workloadID,
		signingKeyID: signingKeyID,
		signingKey:   signingKey,
		proofTTL:     proofTTL,
	}, nil
}

func (r *HTTPBranchScopeResolver) ResolveDescendants(ctx context.Context, tenantID string, branchIDs []string) (map[string][]string, error) {
	if len(branchIDs) == 0 || len(branchIDs) > 100 {
		return nil, errors.New("branch scope request is outside limits")
	}
	commandBytes := make([]byte, 16)
	if _, err := rand.Read(commandBytes); err != nil {
		return nil, fmt.Errorf("create branch scope command id: %w", err)
	}
	commandID := hex.EncodeToString(commandBytes)
	now := time.Now()
	proof := &authorizationv1.SignedWorkloadContext{
		SchemaVersion:    1,
		TenantId:         tenantID,
		WorkloadId:       r.workloadID,
		CommandId:        commandID,
		IssuedAtEpochMs:  uint64(now.UnixMilli()),
		ExpiresAtEpochMs: uint64(now.Add(r.proofTTL).UnixMilli()),
		SigningKeyId:     r.signingKeyID,
	}
	unsigned, err := proto.Marshal(proof)
	if err != nil {
		return nil, fmt.Errorf("encode branch scope workload proof: %w", err)
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(r.signingKey, digest[:])
	proofBytes, err := proto.Marshal(proof)
	if err != nil {
		return nil, fmt.Errorf("encode signed branch scope workload proof: %w", err)
	}
	body, err := json.Marshal(struct {
		BranchIDs []string `json:"branch_ids"`
	}{BranchIDs: branchIDs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-bpmp-workload-proof", hex.EncodeToString(proofBytes))
	req.Header.Set("x-bpmp-command-id", commandID)
	response, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("branch scope resolver returned status %d", response.StatusCode)
	}
	var envelope struct {
		Data []BranchDescendants `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err = decoder.Decode(&envelope); err != nil {
		return nil, errors.New("branch scope resolver returned an invalid response")
	}
	result := make(map[string][]string, len(envelope.Data))
	total := 0
	for _, item := range envelope.Data {
		if item.BranchID == "" || item.OrganizationID == "" || item.OrganizationVersion <= 0 || len(item.DescendantBranchIDs) == 0 {
			return nil, errors.New("branch scope resolver returned incomplete scope")
		}
		if _, duplicate := result[item.BranchID]; duplicate {
			return nil, errors.New("branch scope resolver returned duplicate roots")
		}
		if len(item.DescendantBranchIDs) > 500 {
			return nil, errors.New("branch descendant result exceeds query filter limit")
		}
		total += len(item.DescendantBranchIDs)
		if total > 5000 {
			return nil, errors.New("branch descendant batch exceeds query filter limit")
		}
		result[item.BranchID] = append([]string(nil), item.DescendantBranchIDs...)
	}
	if len(result) != len(branchIDs) {
		return nil, errors.New("branch scope resolver omitted requested roots")
	}
	for _, id := range branchIDs {
		if _, exists := result[id]; !exists {
			return nil, errors.New("branch scope resolver returned a different root set")
		}
	}
	return result, nil
}
