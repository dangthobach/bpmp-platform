use bpmp_contracts::engine::v1 as wire;
use bpmp_domain_core::{
    ActorId, CommandId, ConfigVersion, CorrelationId, DomainEvent, IdentifierError, InstanceId,
    KeyScope, NodeId, PolicyVersion, TaskType, TenantId, WorkflowType, WorkflowValue,
    WorkflowVersion,
};
use prost::Message;
use thiserror::Error;

use crate::{EVENT_SCHEMA_VERSION, EventEnvelope, EventMetadata};

pub struct EventCodec;

impl EventCodec {
    pub fn encode(event: &EventEnvelope) -> Vec<u8> {
        to_wire(event).encode_to_vec()
    }

    /// Decodes the durable event contract into validated domain types.
    ///
    /// # Errors
    ///
    /// Returns [`EventCodecError`] for malformed bytes, missing required messages,
    /// unsupported schema versions, or invalid identifiers.
    pub fn decode(bytes: &[u8]) -> Result<EventEnvelope, EventCodecError> {
        let envelope = wire::EventEnvelope::decode(bytes)
            .map_err(|error| EventCodecError::Decode(error.to_string()))?;
        from_wire(envelope)
    }
}

fn to_wire(envelope: &EventEnvelope) -> wire::EventEnvelope {
    let metadata = &envelope.metadata;
    let event = match &envelope.event {
        DomainEvent::WorkflowStarted {
            tenant_id,
            workflow_type,
            workflow_version,
            start_node_id,
            ..
        } => wire::event_envelope::Event::WorkflowStarted(wire::WorkflowStarted {
            tenant_id: tenant_id.to_string(),
            workflow_type: workflow_type.to_string(),
            workflow_version: workflow_version.to_string(),
            start_node_id: start_node_id.to_string(),
        }),
        DomainEvent::ServiceTaskActivated {
            node_id, task_type, ..
        } => wire::event_envelope::Event::ServiceTaskActivated(wire::ServiceTaskActivated {
            node_id: node_id.to_string(),
            task_type: task_type.to_string(),
        }),
        DomainEvent::ServiceTaskCompleted { node_id, .. } => {
            wire::event_envelope::Event::ServiceTaskCompleted(wire::ServiceTaskCompleted {
                node_id: node_id.to_string(),
            })
        }
        DomainEvent::DecisionTaskEvaluated {
            node_id,
            decision_table_id,
            outputs,
            ..
        } => wire::event_envelope::Event::DecisionTaskEvaluated(wire::DecisionTaskEvaluated {
            node_id: node_id.to_string(),
            decision_table_id: decision_table_id.clone(),
            outputs: outputs
                .iter()
                .map(|(name, value)| wire::WorkflowVariable {
                    name: name.clone(),
                    value: Some(workflow_value_to_wire(value)),
                })
                .collect(),
        }),
        DomainEvent::WorkflowCompleted { .. } => {
            wire::event_envelope::Event::WorkflowCompleted(wire::WorkflowCompleted {})
        }
    };
    wire::EventEnvelope {
        metadata: Some(wire::EventMetadata {
            event_id: metadata.event_id.clone(),
            tenant_id: metadata.tenant_id.to_string(),
            instance_id: metadata.instance_id.to_string(),
            sequence: metadata.sequence,
            schema_version: metadata.schema_version,
            correlation_id: metadata.correlation_id.to_string(),
            causation_command_id: metadata.causation_command_id.to_string(),
            occurred_at_epoch_ms: metadata.occurred_at_epoch_ms,
            config_version: metadata.config_version.to_string(),
            policy_version: metadata.policy_version.to_string(),
            actor_id: metadata.actor_id.to_string(),
            encryption_key_scope: metadata.encryption_key_scope.to_string(),
        }),
        event: Some(event),
    }
}

