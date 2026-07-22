//! Pure deterministic workflow domain.
//!
//! This crate intentionally has no I/O, async runtime, clock, randomness, or
//! environment access. Adapters must validate and inject every external value.

mod configuration;
mod identifiers;
mod workflow;

pub use configuration::{
    BoundaryRuntimePolicy, ConfigError, ConfigurationScope, EnginePolicy, LocalWasmPolicy,
    ResolvedConfigSnapshot, RetryPolicy, ScopeKind,
};
pub use identifiers::{
    ActorId, CaseId, CaseModelId, CommandId, ConfigId, ConfigVersion, CorrelationId,
    IdempotencyKey, IdentifierError, InstanceId, KeyScope, NodeId, PlanItemId, PolicyVersion,
    ScopeInstanceId, SentryId, TaskType, TenantId, WorkflowType, WorkflowVersion,
};
pub use workflow::{
    ActiveBoundarySubscription, ActiveCase, ActiveExecutionScope, ActiveMultiInstance,
    BooleanExpression, BoundaryEventDefinition, BoundaryTimerKind, BoundaryTrigger, CaseDefinition,
    CaseLifecycle, CaseMilestoneDefinition, CasePlanItemKind, CasePlanItemState,
    CasePlanItemStatus, CaseSentryDefinition, CaseStageDefinition, Command, ComparisonOperator,
    DecisionContext, DecisionInput, DecisionOutput, DecisionRule, DecisionTable, DomainError,
    DomainEvent, ExtensionProperty, ExtensionPropertyValue, GatewayCoverage, GatewayCoverageDomain,
    GuardExpression, GuardedTransition, HitPolicy, InstanceState, IntegerInterval, Lifecycle,
    MultiInstanceDefinition, MultiInstanceMode, Node, NodeExecutionMetadata, PendingGatewayJoin,
    UnaryTest, WorkflowDefinition, WorkflowExecutionContracts, WorkflowValue, WorkflowValueType,
    decide, evolve, rehydrate,
};
