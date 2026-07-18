use bpmp_contracts::engine::v1 as wire;
use bpmp_domain_core::{
    ConfigVersion, IdentifierError, InstanceId, InstanceState, KeyScope, Lifecycle, NodeId,
    PolicyVersion, TenantId, WorkflowType, WorkflowVersion,
};
use prost::Message;
use thiserror::Error;

use crate::SnapshotEnvelope;

pub const SNAPSHOT_SCHEMA_VERSION: u32 = 1;

pub struct SnapshotCodec;

impl SnapshotCodec {
    pub fn encode(snapshot: &SnapshotEnvelope) -> Vec<u8> {
        to_wire(snapshot).encode_to_vec()
    }

    /// Decodes a durable snapshot into validated domain types.
    ///
    /// # Errors
    ///
    /// Returns [`SnapshotCodecError`] for malformed bytes, unsupported schemas,
    /// invalid identifiers, or inconsistent lifecycle fields.
    pub fn decode(bytes: &[u8]) -> Result<SnapshotEnvelope, SnapshotCodecError> {
        let snapshot = wire::WorkflowSnapshot::decode(bytes)
            .map_err(|error| SnapshotCodecError::Decode(error.to_string()))?;
        from_wire(snapshot)
    }
}

fn to_wire(snapshot: &SnapshotEnvelope) -> wire::WorkflowSnapshot {
    let (lifecycle, active_node_id) = match &snapshot.state.lifecycle {
        Lifecycle::Initial => (wire::WorkflowLifecycle::Initial, String::new()),
        Lifecycle::Active { active_node } => {
            (wire::WorkflowLifecycle::Active, active_node.to_string())
        }
        Lifecycle::Completed => (wire::WorkflowLifecycle::Completed, String::new()),
    };
    wire::WorkflowSnapshot {
        tenant_id: snapshot.tenant_id.to_string(),
        instance_id: snapshot.instance_id.to_string(),
        sequence: snapshot.state.sequence,
        schema_version: SNAPSHOT_SCHEMA_VERSION,
        workflow_type: snapshot.workflow_type.to_string(),
        workflow_version: snapshot.workflow_version.to_string(),
        lifecycle: lifecycle.into(),
        active_node_id,
        config_version: snapshot.config_version.to_string(),
        policy_version: snapshot.policy_version.to_string(),
        encryption_key_scope: snapshot.encryption_key_scope.to_string(),
    }
}

fn from_wire(snapshot: wire::WorkflowSnapshot) -> Result<SnapshotEnvelope, SnapshotCodecError> {
    if snapshot.schema_version != SNAPSHOT_SCHEMA_VERSION {
        return Err(SnapshotCodecError::UnsupportedSchema(
            snapshot.schema_version,
        ));
    }
    let lifecycle = match wire::WorkflowLifecycle::try_from(snapshot.lifecycle)
        .map_err(|_| SnapshotCodecError::InvalidLifecycle(snapshot.lifecycle))?
    {
        wire::WorkflowLifecycle::Initial if snapshot.active_node_id.is_empty() => {
            Lifecycle::Initial
        }
        wire::WorkflowLifecycle::Active if !snapshot.active_node_id.is_empty() => {
            Lifecycle::Active {
                active_node: identifier(NodeId::new, snapshot.active_node_id, "active_node_id")?,
            }
        }
        wire::WorkflowLifecycle::Completed if snapshot.active_node_id.is_empty() => {
            Lifecycle::Completed
        }
        lifecycle => return Err(SnapshotCodecError::InconsistentLifecycle(lifecycle)),
    };
    Ok(SnapshotEnvelope {
        tenant_id: identifier(TenantId::new, snapshot.tenant_id, "tenant_id")?,
        instance_id: identifier(InstanceId::new, snapshot.instance_id, "instance_id")?,
        workflow_type: identifier(WorkflowType::new, snapshot.workflow_type, "workflow_type")?,
        workflow_version: identifier(
            WorkflowVersion::new,
            snapshot.workflow_version,
            "workflow_version",
        )?,
        state: InstanceState {
            lifecycle,
            sequence: snapshot.sequence,
        },
        config_version: identifier(
            ConfigVersion::new,
            snapshot.config_version,
            "config_version",
        )?,
        policy_version: identifier(
            PolicyVersion::new,
            snapshot.policy_version,
            "policy_version",
        )?,
        encryption_key_scope: identifier(
            KeyScope::new,
            snapshot.encryption_key_scope,
            "encryption_key_scope",
        )?,
    })
}

fn identifier<T>(
    constructor: impl FnOnce(String) -> Result<T, IdentifierError>,
    value: String,
    field: &'static str,
) -> Result<T, SnapshotCodecError> {
    constructor(value).map_err(|source| SnapshotCodecError::Identifier { field, source })
}

#[derive(Debug, Error)]
pub enum SnapshotCodecError {
    #[error("snapshot bytes cannot be decoded: {0}")]
    Decode(String),
    #[error("unsupported snapshot schema version {0}")]
    UnsupportedSchema(u32),
    #[error("snapshot contains unknown lifecycle value {0}")]
    InvalidLifecycle(i32),
    #[error("snapshot lifecycle fields are inconsistent for {0:?}")]
    InconsistentLifecycle(wire::WorkflowLifecycle),
    #[error("invalid snapshot identifier in field {field}: {source}")]
    Identifier {
        field: &'static str,
        source: IdentifierError,
    },
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durable_active_snapshot_round_trips() {
        let expected = SnapshotEnvelope {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            workflow_type: WorkflowType::new("order").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
            state: InstanceState {
                lifecycle: Lifecycle::Active {
                    active_node: NodeId::new("charge").unwrap(),
                },
                sequence: 100,
            },
            config_version: ConfigVersion::new("config-7").unwrap(),
            policy_version: PolicyVersion::new("policy-3").unwrap(),
            encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
        };

        assert_eq!(
            SnapshotCodec::decode(&SnapshotCodec::encode(&expected)).unwrap(),
            expected
        );
    }
}
