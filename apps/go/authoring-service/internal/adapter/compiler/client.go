package compiler

import (
	"context"
	"errors"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/authoring-service/internal/application"
	compilerv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/compiler/v1"
	"google.golang.org/grpc"
)

type RPC interface {
	ValidateBpmn(context.Context, *compilerv1.ValidateBpmnRequest, ...grpc.CallOption) (*compilerv1.ValidateBpmnResponse, error)
	ValidateDmn(context.Context, *compilerv1.ValidateDmnRequest, ...grpc.CallOption) (*compilerv1.ValidateDmnResponse, error)
}

type Client struct{ rpc RPC }

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
