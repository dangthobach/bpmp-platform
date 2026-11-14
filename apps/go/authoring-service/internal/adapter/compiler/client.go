package compiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	compilerv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/compiler/v1"
	"google.golang.org/grpc"
)

type RPC interface {
	CompileBpmn(context.Context, *compilerv1.CompileBpmnRequest, ...grpc.CallOption) (*compilerv1.CompileBpmnResponse, error)
	GetWIRArtifact(context.Context, *compilerv1.GetWIRArtifactRequest, ...grpc.CallOption) (*compilerv1.GetWIRArtifactResponse, error)
	ValidateBpmn(context.Context, *compilerv1.ValidateBpmnRequest, ...grpc.CallOption) (*compilerv1.ValidateBpmnResponse, error)
	ValidateDmn(context.Context, *compilerv1.ValidateDmnRequest, ...grpc.CallOption) (*compilerv1.ValidateDmnResponse, error)
}

type Client struct{ rpc RPC }

func (c *Client) CompileRuntimeArtifact(ctx context.Context, request application.RuntimeArtifactRequest) (application.RuntimeArtifact, error) {
	if request.TenantID == "" || request.ProcessModelID == "" || request.SourceRevision < 1 || request.WorkflowVersion == "" || request.BPMNSourceName == "" || len(request.BPMNXML) == 0 || len(request.BPMNXML) > application.MaxBPMNBytes || len(request.DecisionModels)+len(request.CaseModels) > 32 {
		return application.RuntimeArtifact{}, application.ErrInvalidArgument
	}
	total := len(request.BPMNXML)
	toSources := func(values []application.CompileSource) ([]*compilerv1.CompileSource, error) {
		result := make([]*compilerv1.CompileSource, 0, len(values))
		for _, source := range values {
			if strings.TrimSpace(source.Name) == "" || len(source.Name) > 256 || len(source.Content) == 0 || len(source.Content) > application.MaxBPMNBytes {
				return nil, application.ErrInvalidArgument
			}
			total += len(source.Content)
			if total > 4<<20 {
				return nil, application.ErrInvalidArgument
			}
			result = append(result, &compilerv1.CompileSource{SourceName: source.Name, Content: []byte(source.Content)})
		}
		return result, nil
	}
	decisions, err := toSources(request.DecisionModels)
	if err != nil {
		return application.RuntimeArtifact{}, err
	}
	cases, err := toSources(request.CaseModels)
	if err != nil {
		return application.RuntimeArtifact{}, err
	}
	response, err := c.rpc.CompileBpmn(ctx, &compilerv1.CompileBpmnRequest{
		TenantId: request.TenantID, ProcessModelId: request.ProcessModelID, SourceRevision: request.SourceRevision,
		ExpectedWorkflowType: request.WorkflowType, WorkflowVersion: request.WorkflowVersion,
		BpmnSourceName: request.BPMNSourceName, BpmnXml: []byte(request.BPMNXML), DecisionModels: decisions, CaseModels: cases,
	})
	if err != nil {
		return application.RuntimeArtifact{}, err
	}
	if response == nil {
		return application.RuntimeArtifact{}, errors.New("compiler returned an empty compile response")
	}
	diagnostics := convertDiagnostics(response.GetDiagnostics())
	if !response.GetValid() {
		if len(diagnostics) == 0 || len(response.GetSignedWirArtifact()) != 0 || len(response.GetArtifactSha256()) != 0 || response.GetArtifactId() != "" {
			return application.RuntimeArtifact{}, errors.New("compiler response violates invalid-artifact invariant")
		}
		return application.RuntimeArtifact{Diagnostics: diagnostics}, nil
	}
	if len(diagnostics) != 0 || len(response.GetSignedWirArtifact()) == 0 || len(response.GetArtifactSha256()) != sha256.Size || strings.TrimSpace(response.GetWorkflowType()) == "" {
		return application.RuntimeArtifact{}, errors.New("compiler response violates signed-artifact invariant")
	}
	digest := sha256.Sum256(response.GetSignedWirArtifact())
	if !bytes.Equal(digest[:], response.GetArtifactSha256()) {
		return application.RuntimeArtifact{}, errors.New("compiler artifact SHA-256 does not match response")
	}
	if response.GetArtifactId() != hex.EncodeToString(response.GetArtifactSha256()) {
		return application.RuntimeArtifact{}, errors.New("compiler artifact id does not match SHA-256")
	}
	return application.RuntimeArtifact{Valid: true, ArtifactID: response.GetArtifactId(), WorkflowType: response.GetWorkflowType(), SignedWIR: append([]byte(nil), response.GetSignedWirArtifact()...), SHA256: append([]byte(nil), response.GetArtifactSha256()...)}, nil
}

