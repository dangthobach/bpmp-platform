use std::collections::BTreeMap;
use std::sync::{Arc, RwLock, RwLockReadGuard};

use bpmp_domain_core::{
    ConfigError, ResolvedConfigSnapshot, ScopeKind, TenantId, WorkflowDefinition, WorkflowType,
    WorkflowVersion,
};
use thiserror::Error;

use crate::{
    BoundaryRuntimeError, CommandDefinitionProviderPort, ConfigurationLookup,
    ConfigurationProviderPort, WorkflowDefinitionProviderPort,
};

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct RuntimeScope {
    tenant_id: TenantId,
    workflow_type: WorkflowType,
    workflow_version: WorkflowVersion,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RuntimeScopeDescriptor {
    pub tenant_id: TenantId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct RuntimeConfigurationUpdate {
    pub tenant_id: TenantId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub configuration: ResolvedConfigSnapshot,
}

#[must_use]
pub fn configuration_publication_matches_scope(
    candidate: &RuntimeScopeDescriptor,
    tenant_id: &str,
    scope_kind: ScopeKind,
    reference: &str,
    platform_reference: &str,
    environment_reference: &str,
) -> bool {
    if candidate.tenant_id.as_str() != tenant_id {
        return false;
    }
    match scope_kind {
        ScopeKind::Platform => reference == platform_reference,
        ScopeKind::Environment => reference == environment_reference,
        ScopeKind::Tenant => reference == candidate.tenant_id.as_str(),
        ScopeKind::WorkflowType => candidate.workflow_type.as_str() == reference,
        ScopeKind::WorkflowVersion => {
            reference
                .split_once(':')
                .is_some_and(|(workflow_type, workflow_version)| {
                    candidate.workflow_type.as_str() == workflow_type
                        && candidate.workflow_version.as_str() == workflow_version
                })
        }
        ScopeKind::ApprovedInstanceOverride => false,
    }
}

impl RuntimeScope {
    fn new(
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Self {
        Self {
            tenant_id: tenant_id.clone(),
            workflow_type: workflow_type.clone(),
            workflow_version: workflow_version.clone(),
        }
    }
}

#[derive(Default)]
struct RegistryState {
    definitions: BTreeMap<RuntimeScope, WorkflowDefinition>,
    configurations: BTreeMap<RuntimeScope, ResolvedConfigSnapshot>,
    references: BTreeMap<RuntimeScope, BTreeMap<RuntimeReferenceKind, u64>>,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq, Ord, PartialOrd)]
pub enum RuntimeReferenceKind {
    ActiveInstance,
    Replay,
    Snapshot,
    RetentionHold,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct MigrationSafePoint {
    pub waiting_or_terminal: bool,
    pub local_task_inflight: bool,
    pub scope_transition_inflight: bool,
}

#[derive(Clone, Default)]
pub struct RuntimeSafePointGate {
    state: Arc<RwLock<()>>,
}

pub struct RuntimeWorkPermit<'a> {
    _guard: RwLockReadGuard<'a, ()>,
}

impl RuntimeSafePointGate {
    /// Holds a shared permit for one complete command or background transition.
    ///
    /// # Errors
    ///
    /// Fails closed when the gate lock is poisoned.
    pub fn enter_work(&self) -> Result<RuntimeWorkPermit<'_>, RuntimeRegistryError> {
        self.state
            .read()
            .map(|guard| RuntimeWorkPermit { _guard: guard })
            .map_err(|_| RuntimeRegistryError::LockPoisoned)
    }

    /// Runs one synchronous update after every in-flight transition leaves.
    ///
    /// Network and database I/O must complete before invoking this method.
    ///
    /// # Errors
    ///
    /// Fails closed when the gate lock is poisoned.
    pub fn with_safe_point<T>(
        &self,
        update: impl FnOnce(MigrationSafePoint) -> T,
    ) -> Result<T, RuntimeRegistryError> {
        let _guard = self
            .state
            .write()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?;
        Ok(update(MigrationSafePoint {
            waiting_or_terminal: true,
            local_task_inflight: false,
            scope_transition_inflight: false,
        }))
    }
}

impl MigrationSafePoint {
    const fn permits_migration(self) -> bool {
        self.waiting_or_terminal && !self.local_task_inflight && !self.scope_transition_inflight
    }
}

/// Atomically replaceable, tenant-scoped runtime artifacts.
///
/// Only verified WIR definitions and validated immutable configuration snapshots
/// may be installed. Readers either observe the old complete entry or the new
/// complete entry; no partially loaded artifact is visible.
#[derive(Clone, Default)]
pub struct RuntimeRegistry {
    state: Arc<RwLock<RegistryState>>,
}

impl RuntimeRegistry {
    /// Installs one verified definition and its matching immutable configuration.
    ///
    /// # Errors
    ///
    /// Returns [`RuntimeRegistryError`] for an invalid scope or poisoned registry lock.
    pub fn install(
        &self,
        definition: WorkflowDefinition,
        configuration: ResolvedConfigSnapshot,
    ) -> Result<(), RuntimeRegistryError> {
        if definition.tenant_id.as_str().is_empty()
            || definition.workflow_type.as_str().is_empty()
            || definition.workflow_version.as_str().is_empty()
        {
            return Err(RuntimeRegistryError::InvalidScope);
        }
        let scope = RuntimeScope::new(
            &definition.tenant_id,
            &definition.workflow_type,
            &definition.workflow_version,
        );
        let mut state = self
            .state
            .write()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?;
        state.definitions.insert(scope.clone(), definition);
        state.configurations.insert(scope, configuration);
        Ok(())
    }

    /// Replaces one installed artifact only at an explicit engine safe point.
    ///
    /// # Errors
    ///
    /// Fails closed while a local task or retained-scope transition is in flight.
    pub fn migrate(
        &self,
        definition: WorkflowDefinition,
        configuration: ResolvedConfigSnapshot,
        safe_point: MigrationSafePoint,
    ) -> Result<(), RuntimeRegistryError> {
        if !safe_point.permits_migration() {
            return Err(RuntimeRegistryError::UnsafeMigrationPoint);
        }
        self.install(definition, configuration)
    }

    /// Atomically replaces validated configuration snapshots at one safe point.
    ///
    /// # Errors
    ///
    /// Rejects the entire batch when migration is unsafe, a definition is
    /// missing, a scope is duplicated, or the registry lock is poisoned.
    pub fn replace_configurations(
        &self,
        updates: Vec<RuntimeConfigurationUpdate>,
        safe_point: MigrationSafePoint,
    ) -> Result<usize, RuntimeRegistryError> {
        if !safe_point.permits_migration() {
            return Err(RuntimeRegistryError::UnsafeMigrationPoint);
        }
        let mut state = self
            .state
            .write()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?;
        let mut replacements = BTreeMap::new();
        for update in updates {
            let scope = RuntimeScope::new(
                &update.tenant_id,
                &update.workflow_type,
                &update.workflow_version,
            );
            if !state.definitions.contains_key(&scope) {
                return Err(RuntimeRegistryError::MissingDefinition);
            }
            if replacements.insert(scope, update.configuration).is_some() {
                return Err(RuntimeRegistryError::DuplicateScope);
            }
        }
        let changed = replacements
            .iter()
            .filter(|(scope, configuration)| {
                state
                    .configurations
                    .get(*scope)
                    .is_none_or(|current| current.content_hash != configuration.content_hash)
            })
            .count();
        state.configurations.extend(replacements);
        Ok(changed)
    }

    /// Returns a stable ordered copy of every installed runtime scope.
    ///
    /// # Errors
    ///
    /// Returns [`RuntimeRegistryError::LockPoisoned`] on lock poisoning.
    pub fn installed_scopes(&self) -> Result<Vec<RuntimeScopeDescriptor>, RuntimeRegistryError> {
        self.state
            .read()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)
            .map(|state| {
                state
                    .definitions
                    .keys()
                    .map(|scope| RuntimeScopeDescriptor {
                        tenant_id: scope.tenant_id.clone(),
                        workflow_type: scope.workflow_type.clone(),
                        workflow_version: scope.workflow_version.clone(),
                    })
                    .collect()
            })
    }

    /// Adds a durable-use reference to an installed WIR version.
    ///
    /// # Errors
    ///
    /// Fails for a missing artifact, counter overflow, or poisoned lock.
    pub fn acquire_reference(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
        kind: RuntimeReferenceKind,
    ) -> Result<(), RuntimeRegistryError> {
        let scope = RuntimeScope::new(tenant_id, workflow_type, workflow_version);
        let mut state = self
            .state
            .write()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?;
        if !state.definitions.contains_key(&scope) {
            return Err(RuntimeRegistryError::MissingDefinition);
        }
        let count = state
            .references
            .entry(scope)
            .or_default()
            .entry(kind)
            .or_default();
        *count = count
            .checked_add(1)
            .ok_or(RuntimeRegistryError::ReferenceOverflow)?;
        Ok(())
    }

    /// Releases a previously acquired durable-use reference.
    ///
    /// # Errors
    ///
    /// Fails closed on an unbalanced release or poisoned lock.
    pub fn release_reference(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
        kind: RuntimeReferenceKind,
    ) -> Result<(), RuntimeRegistryError> {
        let scope = RuntimeScope::new(tenant_id, workflow_type, workflow_version);
        let mut state = self
            .state
            .write()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?;
        let references = state
            .references
            .get_mut(&scope)
            .ok_or(RuntimeRegistryError::UnbalancedReference)?;
        let count = references
            .get_mut(&kind)
            .ok_or(RuntimeRegistryError::UnbalancedReference)?;
        *count = count
            .checked_sub(1)
            .ok_or(RuntimeRegistryError::UnbalancedReference)?;
        if *count == 0 {
            references.remove(&kind);
        }
        if references.is_empty() {
            state.references.remove(&scope);
        }
        Ok(())
    }

    /// Unloads a WIR/configuration pair only when no durable use remains.
    ///
    /// # Errors
    ///
    /// Fails while any active-instance, replay, snapshot, or retention reference exists.
    pub fn retire(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<(), RuntimeRegistryError> {
        let scope = RuntimeScope::new(tenant_id, workflow_type, workflow_version);
        let mut state = self
            .state
            .write()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?;
        if state
            .references
            .get(&scope)
            .is_some_and(|references| references.values().any(|count| *count != 0))
        {
            return Err(RuntimeRegistryError::ArtifactInUse);
        }
        if state.definitions.remove(&scope).is_none() {
            return Err(RuntimeRegistryError::MissingDefinition);
        }
        state.configurations.remove(&scope);
        state.references.remove(&scope);
        Ok(())
    }

    /// Returns the number of installed tenant/workflow/version scopes.
    ///
    /// # Errors
    ///
    /// Returns [`RuntimeRegistryError::LockPoisoned`] if another thread poisoned the lock.
    pub fn len(&self) -> Result<usize, RuntimeRegistryError> {
        self.state
            .read()
            .map(|state| state.definitions.len())
            .map_err(|_| RuntimeRegistryError::LockPoisoned)
    }

    /// Returns whether the registry contains no installed scopes.
    ///
    /// # Errors
    ///
    /// Returns [`RuntimeRegistryError::LockPoisoned`] if another thread poisoned the lock.
    pub fn is_empty(&self) -> Result<bool, RuntimeRegistryError> {
        self.len().map(|length| length == 0)
    }

    fn definition(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, RuntimeRegistryError> {
        let scope = RuntimeScope::new(tenant_id, workflow_type, workflow_version);
        self.state
            .read()
            .map_err(|_| RuntimeRegistryError::LockPoisoned)?
            .definitions
            .get(&scope)
            .cloned()
            .ok_or(RuntimeRegistryError::MissingDefinition)
    }
}

impl CommandDefinitionProviderPort for RuntimeRegistry {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, String> {
        self.definition(tenant_id, workflow_type, workflow_version)
            .map_err(|error| error.to_string())
    }
}

impl WorkflowDefinitionProviderPort for RuntimeRegistry {
    fn resolve(
        &self,
        tenant_id: &TenantId,
        workflow_type: &WorkflowType,
        workflow_version: &WorkflowVersion,
    ) -> Result<WorkflowDefinition, BoundaryRuntimeError> {
        self.definition(tenant_id, workflow_type, workflow_version)
            .map_err(|error| BoundaryRuntimeError::DefinitionUnavailable(error.to_string()))
    }
}

impl ConfigurationProviderPort for RuntimeRegistry {
    fn resolve(&self, lookup: &ConfigurationLookup) -> Result<ResolvedConfigSnapshot, ConfigError> {
        let scope = RuntimeScope::new(
            &lookup.tenant_id,
            &lookup.workflow_type,
            &lookup.workflow_version,
        );
        self.state
            .read()
            .map_err(|_| ConfigError::MissingPublishedSnapshot)?
            .configurations
            .get(&scope)
            .cloned()
            .ok_or(ConfigError::MissingPublishedSnapshot)
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum RuntimeRegistryError {
    #[error("runtime artifact scope is invalid")]
    InvalidScope,
    #[error("runtime registry lock is poisoned")]
    LockPoisoned,
    #[error("verified workflow definition is not installed")]
    MissingDefinition,
    #[error("workflow migration is not at a safe point")]
    UnsafeMigrationPoint,
    #[error("runtime artifact reference counter overflowed")]
    ReferenceOverflow,
    #[error("runtime artifact reference release is unbalanced")]
    UnbalancedReference,
    #[error("runtime artifact is still referenced")]
    ArtifactInUse,
    #[error("runtime configuration update contains a duplicate scope")]
    DuplicateScope,
}

#[cfg(test)]
mod tests {
    use bpmp_domain_core::{Node, NodeId};
    use proptest::prelude::*;

    use super::*;

    #[test]
    fn configuration_publication_is_tenant_and_deployment_scoped() {
        let candidate = RuntimeScopeDescriptor {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            workflow_type: WorkflowType::new("order").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
        };
        assert!(configuration_publication_matches_scope(
            &candidate,
            "tenant-a",
            ScopeKind::Environment,
            "production",
            "platform-a",
            "production",
        ));
        assert!(!configuration_publication_matches_scope(
            &candidate,
            "tenant-b",
            ScopeKind::Environment,
            "production",
            "platform-a",
            "production",
        ));
        assert!(!configuration_publication_matches_scope(
            &candidate,
            "tenant-a",
            ScopeKind::Environment,
            "staging",
            "platform-a",
            "production",
        ));
        assert!(configuration_publication_matches_scope(
            &candidate,
            "tenant-a",
            ScopeKind::WorkflowVersion,
            "order:1",
            "platform-a",
            "production",
        ));
    }

    fn registry() -> (RuntimeRegistry, TenantId, WorkflowType, WorkflowVersion) {
        let registry = RuntimeRegistry::default();
        let tenant = TenantId::new("tenant-a").unwrap();
        let workflow_type = WorkflowType::new("order").unwrap();
        let version = WorkflowVersion::new("1").unwrap();
        let start = NodeId::new("start").unwrap();
        let end = NodeId::new("end").unwrap();
        let definition = WorkflowDefinition::new(
            tenant.clone(),
            workflow_type.clone(),
            version.clone(),
            start.clone(),
            [(start, Node::Start { next: end.clone() }), (end, Node::End)],
        )
        .unwrap();
        let scope = RuntimeScope::new(&tenant, &workflow_type, &version);
        registry
            .state
            .write()
            .unwrap()
            .definitions
            .insert(scope, definition);
        (registry, tenant, workflow_type, version)
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 40: Workflow migration occurs only at a safe point
        #[test]
        fn migration_safe_point_requires_wait_and_no_inflight_work(
            waiting in any::<bool>(),
            local_inflight in any::<bool>(),
            scope_inflight in any::<bool>(),
        ) {
            let point = MigrationSafePoint {
                waiting_or_terminal: waiting,
                local_task_inflight: local_inflight,
                scope_transition_inflight: scope_inflight,
            };
            prop_assert_eq!(
                point.permits_migration(),
                waiting && !local_inflight && !scope_inflight
            );
        }

        // Feature: rust-bpm-platform, Property 41: WIR retirement waits for every durable reference
        #[test]
        fn retirement_is_blocked_until_all_reference_kinds_release(
            active in 0_u8..4,
            replay in 0_u8..4,
            snapshot in 0_u8..4,
            retention in 0_u8..4,
        ) {
            let (registry, tenant, workflow_type, version) = registry();
            let counts = [
                (RuntimeReferenceKind::ActiveInstance, active),
                (RuntimeReferenceKind::Replay, replay),
                (RuntimeReferenceKind::Snapshot, snapshot),
                (RuntimeReferenceKind::RetentionHold, retention),
            ];
            for (kind, count) in counts {
                for _ in 0..count {
                    registry.acquire_reference(&tenant, &workflow_type, &version, kind).unwrap();
                }
            }
            let has_references = active + replay + snapshot + retention > 0;
            let result = registry.retire(&tenant, &workflow_type, &version);
            if has_references {
                prop_assert_eq!(result.unwrap_err(), RuntimeRegistryError::ArtifactInUse);
                for (kind, count) in counts {
                    for _ in 0..count {
                        registry.release_reference(&tenant, &workflow_type, &version, kind).unwrap();
                    }
                }
                prop_assert!(registry.retire(&tenant, &workflow_type, &version).is_ok());
            } else {
                prop_assert!(result.is_ok());
            }
        }
    }
}
