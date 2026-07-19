use bpmp_contracts::engine::v1 as wire;
use bpmp_domain_core::{
    ActiveBoundarySubscription, ActiveMultiInstance, BoundaryTimerKind, BoundaryTrigger,
    ConfigVersion, IdentifierError, InstanceId, InstanceState, KeyScope, Lifecycle,
    MultiInstanceMode, NodeId, PendingGatewayJoin, PolicyVersion, TaskType, TenantId, WorkflowType,
    WorkflowValue, WorkflowVersion,
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
        variables: snapshot
            .state
            .variables
            .iter()
            .map(|(name, value)| wire::WorkflowVariable {
                name: name.clone(),
                value: Some(workflow_value_to_wire(value)),
            })
            .collect(),
        active_tokens: snapshot
            .state
            .active_tokens
            .iter()
            .map(|(node_id, count)| wire::ActiveToken {
                node_id: node_id.to_string(),
                count: *count,
            })
            .collect(),
        pending_gateway_joins: snapshot
            .state
            .pending_gateway_joins
            .iter()
            .map(|(gateway_id, join)| wire::PendingGatewayJoin {
                gateway_id: gateway_id.to_string(),
                expected_tokens: join.expected_tokens,
                arrived_tokens: join.arrived_tokens,
            })
            .collect(),
        active_multi_instances: snapshot
            .state
            .active_multi_instances
            .iter()
            .map(|(node_id, active)| wire::ActiveMultiInstance {
                node_id: node_id.to_string(),
                task_type: active.task_type.to_string(),
                mode: multi_instance_mode_to_wire(active.mode).into(),
                total_instances: active.total_instances,
                next_iteration: active.next_iteration,
                max_parallelism: active.max_parallelism,
                item_variable: active.item_variable.clone().unwrap_or_default(),
                items: active
                    .items
                    .iter()
                    .map(workflow_value_item_to_wire)
                    .collect(),
                active_iterations: active.active_iterations.iter().copied().collect(),
                completed_iterations: active.completed_iterations.iter().copied().collect(),
            })
            .collect(),
        active_boundary_subscriptions: snapshot
            .state
            .active_boundary_subscriptions
            .iter()
            .map(|(boundary_event_id, subscription)| {
                let (trigger_kind, trigger_reference) =
                    boundary_trigger_to_wire(&subscription.trigger);
                wire::ActiveBoundarySubscription {
                    boundary_event_id: boundary_event_id.to_string(),
                    attached_node_id: subscription.attached_node_id.to_string(),
                    target_node_id: subscription.target_node_id.to_string(),
                    cancel_activity: subscription.cancel_activity,
                    trigger_kind: trigger_kind.into(),
                    trigger_reference,
                    armed_at_epoch_ms: subscription.armed_at_epoch_ms,
                }
            })
            .collect(),
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
    let active_tokens = snapshot
        .active_tokens
        .into_iter()
        .map(|token| {
            if token.count == 0 {
                return Err(SnapshotCodecError::InvalidTokenState);
            }
            Ok((
                identifier(NodeId::new, token.node_id, "active_token.node_id")?,
                token.count,
            ))
        })
        .collect::<Result<_, _>>()?;
    let pending_gateway_joins = snapshot
        .pending_gateway_joins
        .into_iter()
        .map(|join| {
            if join.expected_tokens == 0 || join.arrived_tokens >= join.expected_tokens {
                return Err(SnapshotCodecError::InvalidTokenState);
            }
            Ok((
                identifier(NodeId::new, join.gateway_id, "pending_join.gateway_id")?,
                PendingGatewayJoin {
                    expected_tokens: join.expected_tokens,
                    arrived_tokens: join.arrived_tokens,
                },
            ))
        })
        .collect::<Result<_, _>>()?;
    let active_multi_instances = snapshot
        .active_multi_instances
        .into_iter()
        .map(active_multi_instance_from_wire)
        .collect::<Result<_, _>>()?;
    let active_boundary_subscriptions = snapshot
        .active_boundary_subscriptions
        .into_iter()
        .map(active_boundary_subscription_from_wire)
        .collect::<Result<_, _>>()?;
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
            variables: snapshot
                .variables
                .into_iter()
                .map(workflow_variable_from_wire)
                .collect::<Result<_, _>>()?,
            active_tokens,
            pending_gateway_joins,
            active_multi_instances,
            active_boundary_subscriptions,
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

fn workflow_value_to_wire(value: &WorkflowValue) -> wire::workflow_variable::Value {
    match value {
        WorkflowValue::Boolean(value) => wire::workflow_variable::Value::BooleanValue(*value),
        WorkflowValue::Integer(value) => wire::workflow_variable::Value::IntegerValue(*value),
        WorkflowValue::String(value) => wire::workflow_variable::Value::StringValue(value.clone()),
        WorkflowValue::List(items) => {
            wire::workflow_variable::Value::ListValue(workflow_value_list_to_wire(items))
        }
    }
}

fn workflow_variable_from_wire(
    variable: wire::WorkflowVariable,
) -> Result<(String, WorkflowValue), SnapshotCodecError> {
    let name = non_empty(variable.name, "workflow_variable.name")?;
    let value = match variable
        .value
        .ok_or(SnapshotCodecError::MissingWorkflowVariableValue)?
    {
        wire::workflow_variable::Value::BooleanValue(value) => WorkflowValue::Boolean(value),
        wire::workflow_variable::Value::IntegerValue(value) => WorkflowValue::Integer(value),
        wire::workflow_variable::Value::StringValue(value) => WorkflowValue::String(value),
        wire::workflow_variable::Value::ListValue(value) => workflow_value_list_from_wire(value)?,
    };
    Ok((name, value))
}

fn active_multi_instance_from_wire(
    active: wire::ActiveMultiInstance,
) -> Result<(NodeId, ActiveMultiInstance), SnapshotCodecError> {
    if active.total_instances == 0
        || active.max_parallelism == 0
        || active.next_iteration > active.total_instances
        || active.max_parallelism > active.total_instances
    {
        return Err(SnapshotCodecError::InvalidMultiInstanceState);
    }
    let active_iterations = active.active_iterations.into_iter().collect();
    let completed_iterations = active.completed_iterations.into_iter().collect();
    let decoded = ActiveMultiInstance {
        task_type: identifier(TaskType::new, active.task_type, "multi_instance.task_type")?,
        mode: multi_instance_mode_from_wire(active.mode)?,
        total_instances: active.total_instances,
        next_iteration: active.next_iteration,
        max_parallelism: active.max_parallelism,
        item_variable: optional_non_empty(active.item_variable),
        items: active
            .items
            .into_iter()
            .map(workflow_value_item_from_wire)
            .collect::<Result<_, _>>()?,
        active_iterations,
        completed_iterations,
    };
    validate_active_multi_instance(&decoded)?;
    Ok((
        identifier(NodeId::new, active.node_id, "multi_instance.node_id")?,
        decoded,
    ))
}

fn validate_active_multi_instance(active: &ActiveMultiInstance) -> Result<(), SnapshotCodecError> {
    let limit = active.total_instances;
    if active.active_iterations.len() > active.max_parallelism as usize
        || active
            .active_iterations
            .iter()
            .chain(&active.completed_iterations)
            .any(|iteration| *iteration >= limit || *iteration >= active.next_iteration)
        || !active
            .active_iterations
            .is_disjoint(&active.completed_iterations)
        || (!active.items.is_empty() && active.items.len() != limit as usize)
    {
        return Err(SnapshotCodecError::InvalidMultiInstanceState);
    }
    Ok(())
}

fn active_boundary_subscription_from_wire(
    subscription: wire::ActiveBoundarySubscription,
) -> Result<(NodeId, ActiveBoundarySubscription), SnapshotCodecError> {
    Ok((
        identifier(
            NodeId::new,
            subscription.boundary_event_id,
            "boundary_subscription.boundary_event_id",
        )?,
        ActiveBoundarySubscription {
            attached_node_id: identifier(
                NodeId::new,
                subscription.attached_node_id,
                "boundary_subscription.attached_node_id",
            )?,
            target_node_id: identifier(
                NodeId::new,
                subscription.target_node_id,
                "boundary_subscription.target_node_id",
            )?,
            cancel_activity: subscription.cancel_activity,
            trigger: boundary_trigger_from_wire(
                subscription.trigger_kind,
                subscription.trigger_reference,
            )?,
            armed_at_epoch_ms: subscription.armed_at_epoch_ms,
        },
    ))
}

fn workflow_value_list_to_wire(items: &[WorkflowValue]) -> wire::WorkflowValueList {
    wire::WorkflowValueList {
        items: items.iter().map(workflow_value_item_to_wire).collect(),
    }
}

fn workflow_value_item_to_wire(value: &WorkflowValue) -> wire::WorkflowValueItem {
    use wire::workflow_value_item::Value;
    let value = match value {
        WorkflowValue::Boolean(value) => Value::BooleanValue(*value),
        WorkflowValue::Integer(value) => Value::IntegerValue(*value),
        WorkflowValue::String(value) => Value::StringValue(value.clone()),
        WorkflowValue::List(items) => Value::ListValue(workflow_value_list_to_wire(items)),
    };
    wire::WorkflowValueItem { value: Some(value) }
}

fn workflow_value_list_from_wire(
    list: wire::WorkflowValueList,
) -> Result<WorkflowValue, SnapshotCodecError> {
    Ok(WorkflowValue::List(
        list.items
            .into_iter()
            .map(workflow_value_item_from_wire)
            .collect::<Result<_, _>>()?,
    ))
}

fn workflow_value_item_from_wire(
    item: wire::WorkflowValueItem,
) -> Result<WorkflowValue, SnapshotCodecError> {
    use wire::workflow_value_item::Value;
    match item
        .value
        .ok_or(SnapshotCodecError::MissingWorkflowVariableValue)?
    {
        Value::BooleanValue(value) => Ok(WorkflowValue::Boolean(value)),
        Value::IntegerValue(value) => Ok(WorkflowValue::Integer(value)),
        Value::StringValue(value) => Ok(WorkflowValue::String(value)),
        Value::ListValue(value) => workflow_value_list_from_wire(value),
    }
}

const fn multi_instance_mode_to_wire(mode: MultiInstanceMode) -> wire::MultiInstanceMode {
    match mode {
        MultiInstanceMode::Sequential => wire::MultiInstanceMode::Sequential,
        MultiInstanceMode::Parallel => wire::MultiInstanceMode::Parallel,
    }
}

fn multi_instance_mode_from_wire(value: i32) -> Result<MultiInstanceMode, SnapshotCodecError> {
    match wire::MultiInstanceMode::try_from(value) {
        Ok(wire::MultiInstanceMode::Sequential) => Ok(MultiInstanceMode::Sequential),
        Ok(wire::MultiInstanceMode::Parallel) => Ok(MultiInstanceMode::Parallel),
        _ => Err(SnapshotCodecError::InvalidMultiInstanceMode(value)),
    }
}

fn boundary_trigger_to_wire(trigger: &BoundaryTrigger) -> (wire::BoundaryTriggerKind, String) {
    match trigger {
        BoundaryTrigger::Timer { kind, expression } => (
            match kind {
                BoundaryTimerKind::Date => wire::BoundaryTriggerKind::TimerDate,
                BoundaryTimerKind::Duration => wire::BoundaryTriggerKind::TimerDuration,
                BoundaryTimerKind::Cycle => wire::BoundaryTriggerKind::TimerCycle,
            },
            expression.clone(),
        ),
        BoundaryTrigger::Error { error_ref } => (
            wire::BoundaryTriggerKind::Error,
            error_ref.clone().unwrap_or_default(),
        ),
        BoundaryTrigger::Message { message_ref } => {
            (wire::BoundaryTriggerKind::Message, message_ref.clone())
        }
    }
}

fn boundary_trigger_from_wire(
    kind: i32,
    reference: String,
) -> Result<BoundaryTrigger, SnapshotCodecError> {
    match wire::BoundaryTriggerKind::try_from(kind) {
        Ok(wire::BoundaryTriggerKind::TimerDate) => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Date,
            expression: non_empty(reference, "boundary_subscription.trigger_reference")?,
        }),
        Ok(wire::BoundaryTriggerKind::TimerDuration) => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Duration,
            expression: non_empty(reference, "boundary_subscription.trigger_reference")?,
        }),
        Ok(wire::BoundaryTriggerKind::TimerCycle) => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Cycle,
            expression: non_empty(reference, "boundary_subscription.trigger_reference")?,
        }),
        Ok(wire::BoundaryTriggerKind::Error) => Ok(BoundaryTrigger::Error {
            error_ref: optional_non_empty(reference),
        }),
        Ok(wire::BoundaryTriggerKind::Message) => Ok(BoundaryTrigger::Message {
            message_ref: non_empty(reference, "boundary_subscription.trigger_reference")?,
        }),
        _ => Err(SnapshotCodecError::InvalidBoundaryTriggerKind(kind)),
    }
}

