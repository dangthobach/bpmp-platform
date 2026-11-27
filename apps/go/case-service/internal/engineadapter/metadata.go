package engineadapter

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	authoringv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authoring/v1"
	authv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/authorization/v1"
	configv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	maxMetadataResponseBytes = 64 * 1024
	maxDeploymentScanPages   = 10
)

// OwnerAPIMetadataProvider resolves workflow start_node_id from the authoritative
// Authoring Service (ACTIVE runtime deployment + immutable BPMN version) and
// tenant encryption_key_scope from ConfigurationResolverService.
type OwnerAPIMetadataProvider struct {
	authoring     authoringv1.AuthoringServiceClient
	configuration configv1.ConfigurationResolverServiceClient
	platformRef   string
	envRef        string
	proofForJob   func(context.Context, application.StartJob) (*authv1.ActorProof, error)
}

func NewOwnerAPIMetadataProvider(
	authoring authoringv1.AuthoringServiceClient,
	configuration configv1.ConfigurationResolverServiceClient,
	platformRef, envRef string,
	proofForJob func(context.Context, application.StartJob) (*authv1.ActorProof, error),
) (*OwnerAPIMetadataProvider, error) {
	if authoring == nil || configuration == nil || strings.TrimSpace(platformRef) == "" || strings.TrimSpace(envRef) == "" || proofForJob == nil {
		return nil, application.ErrInvalid
	}
	return &OwnerAPIMetadataProvider{
		authoring:     authoring,
		configuration: configuration,
		platformRef:   strings.TrimSpace(platformRef),
		envRef:        strings.TrimSpace(envRef),
		proofForJob:   proofForJob,
	}, nil
}

func (p *OwnerAPIMetadataProvider) ResolveStartMetadata(ctx context.Context, job application.StartJob) (string, string, error) {
	if job.TenantID == uuid.Nil || job.WorkflowType == "" || job.WorkflowVersion == "" {
		return "", "", application.ErrInvalid
	}
	proof, err := p.proofForJob(ctx, job)
	if err != nil {
		return "", "", fmt.Errorf("issue authoring metadata proof: %w", err)
	}
	if proof == nil || len(proof.GetSignedProof()) == 0 {
		return "", "", application.ErrInvalid
	}

	tenantID := job.TenantID.String()
	var matched *authoringv1.RuntimeDeployment
	pageToken := ""
	for page := 0; page < maxDeploymentScanPages; page++ {
		resp, listErr := p.authoring.ListRuntimeDeployments(ctx, &authoringv1.ListRuntimeDeploymentsRequest{
			TenantId:          tenantID,
			TargetEnvironment: p.envRef,
			PageToken:         pageToken,
			PageSize:          100,
			ActorProof:        proof,
		})
		if listErr != nil {
			return "", "", fmt.Errorf("list authoring runtime deployments: %w", listErr)
		}
		for _, dep := range resp.GetDeployments() {
			if dep.GetTenantId() != tenantID ||
				dep.GetWorkflowType() != job.WorkflowType ||
				dep.GetWorkflowVersion() != job.WorkflowVersion ||
				dep.GetTargetEnvironment() != p.envRef ||
				dep.GetStatus() != "ACTIVE" {
				continue
			}
			if matched != nil && matched.GetProcessModelVersionId() != dep.GetProcessModelVersionId() {
				return "", "", errors.New("ambiguous ACTIVE runtime deployments for workflow version")
			}
			matched = dep
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}
	if matched == nil || matched.GetProcessModelId() == "" || matched.GetProcessModelVersionId() == "" {
		return "", "", errors.New("no ACTIVE runtime deployment found for workflow version")
	}

	versionResp, err := p.authoring.GetProcessModelVersion(ctx, &authoringv1.GetProcessModelVersionRequest{
		TenantId:       tenantID,
		ProcessModelId: matched.GetProcessModelId(),
		VersionId:      matched.GetProcessModelVersionId(),
		ActorProof:     proof,
	})
	if err != nil {
		return "", "", fmt.Errorf("get authoring process model version: %w", err)
	}
	version := versionResp.GetVersion()
	if version == nil || version.GetTenantId() != tenantID || version.GetProcessModelId() != matched.GetProcessModelId() {
		return "", "", errors.New("authoring process model version scope mismatch")
	}
	startNodeID, err := extractUniqueStartNodeID(version.GetBpmnXml())
	if err != nil {
		return "", "", err
	}

	cfgResp, err := p.configuration.ResolveConfiguration(ctx, &configv1.ResolveConfigurationRequest{
		TenantId:             tenantID,
		WorkflowType:         job.WorkflowType,
		WorkflowVersion:      job.WorkflowVersion,
		PlatformReference:    p.platformRef,
		EnvironmentReference: p.envRef,
		Owner:                configv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE,
	})
	if err != nil {
		return "", "", fmt.Errorf("resolve engine configuration: %w", err)
	}
	snapshot := cfgResp.GetSnapshot()
	if snapshot == nil {
		return "", "", errors.New("configuration snapshot is empty")
	}
	keyScope := strings.TrimSpace(snapshot.GetEngine().GetEventPayloadKeyScope())
	if keyScope == "" {
		return "", "", errors.New("resolved engine configuration lacks event_payload_key_scope")
	}
	return startNodeID, keyScope, nil
}

func extractUniqueStartNodeID(bpmnXML string) (string, error) {
	if strings.TrimSpace(bpmnXML) == "" {
		return "", errors.New("deployed BPMN XML is empty")
	}
	decoder := xml.NewDecoder(strings.NewReader(bpmnXML))
	var startNodeID string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse deployed BPMN XML: %w", err)
		}
		elem, ok := token.(xml.StartElement)
		if !ok || elem.Name.Local != "startEvent" {
			continue
		}
		var id string
		for _, attr := range elem.Attr {
			if attr.Name.Local == "id" {
				id = strings.TrimSpace(attr.Value)
				break
			}
		}
		if id == "" {
			return "", errors.New("deployed BPMN startEvent is missing id")
		}
		if startNodeID != "" {
			return "", errors.New("deployed BPMN has multiple startEvent nodes")
		}
		startNodeID = id
	}
	if startNodeID == "" {
		return "", errors.New("deployed BPMN has no startEvent node")
	}
	return startNodeID, nil
}