fn from_wire(envelope: wire::EventEnvelope) -> Result<EventEnvelope, EventCodecError> {
    let metadata = envelope.metadata.ok_or(EventCodecError::MissingMetadata)?;
    if metadata.schema_version != EVENT_SCHEMA_VERSION {
        return Err(EventCodecError::UnsupportedSchema(metadata.schema_version));
    }
    let occurred_at_epoch_ms = metadata.occurred_at_epoch_ms;
    let metadata_tenant_id = identifier(TenantId::new, metadata.tenant_id.clone(), "tenant_id")?;
    let event = match envelope.event.ok_or(EventCodecError::MissingEvent)? {
        wire::event_envelope::Event::WorkflowStarted(started) => DomainEvent::WorkflowStarted {
            tenant_id: identifier(
                TenantId::new,
                started.tenant_id,
                "workflow_started.tenant_id",
            )?,
            workflow_type: identifier(WorkflowType::new, started.workflow_type, "workflow_type")?,
            workflow_version: identifier(
                WorkflowVersion::new,
                started.workflow_version,
                "workflow_version",
            )?,
            start_node_id: identifier(NodeId::new, started.start_node_id, "start_node_id")?,
            occurred_at_epoch_ms,
        },
        wire::event_envelope::Event::ServiceTaskActivated(activated) => {
            DomainEvent::ServiceTaskActivated {
                node_id: identifier(NodeId::new, activated.node_id, "node_id")?,
                task_type: identifier(TaskType::new, activated.task_type, "task_type")?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::ServiceTaskCompleted(completed) => {
            DomainEvent::ServiceTaskCompleted {
                node_id: identifier(NodeId::new, completed.node_id, "node_id")?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::DecisionTaskEvaluated(evaluated) => {
            DomainEvent::DecisionTaskEvaluated {
                node_id: identifier(NodeId::new, evaluated.node_id, "node_id")?,
                decision_table_id: non_empty(evaluated.decision_table_id, "decision_table_id")?,
                outputs: evaluated
                    .outputs
                    .into_iter()
                    .map(workflow_variable_from_wire)
                    .collect::<Result<_, _>>()?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::WorkflowCompleted(_) => DomainEvent::WorkflowCompleted {
            occurred_at_epoch_ms,
        },
    };
    if let DomainEvent::WorkflowStarted { tenant_id, .. } = &event
        && tenant_id != &metadata_tenant_id
    {
        return Err(EventCodecError::TenantMismatch);
    }
    Ok(EventEnvelope {
        metadata: EventMetadata {
            event_id: metadata.event_id,
            tenant_id: metadata_tenant_id,
            instance_id: identifier(InstanceId::new, metadata.instance_id, "instance_id")?,
            sequence: metadata.sequence,
            schema_version: metadata.schema_version,
            correlation_id: identifier(
                CorrelationId::new,
                metadata.correlation_id,
                "correlation_id",
            )?,
            causation_command_id: identifier(
                CommandId::new,
                metadata.causation_command_id,
                "causation_command_id",
            )?,
            occurred_at_epoch_ms,
            config_version: identifier(
                ConfigVersion::new,
                metadata.config_version,
                "config_version",
            )?,
            policy_version: identifier(
                PolicyVersion::new,
                metadata.policy_version,
                "policy_version",
            )?,
            actor_id: identifier(ActorId::new, metadata.actor_id, "actor_id")?,
            encryption_key_scope: identifier(
                KeyScope::new,
                metadata.encryption_key_scope,
                "encryption_key_scope",
            )?,
        },
        event,
    })
}

fn workflow_value_to_wire(value: &WorkflowValue) -> wire::workflow_variable::Value {
    match value {
        WorkflowValue::Boolean(value) => wire::workflow_variable::Value::BooleanValue(*value),
        WorkflowValue::Integer(value) => wire::workflow_variable::Value::IntegerValue(*value),
        WorkflowValue::String(value) => wire::workflow_variable::Value::StringValue(value.clone()),
    }
}

fn workflow_variable_from_wire(
    variable: wire::WorkflowVariable,
) -> Result<(String, WorkflowValue), EventCodecError> {
    let name = non_empty(variable.name, "workflow_variable.name")?;
    let value = match variable
        .value
        .ok_or(EventCodecError::MissingWorkflowVariableValue)?
    {
        wire::workflow_variable::Value::BooleanValue(value) => WorkflowValue::Boolean(value),
        wire::workflow_variable::Value::IntegerValue(value) => WorkflowValue::Integer(value),
        wire::workflow_variable::Value::StringValue(value) => WorkflowValue::String(value),
    };
    Ok((name, value))
}

fn non_empty(value: String, field: &'static str) -> Result<String, EventCodecError> {
    if value.trim().is_empty() {
        Err(EventCodecError::EmptyField(field))
    } else {
        Ok(value)
    }
}

fn identifier<T>(
    constructor: impl FnOnce(String) -> Result<T, IdentifierError>,
    value: String,
    field: &'static str,
) -> Result<T, EventCodecError> {
    constructor(value).map_err(|source| EventCodecError::Identifier { field, source })
}

#[derive(Debug, Error)]
pub enum EventCodecError {
    #[error("event bytes cannot be decoded: {0}")]
    Decode(String),
    #[error("event envelope is missing metadata")]
    MissingMetadata,
    #[error("event envelope is missing its typed event")]
    MissingEvent,
    #[error("workflow started tenant does not match event metadata tenant")]
    TenantMismatch,
    #[error("event field {0} must not be empty")]
    EmptyField(&'static str),
    #[error("workflow variable is missing a typed value")]
    MissingWorkflowVariableValue,
    #[error("unsupported event schema version {0}")]
    UnsupportedSchema(u32),
    #[error("invalid event identifier in field {field}: {source}")]
    Identifier {
        field: &'static str,
        source: IdentifierError,
    },
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durable_event_round_trips_with_key_and_configuration_versions() {
        let expected = EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 123,
                config_version: ConfigVersion::new("config-7").unwrap(),
                policy_version: PolicyVersion::new("policy-3").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            },
            event: DomainEvent::ServiceTaskActivated {
                node_id: NodeId::new("charge").unwrap(),
                task_type: TaskType::new("payment").unwrap(),
                occurred_at_epoch_ms: 123,
            },
        };

        assert_eq!(
            EventCodec::decode(&EventCodec::encode(&expected)).unwrap(),
            expected
        );
    }

    #[test]
    fn workflow_started_event_carries_tenant_in_payload_and_metadata() {
        let expected = EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 123,
                config_version: ConfigVersion::new("config-7").unwrap(),
                policy_version: PolicyVersion::new("policy-3").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            },
            event: DomainEvent::WorkflowStarted {
                tenant_id: TenantId::new("tenant-a").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
                start_node_id: NodeId::new("start").unwrap(),
                occurred_at_epoch_ms: 123,
            },
        };

        let encoded = EventCodec::encode(&expected);
        assert_eq!(EventCodec::decode(&encoded).unwrap(), expected);
    }

    #[test]
    fn decision_task_evaluated_event_round_trips_outputs() {
        let expected = EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-1".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 1,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 123,
                config_version: ConfigVersion::new("config-7").unwrap(),
                policy_version: PolicyVersion::new("policy-3").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            },
            event: DomainEvent::DecisionTaskEvaluated {
                node_id: NodeId::new("risk").unwrap(),
                decision_table_id: "risk-table".into(),
                outputs: [("approved".into(), WorkflowValue::Boolean(true))].into(),
                occurred_at_epoch_ms: 123,
            },
        };

        let encoded = EventCodec::encode(&expected);
        assert_eq!(EventCodec::decode(&encoded).unwrap(), expected);
    }
}
