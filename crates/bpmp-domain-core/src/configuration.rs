use thiserror::Error;

use crate::{ConfigId, ConfigVersion, KeyScope, PolicyVersion};

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum ScopeKind {
    Platform,
    Environment,
    Tenant,
    WorkflowType,
    WorkflowVersion,
    ApprovedInstanceOverride,
}

impl ScopeKind {
    const fn precedence(self) -> u8 {
        match self {
            Self::Platform => 0,
            Self::Environment => 1,
            Self::Tenant => 2,
            Self::WorkflowType => 3,
            Self::WorkflowVersion => 4,
            Self::ApprovedInstanceOverride => 5,
        }
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ConfigurationScope {
    pub kind: ScopeKind,
    pub reference: String,
}

impl ConfigurationScope {
    /// Creates one resolved configuration scope.
    ///
    /// # Errors
    ///
    /// Returns [`ConfigError::EmptyScopeReference`] when no scope reference is supplied.
    pub fn new(kind: ScopeKind, reference: impl Into<String>) -> Result<Self, ConfigError> {
        let reference = reference.into();
        if reference.trim().is_empty() {
            return Err(ConfigError::EmptyScopeReference { kind });
        }
        Ok(Self { kind, reference })
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RetryPolicy {
    pub max_attempts: u32,
    pub initial_backoff_ms: u64,
    pub max_backoff_ms: u64,
    pub multiplier_millis: u32,
}

impl RetryPolicy {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.max_attempts == 0 {
            return Err(ConfigError::NonPositiveValue("max_attempts"));
        }
        if self.initial_backoff_ms == 0 {
            return Err(ConfigError::NonPositiveValue("initial_backoff_ms"));
        }
        if self.max_backoff_ms < self.initial_backoff_ms {
            return Err(ConfigError::InvalidBackoffRange);
        }
        if self.multiplier_millis == 0 {
            return Err(ConfigError::NonPositiveValue("multiplier_millis"));
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct EnginePolicy {
    pub snapshot_interval_events: u32,
    pub max_events_per_decision: u32,
    pub max_multi_instance_cardinality: u32,
    pub default_multi_instance_parallelism: u32,
    pub boundary_runtime: BoundaryRuntimePolicy,
    pub command_timeout_ms: u64,
    pub optimistic_conflict_retry: RetryPolicy,
    pub local_wasm: LocalWasmPolicy,
    pub authorization_audit_key_scope: KeyScope,
}

impl EnginePolicy {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.snapshot_interval_events == 0 {
            return Err(ConfigError::NonPositiveValue("snapshot_interval_events"));
        }
        if self.max_events_per_decision == 0 {
            return Err(ConfigError::NonPositiveValue("max_events_per_decision"));
        }
        if self.max_multi_instance_cardinality == 0 {
            return Err(ConfigError::NonPositiveValue(
                "max_multi_instance_cardinality",
            ));
        }
        if self.default_multi_instance_parallelism == 0 {
            return Err(ConfigError::NonPositiveValue(
                "default_multi_instance_parallelism",
            ));
        }
        if self.default_multi_instance_parallelism > self.max_multi_instance_cardinality {
            return Err(ConfigError::MultiInstanceParallelismExceedsCardinality);
        }
        self.boundary_runtime.validate()?;
        if self.command_timeout_ms == 0 {
            return Err(ConfigError::NonPositiveValue("command_timeout_ms"));
        }
        self.optimistic_conflict_retry.validate()?;
        self.local_wasm.validate()
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct BoundaryRuntimePolicy {
    pub projection_batch_size: u32,
    pub dispatch_batch_size: u32,
    pub max_dispatch_attempts: u32,
    pub retry_delay_ms: u64,
    pub lease_duration_ms: u64,
    pub max_timer_horizon_ms: u64,
    pub max_expression_bytes: u32,
    pub worker_id: String,
    pub max_signal_id_bytes: u32,
    pub max_reference_bytes: u32,
    pub max_subscriptions_per_instance: u32,
}

impl BoundaryRuntimePolicy {
    fn validate(&self) -> Result<(), ConfigError> {
        for (field, value) in [
            ("projection_batch_size", self.projection_batch_size),
            ("dispatch_batch_size", self.dispatch_batch_size),
            ("max_dispatch_attempts", self.max_dispatch_attempts),
            ("max_expression_bytes", self.max_expression_bytes),
            ("max_signal_id_bytes", self.max_signal_id_bytes),
            ("max_reference_bytes", self.max_reference_bytes),
            (
                "max_subscriptions_per_instance",
                self.max_subscriptions_per_instance,
            ),
        ] {
            if value == 0 {
                return Err(ConfigError::NonPositiveValue(field));
            }
        }
        for (field, value) in [
            ("boundary_retry_delay_ms", self.retry_delay_ms),
            ("boundary_lease_duration_ms", self.lease_duration_ms),
            ("max_timer_horizon_ms", self.max_timer_horizon_ms),
        ] {
            if value == 0 {
                return Err(ConfigError::NonPositiveValue(field));
            }
        }
        if self.worker_id.trim().is_empty() {
            return Err(ConfigError::EmptyBoundaryWorkerId);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct LocalWasmPolicy {
    pub max_module_bytes: u64,
    pub max_input_bytes: u64,
    pub max_output_bytes: u64,
    pub max_memory_bytes: u64,
    pub max_wasm_stack_bytes: u64,
    pub max_table_elements: u32,
    pub max_instances: u32,
    pub max_tables: u32,
    pub max_memories: u32,
    pub fuel: u64,
}

impl LocalWasmPolicy {
    fn validate(&self) -> Result<(), ConfigError> {
        for (field, value) in [
            ("max_module_bytes", self.max_module_bytes),
            ("max_input_bytes", self.max_input_bytes),
            ("max_output_bytes", self.max_output_bytes),
            ("max_memory_bytes", self.max_memory_bytes),
            ("max_wasm_stack_bytes", self.max_wasm_stack_bytes),
            ("fuel", self.fuel),
        ] {
            if value == 0 {
                return Err(ConfigError::NonPositiveValue(field));
            }
        }
        for (field, value) in [
            ("max_table_elements", self.max_table_elements),
            ("max_instances", self.max_instances),
            ("max_tables", self.max_tables),
            ("max_memories", self.max_memories),
        ] {
            if value == 0 {
                return Err(ConfigError::NonPositiveValue(field));
            }
        }
        if self.max_input_bytes > i32::MAX as u64 {
            return Err(ConfigError::WasmAbiLengthExceeded("max_input_bytes"));
        }
        if self.max_output_bytes > i32::MAX as u64 {
            return Err(ConfigError::WasmAbiLengthExceeded("max_output_bytes"));
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ResolvedConfigSnapshot {
    pub config_id: ConfigId,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
    pub schema_version: u32,
    pub resolved_scopes: Vec<ConfigurationScope>,
    pub content_hash: [u8; 32],
    pub engine: EnginePolicy,
}

impl ResolvedConfigSnapshot {
    /// Creates and validates an immutable configuration snapshot.
    ///
    /// # Errors
    ///
    /// Returns a [`ConfigError`] when required values are absent, policy values are
    /// invalid, or scopes do not follow the documented override precedence.
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        config_id: ConfigId,
        config_version: ConfigVersion,
        policy_version: PolicyVersion,
        schema_version: u32,
        resolved_scopes: Vec<ConfigurationScope>,
        content_hash: [u8; 32],
        engine: EnginePolicy,
    ) -> Result<Self, ConfigError> {
        if schema_version == 0 {
            return Err(ConfigError::NonPositiveValue("schema_version"));
        }
        if resolved_scopes.is_empty() {
            return Err(ConfigError::MissingScopes);
        }
        for scopes in resolved_scopes.windows(2) {
            if scopes[0].kind.precedence() >= scopes[1].kind.precedence() {
                return Err(ConfigError::InvalidScopeOrder);
            }
        }
        engine.validate()?;

        Ok(Self {
            config_id,
            config_version,
            policy_version,
            schema_version,
            resolved_scopes,
            content_hash,
            engine,
        })
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum ConfigError {
    #[error("configuration value {0} must be greater than zero")]
    NonPositiveValue(&'static str),
    #[error("maximum backoff must be greater than or equal to initial backoff")]
    InvalidBackoffRange,
    #[error("resolved configuration must contain at least one scope")]
    MissingScopes,
    #[error("configuration scopes must follow the documented override order")]
    InvalidScopeOrder,
    #[error("configuration scope {kind:?} must have a reference")]
    EmptyScopeReference { kind: ScopeKind },
    #[error("no published configuration snapshot matches the requested scope")]
    MissingPublishedSnapshot,
    #[error("WASM configuration value {0} exceeds the ABI i32 length range")]
    WasmAbiLengthExceeded(&'static str),
    #[error("default multi-instance parallelism exceeds maximum cardinality")]
    MultiInstanceParallelismExceedsCardinality,
    #[error("boundary runtime worker id must not be empty")]
    EmptyBoundaryWorkerId,
}