// HTTPMetadataProvider queries an authoritative HTTPS Workflow Metadata API
// using a short-lived EdDSA service JWT and verifies tenant/workflow bindings.
type HTTPMetadataProvider struct {
	client     HTTPDoer
	baseURL    *url.URL
	audience   string
	workloadID string
	keyID      string
	privateKey ed25519.PrivateKey
	now        func() time.Time
}

func NewHTTPMetadataProvider(
	client HTTPDoer,
	origin, audience, workloadID, keyID string,
	privateKey ed25519.PrivateKey,
	now func() time.Time,
) (*HTTPMetadataProvider, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		client == nil || audience == "" || workloadID == "" || keyID == "" ||
		len(privateKey) != ed25519.PrivateKeySize || now == nil {
		return nil, application.ErrInvalid
	}
	return &HTTPMetadataProvider{
		client:     client,
		baseURL:    parsed,
		audience:   audience,
		workloadID: workloadID,
		keyID:      keyID,
		privateKey: append(ed25519.PrivateKey(nil), privateKey...),
		now:        now,
	}, nil
}

func (p *HTTPMetadataProvider) ResolveStartMetadata(ctx context.Context, job application.StartJob) (string, string, error) {
	if job.TenantID == uuid.Nil || job.WorkflowType == "" || job.WorkflowVersion == "" {
		return "", "", application.ErrInvalid
	}
	now := p.now().UTC()
	claims := jwt.MapClaims{
		"sub":          p.workloadID,
		"tenant_id":    job.TenantID.String(),
		"capabilities": []string{"workflow.metadata.read"},
		"aud":          []string{p.audience},
		"iat":          now.Unix(),
		"exp":          now.Add(time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = p.keyID
	signed, err := token.SignedString(p.privateKey)
	if err != nil {
		return "", "", err
	}

	target := *p.baseURL
	target.Path = "/v1/workflow-metadata"
	query := url.Values{}
	query.Set("tenant_id", job.TenantID.String())
	query.Set("workflow_type", job.WorkflowType)
	query.Set("workflow_version", job.WorkflowVersion)
	target.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+signed)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-BPMP-Tenant-ID", job.TenantID.String())
	if job.CorrelationID != "" {
		req.Header.Set("X-Correlation-ID", job.CorrelationID)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("workflow metadata owner API returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataResponseBytes+1))
	if err != nil || len(raw) > maxMetadataResponseBytes {
		return "", "", errors.New("workflow metadata response exceeds bound")
	}
	var payload struct {
		TenantID           string `json:"tenant_id"`
		WorkflowType       string `json:"workflow_type"`
		WorkflowVersion    string `json:"workflow_version"`
		StartNodeID        string `json:"start_node_id"`
		EncryptionKeyScope string `json:"encryption_key_scope"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return "", "", fmt.Errorf("decode workflow metadata response: %w", err)
	}
	if payload.TenantID != job.TenantID.String() ||
		payload.WorkflowType != job.WorkflowType ||
		payload.WorkflowVersion != job.WorkflowVersion ||
		strings.TrimSpace(payload.StartNodeID) == "" ||
		strings.TrimSpace(payload.EncryptionKeyScope) == "" {
		return "", "", errors.New("workflow metadata response scope mismatch or incomplete")
	}
	return strings.TrimSpace(payload.StartNodeID), strings.TrimSpace(payload.EncryptionKeyScope), nil
}