fn optional_non_empty(value: String) -> Option<String> {
    (!value.trim().is_empty()).then_some(value)
}

fn non_empty(value: String, field: &'static str) -> Result<String, SnapshotCodecError> {
    if value.trim().is_empty() {
        Err(SnapshotCodecError::EmptyField(field))
    } else {
        Ok(value)
    }
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
    #[error("snapshot field {0} must not be empty")]
    EmptyField(&'static str),
    #[error("workflow variable is missing a typed value")]
    MissingWorkflowVariableValue,
    #[error("snapshot token/join state is invalid")]
    InvalidTokenState,
    #[error("snapshot contains unknown multi-instance mode {0}")]
    InvalidMultiInstanceMode(i32),
    #[error("snapshot multi-instance state is invalid")]
    InvalidMultiInstanceState,
    #[error("snapshot contains unknown boundary trigger kind {0}")]
    InvalidBoundaryTriggerKind(i32),
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
                variables: [(
                    "recipients".into(),
                    WorkflowValue::List(vec![WorkflowValue::String("a".into())]),
                )]
                .into(),
                active_tokens: std::collections::BTreeMap::default(),
                pending_gateway_joins: std::collections::BTreeMap::default(),
                active_multi_instances: [(
                    NodeId::new("charge").unwrap(),
                    ActiveMultiInstance {
                        task_type: TaskType::new("charge-item").unwrap(),
                        mode: MultiInstanceMode::Parallel,
                        total_instances: 3,
                        next_iteration: 2,
                        max_parallelism: 2,
                        item_variable: Some("recipient".into()),
                        items: vec![
                            WorkflowValue::String("a".into()),
                            WorkflowValue::String("b".into()),
                            WorkflowValue::String("c".into()),
                        ],
                        active_iterations: std::collections::BTreeSet::from([1]),
                        completed_iterations: std::collections::BTreeSet::from([0]),
                    },
                )]
                .into(),
                active_boundary_subscriptions: [(
                    NodeId::new("timeout").unwrap(),
                    ActiveBoundarySubscription {
                        attached_node_id: NodeId::new("charge").unwrap(),
                        target_node_id: NodeId::new("recovery").unwrap(),
                        cancel_activity: true,
                        trigger: BoundaryTrigger::Timer {
                            kind: BoundaryTimerKind::Duration,
                            expression: "PT5M".into(),
                        },
                        armed_at_epoch_ms: 99,
                    },
                )]
                .into(),
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
