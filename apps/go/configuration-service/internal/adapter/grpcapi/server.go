package grpcapi

import (
	"context"
	"errors"

	configurationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/configuration/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/configuration-service/internal/domain"
)

type Server struct {
	configurationv1.UnimplementedConfigurationResolverServiceServer
	service *application.Service
}

func New(service *application.Service) (*Server, error) {
	if service == nil {
		return nil, errors.New("configuration resolver service is required")
	}
	return &Server{service: service}, nil
}

func (s *Server) ResolveConfiguration(
	ctx context.Context,
	request *configurationv1.ResolveConfigurationRequest,
) (*configurationv1.ResolveConfigurationResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "resolution request is required")
	}
	resolved, err := s.service.Resolve(ctx, domain.ResolutionLookup{
		TenantID:             request.GetTenantId(),
		Owner:                ownerFromProto(request.GetOwner()),
		WorkflowType:         request.GetWorkflowType(),
		WorkflowVersion:      request.GetWorkflowVersion(),
		PlatformReference:    request.GetPlatformReference(),
		EnvironmentReference: request.GetEnvironmentReference(),
		InstanceID:           request.GetInstanceId(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	policy, _, _, err := domain.ParsePolicy(resolved.Profile.Owner, resolved.Version.ValuesJSON)
	if err != nil {
		return nil, status.Error(codes.DataLoss, "published configuration is invalid")
	}
	return &configurationv1.ResolveConfigurationResponse{
		Snapshot: &configurationv1.ResolvedConfigurationSnapshot{
			ConfigId:      resolved.Profile.ID,
			ConfigVersion: resolved.Version.ConfigVersion,
			PolicyVersion: resolved.Version.PolicyVersion,
			SchemaVersion: resolved.Version.SchemaVersion,
			ResolvedScopes: []*configurationv1.ConfigurationScope{{
				Type:      scopeType(resolved.Profile.Scope.Type),
				Reference: resolved.Profile.Scope.Reference,
			}},
			ContentHash:          resolved.Version.ContentHash[:],
			Owner:                ownerToProto(resolved.Profile.Owner),
			Engine:               policy.Engine,
			ApiGateway:           policy.APIGateway,
			HumanRuntime:         policy.HumanRuntime,
			Projection:           policy.Projection,
			Governance:           policy.Governance,
			Ordinal:              uint64(resolved.Version.Ordinal),
			ConfigurationService: policy.ConfigurationService,
			CockpitGateway:       policy.CockpitGateway,
			AuthzControlPlane:    policy.AuthzControlPlane,
		},
	}, nil
}

func ownerFromProto(value configurationv1.ConfigurationOwner) domain.Owner {
	switch value {
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE:
		return domain.OwnerEngine
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY:
		return domain.OwnerAPIGateway
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME:
		return domain.OwnerHumanRuntime
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_PROJECTION:
		return domain.OwnerProjection
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_GOVERNANCE:
		return domain.OwnerGovernance
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_CONFIGURATION_SERVICE:
		return domain.OwnerConfigurationService
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_COCKPIT_GATEWAY:
		return domain.OwnerCockpitGateway
	case configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_AUTHZ_CONTROL_PLANE:
		return domain.OwnerAuthzControlPlane
	default:
		return ""
	}
}

func ownerToProto(value domain.Owner) configurationv1.ConfigurationOwner {
	switch value {
	case domain.OwnerEngine:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_ENGINE
	case domain.OwnerAPIGateway:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_API_GATEWAY
	case domain.OwnerHumanRuntime:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_HUMAN_RUNTIME
	case domain.OwnerProjection:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_PROJECTION
	case domain.OwnerGovernance:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_GOVERNANCE
	case domain.OwnerConfigurationService:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_CONFIGURATION_SERVICE
	case domain.OwnerCockpitGateway:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_COCKPIT_GATEWAY
	case domain.OwnerAuthzControlPlane:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_AUTHZ_CONTROL_PLANE
	default:
		return configurationv1.ConfigurationOwner_CONFIGURATION_OWNER_UNSPECIFIED
	}
}

func scopeType(value domain.ScopeType) configurationv1.ConfigurationScopeType {
	switch value {
	case domain.ScopePlatform:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_PLATFORM
	case domain.ScopeEnvironment:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_ENVIRONMENT
	case domain.ScopeTenant:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_TENANT
	case domain.ScopeWorkflowType:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_TYPE
	case domain.ScopeWorkflowVersion:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_WORKFLOW_VERSION
	case domain.ScopeApprovedInstanceOverride:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_APPROVED_INSTANCE_OVERRIDE
	default:
		return configurationv1.ConfigurationScopeType_CONFIGURATION_SCOPE_TYPE_UNSPECIFIED
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		return status.Error(codes.InvalidArgument, "configuration lookup is invalid")
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "published configuration was not found")
	default:
		return status.Error(codes.Internal, "configuration resolution failed")
	}
}
