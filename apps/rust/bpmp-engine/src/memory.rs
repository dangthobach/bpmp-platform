//! Non-durable adapters for development and tests with synthetic data only.

use std::collections::BTreeMap;
use std::sync::Mutex;

use bpmp_domain_core::{
    ActorId, CommandId, ConfigError, IdempotencyKey, InstanceId, ResolvedConfigSnapshot, TenantId,
};

use crate::ports::{
    CommitOutcome, CommitRequest, ConfigurationLookup, ConfigurationProviderPort, LoadedInstance,
    StoreError, WorkflowStorePort,
};
use crate::{
    AuthorizationAudit, CommittedResult, EventCodec, EventEnvelope, OutboxError, OutboxRecord,
    OutboxStorePort, SnapshotEnvelope,
};

#[derive(Default)]
pub struct InMemoryConfigurationProvider {
    snapshots: BTreeMap<ConfigurationLookup, ResolvedConfigSnapshot>,
}

impl InMemoryConfigurationProvider {
    pub fn insert(
        &mut self,
        lookup: ConfigurationLookup,
        snapshot: ResolvedConfigSnapshot,
    ) -> Option<ResolvedConfigSnapshot> {
        self.snapshots.insert(lookup, snapshot)
    }
}

impl ConfigurationProviderPort for InMemoryConfigurationProvider {
    fn resolve(&self, lookup: &ConfigurationLookup) -> Result<ResolvedConfigSnapshot, ConfigError> {
        self.snapshots
            .get(lookup)
            .cloned()
            .ok_or(ConfigError::MissingPublishedSnapshot)
    }
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct StreamKey {
    tenant_id: TenantId,
    instance_id: InstanceId,
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct IdempotencyScope {
    tenant_id: TenantId,
    actor_id: ActorId,
    idempotency_key: IdempotencyKey,
}

#[derive(Debug, Clone, Eq, PartialEq, Ord, PartialOrd)]
struct AuthorizationAuditKey {
    tenant_id: TenantId,
    command_id: CommandId,
}

#[derive(Debug, Clone)]
struct StoredResult {
    command_id: CommandId,
    result: CommittedResult,
}

#[derive(Default)]
struct MemoryState {
    streams: BTreeMap<StreamKey, Vec<EventEnvelope>>,
    snapshots: BTreeMap<StreamKey, SnapshotEnvelope>,
    idempotency: BTreeMap<IdempotencyScope, StoredResult>,
    authorization_audits: BTreeMap<AuthorizationAuditKey, AuthorizationAudit>,
    outbox: Vec<OutboxRecord>,
    outbox_checkpoint: u64,
}

#[derive(Default)]
pub struct InMemoryWorkflowStore {
    state: Mutex<MemoryState>,
}

impl InMemoryWorkflowStore {
    /// Reads a synthetic authorization audit from the development adapter.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] when the in-memory lock is poisoned.
    pub fn authorization_audit(
        &self,
        tenant_id: &TenantId,
        command_id: &CommandId,
    ) -> Result<Option<AuthorizationAudit>, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        Ok(state
            .authorization_audits
            .get(&AuthorizationAuditKey {
                tenant_id: tenant_id.clone(),
                command_id: command_id.clone(),
            })
            .cloned())
    }

    /// Returns the number of synthetic audit records held by this adapter.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError`] when the in-memory lock is poisoned.
    pub fn authorization_audit_count(&self) -> Result<usize, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        Ok(state.authorization_audits.len())
    }
}