func (c *Client) GetRuntimeArtifact(ctx context.Context, artifactID string) (application.RuntimeArtifact, error) {
	digest, err := hex.DecodeString(artifactID)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != artifactID {
		return application.RuntimeArtifact{}, application.ErrInvalidArgument
	}
	response, err := c.rpc.GetWIRArtifact(ctx, &compilerv1.GetWIRArtifactRequest{ArtifactId: artifactID})
	if err != nil {
		return application.RuntimeArtifact{}, err
	}
	if response == nil || len(response.GetSignedWirArtifact()) == 0 || !bytes.Equal(response.GetArtifactSha256(), digest) {
		return application.RuntimeArtifact{}, errors.New("compiler artifact response violates content-address invariant")
	}
	computed := sha256.Sum256(response.GetSignedWirArtifact())
	if !bytes.Equal(computed[:], digest) {
		return application.RuntimeArtifact{}, errors.New("retrieved compiler artifact does not match requested id")
	}
	return application.RuntimeArtifact{Valid: true, ArtifactID: artifactID, SignedWIR: append([]byte(nil), response.GetSignedWirArtifact()...), SHA256: append([]byte(nil), digest...)}, nil
}

func New(rpc RPC) (*Client, error) {
	if rpc == nil {
		return nil, errors.New("compiler RPC client is required")
	}
	return &Client{rpc: rpc}, nil
}

func (c *Client) ValidateBPMN(ctx context.Context, tenantID, modelID string, revision int64, source string) ([]application.Diagnostic, error) {
	if tenantID == "" || modelID == "" || revision < 1 || len(source) == 0 || len(source) > application.MaxBPMNBytes {
		return nil, application.ErrInvalidArgument
	}
	response, err := c.rpc.ValidateBpmn(ctx, &compilerv1.ValidateBpmnRequest{
		TenantId: tenantID, ProcessModelId: modelID, DraftRevision: revision,
		SourceName: modelID + ".bpmn", BpmnXml: []byte(source),
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("compiler returned an empty response")
	}
	diagnostics := convertDiagnostics(response.GetDiagnostics())
	if response.GetValid() && len(diagnostics) != 0 {
		return nil, errors.New("compiler response violates valid/diagnostic invariant")
	}
	if !response.GetValid() && len(diagnostics) == 0 {
		return nil, errors.New("compiler rejected BPMN without diagnostics")
	}
	return diagnostics, nil
}

func (c *Client) ValidateDMN(ctx context.Context, tenantID, modelID string, revision int64, source string) ([]application.Diagnostic, error) {
	if tenantID == "" || modelID == "" || revision < 1 || len(source) == 0 || len(source) > application.MaxBPMNBytes {
		return nil, application.ErrInvalidArgument
	}
	response, err := c.rpc.ValidateDmn(ctx, &compilerv1.ValidateDmnRequest{
		TenantId: tenantID, DecisionModelId: modelID, DraftRevision: revision,
		SourceName: modelID + ".dmn", DmnXml: []byte(source),
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("compiler returned an empty response")
	}
	diagnostics := convertDiagnostics(response.GetDiagnostics())
	if response.GetValid() && len(diagnostics) != 0 {
		return nil, errors.New("compiler response violates valid/diagnostic invariant")
	}
	if !response.GetValid() && len(diagnostics) == 0 {
		return nil, errors.New("compiler rejected DMN without diagnostics")
	}
	return diagnostics, nil
}

func convertDiagnostics(values []*compilerv1.Diagnostic) []application.Diagnostic {
	diagnostics := make([]application.Diagnostic, 0, len(values))
	for _, diagnostic := range values {
		if diagnostic == nil {
			continue
		}
		severity := strings.ToUpper(diagnostic.GetSeverity())
		if severity != "ERROR" && severity != "WARNING" && severity != "INFO" {
			severity = "ERROR"
		}
		diagnostics = append(diagnostics, application.Diagnostic{
			Code: diagnostic.GetCode(), Severity: severity, Message: diagnostic.GetMessage(),
			ElementID: diagnostic.GetElementId(), Line: diagnostic.GetLine(), Column: diagnostic.GetColumn(),
		})
	}
	return diagnostics
}
