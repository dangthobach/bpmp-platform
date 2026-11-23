package engineadapter

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// WorkflowMetadata must resolve the deployed definition's start node and the
// current tenant Engine key scope. It must not read another service's database.
type WorkflowMetadata interface {
	ResolveStartMetadata(context.Context, application.StartJob) (startNodeID, encryptionKeyScope string, err error)
}

type AuthzCredentialsProvider struct {
	client        HTTPDoer
	authzURL      string
	authzAudience string
	workloadID    string
	keyID         string
	privateKey    ed25519.PrivateKey
	metadata      WorkflowMetadata
	now           func() time.Time
}

func NewAuthzCredentialsProvider(client HTTPDoer, authzOrigin, authzAudience, workloadID, keyID string,
	privateKey ed25519.PrivateKey, metadata WorkflowMetadata, now func() time.Time) (*AuthzCredentialsProvider, error) {
	parsed, err := url.Parse(authzOrigin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" ||
		client == nil || authzAudience == "" || workloadID == "" || keyID == "" ||
		len(privateKey) != ed25519.PrivateKeySize || metadata == nil || now == nil {
		return nil, application.ErrInvalid
	}
	return &AuthzCredentialsProvider{client: client, authzURL: authzOrigin + "/authz/v1/actor-context",
		authzAudience: authzAudience, workloadID: workloadID, keyID: keyID,
		privateKey: append(ed25519.PrivateKey(nil), privateKey...), metadata: metadata, now: now}, nil
}

func (p *AuthzCredentialsProvider) ResolveStart(ctx context.Context, job application.StartJob) (StartCredentials, error) {
	if job.TenantID == uuid.Nil || job.CommandID == uuid.Nil || job.CaseID != job.CommandID {
		return StartCredentials{}, application.ErrInvalid
	}
	if _, err := uuid.Parse(job.ActorID); err != nil {
		return StartCredentials{}, application.ErrInvalid
	}
	startNode, keyScope, err := p.metadata.ResolveStartMetadata(ctx, job)
	if err != nil {
		return StartCredentials{}, err
	}
	if startNode == "" || keyScope == "" {
		return StartCredentials{}, application.ErrInvalid
	}
	now := p.now().UTC()
	claims := jwt.MapClaims{
		"sub": p.workloadID, "actor_id": job.ActorID, "tenant_id": job.TenantID.String(),
		"capabilities": []string{"identity.actor-context.issue"}, "aud": []string{p.authzAudience},
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	}
	delegation := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	delegation.Header["kid"] = p.keyID
	token, err := delegation.SignedString(p.privateKey)
	if err != nil {
		return StartCredentials{}, err
	}
	body, err := json.Marshal(struct {
		TenantID           string `json:"tenant_id"`
		AudienceWorkloadID string `json:"audience_workload_id"`
		CommandID          string `json:"command_id"`
	}{job.TenantID.String(), p.workloadID, job.CommandID.String()})
	if err != nil {
		return StartCredentials{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.authzURL, bytes.NewReader(body))
	if err != nil {
		return StartCredentials{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return StartCredentials{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return StartCredentials{}, fmt.Errorf("Authz actor-context issuer returned HTTP %d", response.StatusCode)
	}
	var issued struct {
		TenantID           string `json:"tenant_id"`
		ActorID            string `json:"actor_id"`
		AudienceWorkloadID string `json:"audience_workload_id"`
		CommandID          string `json:"command_id"`
		SignedActorContext string `json:"signed_actor_context"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return StartCredentials{}, errors.New("Authz actor context response exceeds bound")
	}
	if err = json.Unmarshal(raw, &issued); err != nil {
		return StartCredentials{}, fmt.Errorf("decode Authz actor context: %w", err)
	}
	if issued.TenantID != job.TenantID.String() || issued.ActorID != job.ActorID ||
		issued.AudienceWorkloadID != p.workloadID || issued.CommandID != job.CommandID.String() ||
		issued.SignedActorContext == "" {
		return StartCredentials{}, errors.New("Authz actor context scope mismatch")
	}
	actorProof, err := base64.StdEncoding.Strict().DecodeString(issued.SignedActorContext)
	if err != nil {
		return StartCredentials{}, err
	}
	proof := &authv1.SignedWorkloadContext{SchemaVersion: 1, TenantId: job.TenantID.String(),
		WorkloadId: p.workloadID, CommandId: job.CommandID.String(),
		IssuedAtEpochMs: uint64(now.UnixMilli()), ExpiresAtEpochMs: uint64(now.Add(15 * time.Second).UnixMilli()),
		SigningKeyId: p.keyID}
	unsigned, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		return StartCredentials{}, err
	}
	digest := sha256.Sum256(unsigned)
	proof.ContentHash = digest[:]
	proof.Signature = ed25519.Sign(p.privateKey, digest[:])
	workloadProof, err := proto.MarshalOptions{Deterministic: true}.Marshal(proof)
	if err != nil {
		return StartCredentials{}, err
	}
	result := StartCredentials{SignedActorContext: actorProof, SignedWorkloadProof: workloadProof,
		StartNodeID: startNode, EncryptionKeyScope: keyScope}
	if err = validateCredentials(result, job, p.workloadID, p.now().UTC()); err != nil {
		return StartCredentials{}, err
	}
	return result, nil
}