impl WorkflowStorePort for InMemoryWorkflowStore {
    fn lookup_idempotency(
        &self,
        tenant_id: &TenantId,
        actor_id: &ActorId,
        idempotency_key: &IdempotencyKey,
        command_id: &CommandId,
    ) -> Result<Option<CommittedResult>, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let stored = state.idempotency.get(&IdempotencyScope {
            tenant_id: tenant_id.clone(),
            actor_id: actor_id.clone(),
            idempotency_key: idempotency_key.clone(),
        });
        match stored {
            Some(stored) if stored.command_id == *command_id => Ok(Some(stored.result.clone())),
            Some(_) => Err(StoreError::IdempotencyConflict),
            None => Ok(None),
        }
    }

    fn load(
        &self,
        tenant_id: &TenantId,
        instance_id: &InstanceId,
    ) -> Result<LoadedInstance, StoreError> {
        let state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let stream_key = StreamKey {
            tenant_id: tenant_id.clone(),
            instance_id: instance_id.clone(),
        };
        let stream = state.streams.get(&stream_key).cloned().unwrap_or_default();
        let version = u64::try_from(stream.len())
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let snapshot = state.snapshots.get(&stream_key).cloned();
        let snapshot_sequence = snapshot
            .as_ref()
            .map_or(0, |snapshot| snapshot.state.sequence);
        let events = stream
            .into_iter()
            .filter(|event| event.metadata.sequence > snapshot_sequence)
            .collect();
        Ok(LoadedInstance {
            snapshot,
            events,
            version,
        })
    }

    fn commit(&self, request: CommitRequest) -> Result<CommitOutcome, StoreError> {
        request.validate_authorization_audit()?;
        let mut state = self
            .state
            .lock()
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let idempotency_scope = IdempotencyScope {
            tenant_id: request.tenant_id.clone(),
            actor_id: request.actor_id,
            idempotency_key: request.idempotency_key,
        };
        if let Some(stored) = state.idempotency.get(&idempotency_scope) {
            return if stored.command_id == request.command_id {
                Ok(CommitOutcome::Duplicate(stored.result.clone()))
            } else {
                Err(StoreError::IdempotencyConflict)
            };
        }

        let audit_key = AuthorizationAuditKey {
            tenant_id: request.tenant_id.clone(),
            command_id: request.command_id.clone(),
        };
        if state.authorization_audits.contains_key(&audit_key) {
            return Err(StoreError::InvalidAuthorizationAudit);
        }

        let stream_key = StreamKey {
            tenant_id: request.tenant_id.clone(),
            instance_id: request.instance_id.clone(),
        };
        if request.snapshot.as_ref().is_some_and(|snapshot| {
            snapshot.tenant_id != request.tenant_id
                || snapshot.instance_id != request.instance_id
                || snapshot.state.sequence <= request.expected_version
                || snapshot.state.sequence > request.result.version
        }) {
            return Err(StoreError::InvalidSnapshot);
        }
        let outbox_tail = u64::try_from(state.outbox.len())
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        let outbox_records = request
            .events
            .iter()
            .enumerate()
            .map(|(index, event)| {
                let offset = u64::try_from(index)
                    .map_err(|error| StoreError::Unavailable(error.to_string()))?;
                let cursor = outbox_tail
                    .checked_add(offset)
                    .and_then(|value| value.checked_add(1))
                    .ok_or_else(|| StoreError::Unavailable("outbox cursor overflow".into()))?;
                Ok(OutboxRecord {
                    cursor,
                    tenant_id: request.tenant_id.to_string(),
                    instance_id: request.instance_id.to_string(),
                    event_id: event.metadata.event_id.clone(),
                    payload: EventCodec::encode(event),
                })
            })
            .collect::<Result<Vec<_>, StoreError>>()?;
        let stream = state.streams.entry(stream_key.clone()).or_default();
        let actual = u64::try_from(stream.len())
            .map_err(|error| StoreError::Unavailable(error.to_string()))?;
        if actual != request.expected_version {
            return Err(StoreError::VersionConflict {
                expected: request.expected_version,
                actual,
            });
        }
        let first_sequence = request.events.first().map(|event| event.metadata.sequence);
        if first_sequence.is_some_and(|sequence| sequence != actual + 1)
            || request
                .events
                .windows(2)
                .any(|events| events[1].metadata.sequence != events[0].metadata.sequence + 1)
        {
            return Err(StoreError::NonContiguousSequence);
        }
        stream.extend(request.events);
        state.outbox.extend(outbox_records);
        if let Some(snapshot) = request.snapshot {
            state.snapshots.insert(stream_key, snapshot);
        }
        state.idempotency.insert(
            idempotency_scope,
            StoredResult {
                command_id: request.command_id,
                result: request.result.clone(),
            },
        );
        state
            .authorization_audits
            .insert(audit_key, request.authorization_audit);
        Ok(CommitOutcome::Committed(request.result))
    }
}

