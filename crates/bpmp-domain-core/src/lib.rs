//! Pure deterministic workflow domain.
//!
//! This crate intentionally has no I/O, async runtime, clock, randomness, or
//! environment access. Adapters must validate and inject every external value.

mod configuration;
mod identifiers;
mod workflow;

pub use configuration::{
    ConfigError, ConfigurationScope, EnginePolicy, LocalWasmPolicy, ResolvedConfigSnapshot,
    RetryPolicy, ScopeKind,
};
pub use identifiers::{
    ActorId, CommandId, ConfigId, ConfigVersion, CorrelationId, IdempotencyKey, IdentifierError,
    InstanceId, KeyScope, NodeId, PolicyVersion, TaskType, TenantId, WorkflowType, WorkflowVersion,
};
pub use workflow::{
    Command, ComparisonOperator, DecisionContext, DecisionInput, DecisionOutput, DecisionRule,
    DecisionTable, DomainError, DomainEvent, GatewayCoverage, GatewayCoverageDomain,
    GuardExpression, GuardedTransition, HitPolicy, InstanceState, IntegerInterval, Lifecycle, Node,
    UnaryTest, WorkflowDefinition, WorkflowValue, WorkflowValueType, decide, evolve, rehydrate,
};
