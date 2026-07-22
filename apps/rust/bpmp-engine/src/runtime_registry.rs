use std::collections::BTreeMap;
use std::sync::{Arc, RwLock};

use bpmp_domain_core::{
    ConfigError, ResolvedConfigSnapshot, TenantId, WorkflowDefinition, WorkflowType,
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
}