impl OutboxStorePort for InMemoryWorkflowStore {
    fn read_after(&self, cursor: u64, limit: usize) -> Result<Vec<OutboxRecord>, OutboxError> {
        if limit == 0 {
            return Err(OutboxError::InvalidConfiguration);
        }
        let state = self
            .state
            .lock()
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        Ok(state
            .outbox
            .iter()
            .filter(|record| record.cursor > cursor)
            .take(limit)
            .cloned()
            .collect())
    }

    fn checkpoint(&self, expected: u64, committed: u64) -> Result<(), OutboxError> {
        let mut state = self
            .state
            .lock()
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        if state.outbox_checkpoint != expected {
            return Err(OutboxError::CheckpointConflict);
        }
        let tail = u64::try_from(state.outbox.len())
            .map_err(|error| OutboxError::StoreUnavailable(error.to_string()))?;
        if committed <= expected || committed > tail {
            return Err(OutboxError::StoreUnavailable(
                "outbox checkpoint is outside the committed range".into(),
            ));
        }
        state.outbox_checkpoint = committed;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use bpmp_domain_core::{
        ConfigVersion, CorrelationId, KeyScope, PolicyVersion, WorkflowType, WorkflowVersion,
    };

    use super::*;

    #[test]
    fn mismatched_authorization_audit_leaves_store_unchanged() {
        let tenant_id = TenantId::new("tenant-a").unwrap();
        let instance_id = InstanceId::new("instance-1").unwrap();
        let command_id = CommandId::new("command-1").unwrap();
        let policy_version = PolicyVersion::new("policy-1").unwrap();
        let config_version = ConfigVersion::new("config-1").unwrap();
        let store = InMemoryWorkflowStore::default();
        let request = CommitRequest {
            tenant_id: tenant_id.clone(),
            instance_id: instance_id.clone(),
            actor_id: ActorId::new("actor-1").unwrap(),
            idempotency_key: IdempotencyKey::new("idempotency-1").unwrap(),
            command_id: command_id.clone(),
            expected_version: 0,
            events: Vec::new(),
            snapshot: None,
            authorization_audit: AuthorizationAudit {
                decision_id: "allow:command-1".into(),
                tenant_id: tenant_id.clone(),
                actor_id: ActorId::new("different-actor").unwrap(),
                workload_id: "gateway".into(),
                roles: vec!["operator".into()],
                action: "START".into(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                instance_id: instance_id.clone(),
                active_node_id: "start".into(),
                policy_version: policy_version.clone(),
                config_version: config_version.clone(),
                bundle_sequence: 1,
                revoke_epoch: 1,
                occurred_at_epoch_ms: 42,
                command_id: command_id.clone(),
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                matched_grant_ids: vec!["allow-start".into()],
                encryption_key_scope: KeyScope::new("tenant-a/compliance-audit").unwrap(),
            },
            result: CommittedResult {
                version: 0,
                event_ids: Vec::new(),
                config_version,
                policy_version,
            },
        };

        assert_eq!(
            store.commit(request),
            Err(StoreError::InvalidAuthorizationAudit)
        );
        assert_eq!(store.authorization_audit_count().unwrap(), 0);
        assert_eq!(store.load(&tenant_id, &instance_id).unwrap().version, 0);
    }
}
