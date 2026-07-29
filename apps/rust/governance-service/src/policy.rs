use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result};
use bpmp_contracts::configuration::v1 as configurationv1;
use tokio::sync::{Mutex, RwLock};
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity};

use crate::config::{ConfigurationResolverConfig, TlsConfig};

type ResolverClient =
    configurationv1::configuration_resolver_service_client::ConfigurationResolverServiceClient<
        Channel,
    >;

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
pub struct PolicyScope {
    pub tenant_id: String,
    pub workflow_type: String,
    pub workflow_version: String,
}

#[derive(Debug, Clone)]
pub struct ResolvedPolicy {
    pub config_version: String,
    pub policy_version: String,
    pub ordinal: u64,
    pub policy: configurationv1::GovernancePolicy,
}

#[derive(Clone)]
pub struct PolicyCache {
    values: Arc<RwLock<BTreeMap<PolicyScope, ResolvedPolicy>>>,
    client: Arc<Mutex<ResolverClient>>,
    resolver: ConfigurationResolverConfig,
}

impl PolicyCache {
    pub async fn connect(resolver: ConfigurationResolverConfig, tls: &TlsConfig) -> Result<Self> {
        let certificate = tokio::fs::read(&tls.server_certificate).await?;
        let private_key = tokio::fs::read(&tls.server_private_key).await?;
        let ca = tokio::fs::read(&tls.client_ca).await?;
        let channel = Endpoint::from_shared(resolver.endpoint.clone())?
            .connect_timeout(Duration::from_millis(resolver.timeout_ms))
            .timeout(Duration::from_millis(resolver.timeout_ms))
            .tls_config(
                ClientTlsConfig::new()
                    .domain_name(resolver.tls_domain.clone())
                    .ca_certificate(Certificate::from_pem(ca))
                    .identity(Identity::from_pem(certificate, private_key)),
            )?
            .connect()
            .await
            .context("connect governance configuration resolver")?;
        Ok(Self {
            values: Arc::new(RwLock::new(BTreeMap::new())),
            client: Arc::new(Mutex::new(ResolverClient::new(channel))),
            resolver,
        })
    }

    pub async fn get(&self, scope: &PolicyScope) -> Result<ResolvedPolicy> {
        if let Some(value) = self.values.read().await.get(scope).cloned() {
            return Ok(value);
        }
        self.refresh(scope).await
    }

    pub async fn refresh(&self, scope: &PolicyScope) -> Result<ResolvedPolicy> {
        let response = self
            .client
            .lock()
            .await
            .resolve_configuration(configurationv1::ResolveConfigurationRequest {
                tenant_id: scope.tenant_id.clone(),
                workflow_type: scope.workflow_type.clone(),
                workflow_version: scope.workflow_version.clone(),
                platform_reference: self.resolver.platform_reference.clone(),
                environment_reference: self.resolver.environment_reference.clone(),
                instance_id: String::new(),
                owner: configurationv1::ConfigurationOwner::Governance as i32,
            })
            .await
            .context("resolve governance policy")?
            .into_inner()
            .snapshot
            .context("governance resolver returned no snapshot")?;
        if configurationv1::ConfigurationOwner::try_from(response.owner)?
            != configurationv1::ConfigurationOwner::Governance
            || response.ordinal == 0
        {
            anyhow::bail!("resolved governance policy metadata is invalid");
        }
        let policy = response
            .governance
            .context("resolved governance policy body is missing")?;
        validate_policy(&policy)?;
        let value = ResolvedPolicy {
            config_version: response.config_version,
            policy_version: response.policy_version,
            ordinal: response.ordinal,
            policy,
        };
        let mut values = self.values.write().await;
        if values
            .get(scope)
            .is_some_and(|current| current.ordinal > value.ordinal)
        {
            anyhow::bail!("configuration resolver returned a stale governance policy");
        }
        values.insert(scope.clone(), value.clone());
        Ok(value)
    }

    pub async fn scopes(&self) -> Vec<PolicyScope> {
        self.values.read().await.keys().cloned().collect()
    }

    pub async fn retire_matching(
        &self,
        event: &configurationv1::ConfigurationPublicationEvent,
    ) -> Result<usize> {
        let scope = event
            .scope
            .as_ref()
            .context("governance retirement has no scope")?;
        let scope_type = configurationv1::ConfigurationScopeType::try_from(scope.r#type)?;
        let mut values = self.values.write().await;
        let before = values.len();
        values.retain(|candidate, value| {
            if candidate.tenant_id != event.tenant_id || value.ordinal > event.ordinal {
                return true;
            }
            !publication_matches(
                candidate,
                scope_type,
                &scope.reference,
                &event.workflow_type,
                &event.workflow_version,
            )
        });
        Ok(before - values.len())
    }
}

fn publication_matches(
    scope: &PolicyScope,
    scope_type: configurationv1::ConfigurationScopeType,
    reference: &str,
    workflow_type: &str,
    workflow_version: &str,
) -> bool {
    match scope_type {
        configurationv1::ConfigurationScopeType::Platform
        | configurationv1::ConfigurationScopeType::Environment
        | configurationv1::ConfigurationScopeType::Tenant => true,
        configurationv1::ConfigurationScopeType::WorkflowType => scope.workflow_type == reference,
        configurationv1::ConfigurationScopeType::WorkflowVersion => {
            let expected = format!("{}:{}", scope.workflow_type, scope.workflow_version);
            expected == reference
                || (scope.workflow_type == workflow_type
                    && scope.workflow_version == workflow_version)
        }
        configurationv1::ConfigurationScopeType::ApprovedInstanceOverride
        | configurationv1::ConfigurationScopeType::Unspecified => false,
    }
}

fn validate_policy(policy: &configurationv1::GovernancePolicy) -> Result<()> {
    let retry = policy.kms_retry.as_ref().context("KMS retry is missing")?;
    if policy.approval_ttl_ms == 0
        || policy.fresh_authentication_max_age_ms == 0
        || policy.kms_request_timeout_ms == 0
        || retry.max_attempts == 0
        || retry.initial_backoff_ms == 0
        || retry.max_backoff_ms < retry.initial_backoff_ms
        || retry.multiplier_millis < 1_000
        || policy.key_cache_ttl_ms == 0
        || policy.revocation_barrier_timeout_ms == 0
        || policy.reconciliation_batch_size == 0
        || policy.max_pending_compensations == 0
        || policy.abort_capability.trim().is_empty()
        || policy.accepted_auth_assurance.is_empty()
        || policy.approval_keys.is_empty()
        || policy.required_approver_count == 0
        || usize::try_from(policy.required_approver_count).map_or(true, |required| {
            policy
                .approval_keys
                .iter()
                .filter(|key| key.enabled)
                .count()
                < required
        })
    {
        anyhow::bail!("resolved governance policy is incomplete or unbounded");
    }
    Ok(())
}
