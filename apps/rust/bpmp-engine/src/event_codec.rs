use bpmp_contracts::engine::v1 as wire;
use bpmp_domain_core::{
    ActorId, BoundaryTimerKind, BoundaryTrigger, CommandId, ConfigVersion, CorrelationId,
    DomainEvent, IdentifierError, InstanceId, KeyScope, MultiInstanceMode, NodeId, PolicyVersion,
    ScopeInstanceId, TaskType, TenantId, WorkflowType, WorkflowValue, WorkflowVersion,
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

#[allow(clippy::too_many_lines)]
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
        DomainEvent::UserTaskActivated {
            node_id,
            task_type,
            assignment_policy_ref,
            form_key,
            ..
        } => wire::event_envelope::Event::UserTaskActivated(wire::UserTaskActivated {
            node_id: node_id.to_string(),
            task_type: task_type.to_string(),
            assignment_policy_ref: assignment_policy_ref.clone(),
            form_key: form_key.clone().unwrap_or_default(),
        }),
        DomainEvent::UserTaskCompleted {
            node_id,
            decision,
            result_variable,
            ..
        } => wire::event_envelope::Event::UserTaskCompleted(wire::UserTaskCompleted {
            node_id: node_id.to_string(),
            decision: decision.clone(),
            result_variable: result_variable.clone(),
        }),
        DomainEvent::ScriptTaskActivated {
            node_id,
            task_type,
            implementation_ref,
            implementation_version,
            ..
        } => wire::event_envelope::Event::ScriptTaskActivated(wire::ScriptTaskActivated {
            node_id: node_id.to_string(),
            task_type: task_type.to_string(),
            implementation_ref: implementation_ref.clone(),
            implementation_version: implementation_version.clone(),
        }),
        DomainEvent::ScriptTaskCompleted { node_id, .. } => {
            wire::event_envelope::Event::ScriptTaskCompleted(wire::ScriptTaskCompleted {
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
        DomainEvent::GatewaySplitActivated {
            gateway_id,
            join_gateway_id,
            selected_targets,
            ..
        } => wire::event_envelope::Event::GatewaySplitActivated(wire::GatewaySplitActivated {
            gateway_id: gateway_id.to_string(),
            join_gateway_id: join_gateway_id.to_string(),
            selected_target_node_ids: selected_targets.iter().map(ToString::to_string).collect(),
        }),
        DomainEvent::GatewayTokenArrived { gateway_id, .. } => {
            wire::event_envelope::Event::GatewayTokenArrived(wire::GatewayTokenArrived {
                gateway_id: gateway_id.to_string(),
            })
        }
        DomainEvent::GatewayJoined { gateway_id, .. } => {
            wire::event_envelope::Event::GatewayJoined(wire::GatewayJoined {
                gateway_id: gateway_id.to_string(),
            })
        }
        DomainEvent::BoundaryEventArmed {
            boundary_event_id,
            attached_node_id,
            target_node_id,
            cancel_activity,
            trigger,
            ..
        } => {
            let (trigger_kind, trigger_reference) = boundary_trigger_to_wire(trigger);
            wire::event_envelope::Event::BoundaryEventArmed(wire::BoundaryEventArmed {
                boundary_event_id: boundary_event_id.to_string(),
                attached_node_id: attached_node_id.to_string(),
                target_node_id: target_node_id.to_string(),
                cancel_activity: *cancel_activity,
                trigger_kind: trigger_kind.into(),
                trigger_reference,
            })
        }
        DomainEvent::BoundaryEventsDisarmed {
            attached_node_id,
            boundary_event_ids,
            ..
        } => wire::event_envelope::Event::BoundaryEventsDisarmed(wire::BoundaryEventsDisarmed {
            attached_node_id: attached_node_id.to_string(),
            boundary_event_ids: boundary_event_ids.iter().map(ToString::to_string).collect(),
        }),
        DomainEvent::MultiInstanceStarted {
            node_id,
            task_type,
            mode,
            total_instances,
            max_parallelism,
            item_variable,
            items,
            ..
        } => wire::event_envelope::Event::MultiInstanceStarted(wire::MultiInstanceStarted {
            node_id: node_id.to_string(),
            task_type: task_type.to_string(),
            mode: multi_instance_mode_to_wire(*mode).into(),
            total_instances: *total_instances,
            max_parallelism: *max_parallelism,
            item_variable: item_variable.clone().unwrap_or_default(),
            items: items.iter().map(workflow_value_item_to_wire).collect(),
        }),
        DomainEvent::MultiInstanceIterationActivated {
            node_id,
            task_type,
            iteration,
            item,
            ..
        } => wire::event_envelope::Event::MultiInstanceIterationActivated(
            wire::MultiInstanceIterationActivated {
                node_id: node_id.to_string(),
                task_type: task_type.to_string(),
                iteration: *iteration,
                item: item.as_ref().map(workflow_value_item_to_wire),
            },
        ),
        DomainEvent::MultiInstanceIterationCompleted {
            node_id, iteration, ..
        } => wire::event_envelope::Event::MultiInstanceIterationCompleted(
            wire::MultiInstanceIterationCompleted {
                node_id: node_id.to_string(),
                iteration: *iteration,
            },
        ),
        DomainEvent::MultiInstanceCompleted {
            node_id,
            completion_condition_satisfied,
            cancelled_iterations,
            ..
        } => wire::event_envelope::Event::MultiInstanceCompleted(wire::MultiInstanceCompleted {
            node_id: node_id.to_string(),
            completion_condition_satisfied: *completion_condition_satisfied,
            cancelled_iterations: cancelled_iterations.clone(),
        }),
        DomainEvent::BoundaryEventTriggered {
            boundary_event_id,
            attached_node_id,
            target_node_id,
            cancel_activity,
            cancelled_iterations,
            cancelled_task_tokens,
            ..
        } => wire::event_envelope::Event::BoundaryEventTriggered(wire::BoundaryEventTriggered {
            boundary_event_id: boundary_event_id.to_string(),
            attached_node_id: attached_node_id.to_string(),
            target_node_id: target_node_id.to_string(),
            cancel_activity: *cancel_activity,
            cancelled_iterations: cancelled_iterations.clone(),
            cancelled_task_tokens: *cancelled_task_tokens,
        }),
        DomainEvent::ScopeEntered {
            scope_instance_id,
            scope_node_id,
            start_node_id,
            parent_scope_instance_id,
            invocation,
            ..
        } => wire::event_envelope::Event::ScopeEntered(wire::ScopeEntered {
            scope_instance_id: scope_instance_id.to_string(),
            scope_node_id: scope_node_id.to_string(),
            start_node_id: start_node_id.to_string(),
            parent_scope_instance_id: parent_scope_instance_id.as_ref().map(ToString::to_string),
            invocation: *invocation,
        }),
        DomainEvent::ScopeCompleted {
            scope_instance_id,
            scope_node_id,
            end_node_id,
            ..
        } => wire::event_envelope::Event::ScopeCompleted(wire::ScopeCompleted {
            scope_instance_id: scope_instance_id.to_string(),
            scope_node_id: scope_node_id.to_string(),
            end_node_id: end_node_id.to_string(),
        }),
        DomainEvent::WorkflowBranchCompleted { end_node_id, .. } => {
            wire::event_envelope::Event::WorkflowBranchCompleted(wire::WorkflowBranchCompleted {
                end_node_id: end_node_id.to_string(),
            })
        }
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
            workflow_type: metadata.workflow_type.to_string(),
            workflow_version: metadata.workflow_version.to_string(),
        }),
        event: Some(event),
    }
}

#[allow(clippy::too_many_lines)]
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
        wire::event_envelope::Event::UserTaskActivated(activated) => {
            DomainEvent::UserTaskActivated {
                node_id: identifier(NodeId::new, activated.node_id, "node_id")?,
                task_type: identifier(TaskType::new, activated.task_type, "task_type")?,
                assignment_policy_ref: non_empty(
                    activated.assignment_policy_ref,
                    "assignment_policy_ref",
                )?,
                form_key: optional_non_empty(activated.form_key),
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::UserTaskCompleted(completed) => {
            DomainEvent::UserTaskCompleted {
                node_id: identifier(NodeId::new, completed.node_id, "node_id")?,
                decision: non_empty(completed.decision, "decision")?,
                result_variable: non_empty(completed.result_variable, "result_variable")?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::ScriptTaskActivated(activated) => {
            DomainEvent::ScriptTaskActivated {
                node_id: identifier(NodeId::new, activated.node_id, "node_id")?,
                task_type: identifier(TaskType::new, activated.task_type, "task_type")?,
                implementation_ref: non_empty(activated.implementation_ref, "implementation_ref")?,
                implementation_version: non_empty(
                    activated.implementation_version,
                    "implementation_version",
                )?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::ScriptTaskCompleted(completed) => {
            DomainEvent::ScriptTaskCompleted {
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
        wire::event_envelope::Event::GatewaySplitActivated(activated) => {
            DomainEvent::GatewaySplitActivated {
                gateway_id: identifier(NodeId::new, activated.gateway_id, "gateway_id")?,
                join_gateway_id: identifier(
                    NodeId::new,
                    activated.join_gateway_id,
                    "join_gateway_id",
                )?,
                selected_targets: activated
                    .selected_target_node_ids
                    .into_iter()
                    .map(|target| identifier(NodeId::new, target, "selected_target_node_id"))
                    .collect::<Result<_, _>>()?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::GatewayTokenArrived(arrived) => {
            DomainEvent::GatewayTokenArrived {
                gateway_id: identifier(NodeId::new, arrived.gateway_id, "gateway_id")?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::GatewayJoined(joined) => DomainEvent::GatewayJoined {
            gateway_id: identifier(NodeId::new, joined.gateway_id, "gateway_id")?,
            occurred_at_epoch_ms,
        },
        wire::event_envelope::Event::BoundaryEventArmed(armed) => DomainEvent::BoundaryEventArmed {
            boundary_event_id: identifier(
                NodeId::new,
                armed.boundary_event_id,
                "boundary_event_id",
            )?,
            attached_node_id: identifier(NodeId::new, armed.attached_node_id, "attached_node_id")?,
            target_node_id: identifier(NodeId::new, armed.target_node_id, "target_node_id")?,
            cancel_activity: armed.cancel_activity,
            trigger: boundary_trigger_from_wire(armed.trigger_kind, armed.trigger_reference)?,
            occurred_at_epoch_ms,
        },
        wire::event_envelope::Event::BoundaryEventsDisarmed(disarmed) => {
            DomainEvent::BoundaryEventsDisarmed {
                attached_node_id: identifier(
                    NodeId::new,
                    disarmed.attached_node_id,
                    "attached_node_id",
                )?,
                boundary_event_ids: disarmed
                    .boundary_event_ids
                    .into_iter()
                    .map(|id| identifier(NodeId::new, id, "boundary_event_id"))
                    .collect::<Result<_, _>>()?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::MultiInstanceStarted(started) => {
            DomainEvent::MultiInstanceStarted {
                node_id: identifier(NodeId::new, started.node_id, "node_id")?,
                task_type: identifier(TaskType::new, started.task_type, "task_type")?,
                mode: multi_instance_mode_from_wire(started.mode)?,
                total_instances: started.total_instances,
                max_parallelism: positive(started.max_parallelism, "max_parallelism")?,
                item_variable: optional_non_empty(started.item_variable),
                items: started
                    .items
                    .into_iter()
                    .map(workflow_value_item_from_wire)
                    .collect::<Result<_, _>>()?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::MultiInstanceIterationActivated(activated) => {
            DomainEvent::MultiInstanceIterationActivated {
                node_id: identifier(NodeId::new, activated.node_id, "node_id")?,
                task_type: identifier(TaskType::new, activated.task_type, "task_type")?,
                iteration: activated.iteration,
                item: activated
                    .item
                    .map(workflow_value_item_from_wire)
                    .transpose()?,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::MultiInstanceIterationCompleted(completed) => {
            DomainEvent::MultiInstanceIterationCompleted {
                node_id: identifier(NodeId::new, completed.node_id, "node_id")?,
                iteration: completed.iteration,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::MultiInstanceCompleted(completed) => {
            DomainEvent::MultiInstanceCompleted {
                node_id: identifier(NodeId::new, completed.node_id, "node_id")?,
                completion_condition_satisfied: completed.completion_condition_satisfied,
                cancelled_iterations: completed.cancelled_iterations,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::BoundaryEventTriggered(triggered) => {
            DomainEvent::BoundaryEventTriggered {
                boundary_event_id: identifier(
                    NodeId::new,
                    triggered.boundary_event_id,
                    "boundary_event_id",
                )?,
                attached_node_id: identifier(
                    NodeId::new,
                    triggered.attached_node_id,
                    "attached_node_id",
                )?,
                target_node_id: identifier(
                    NodeId::new,
                    triggered.target_node_id,
                    "target_node_id",
                )?,
                cancel_activity: triggered.cancel_activity,
                cancelled_iterations: triggered.cancelled_iterations,
                cancelled_task_tokens: triggered.cancelled_task_tokens,
                occurred_at_epoch_ms,
            }
        }
        wire::event_envelope::Event::ScopeEntered(entered) => DomainEvent::ScopeEntered {
            scope_instance_id: identifier(
                ScopeInstanceId::new,
                entered.scope_instance_id,
                "scope_instance_id",
            )?,
            scope_node_id: identifier(NodeId::new, entered.scope_node_id, "scope_node_id")?,
            start_node_id: identifier(NodeId::new, entered.start_node_id, "start_node_id")?,
            parent_scope_instance_id: entered
                .parent_scope_instance_id
                .map(|value| identifier(ScopeInstanceId::new, value, "parent_scope_instance_id"))
                .transpose()?,
            invocation: positive_u64(entered.invocation, "scope_invocation")?,
            occurred_at_epoch_ms,
        },
        wire::event_envelope::Event::ScopeCompleted(completed) => DomainEvent::ScopeCompleted {
            scope_instance_id: identifier(
                ScopeInstanceId::new,
                completed.scope_instance_id,
                "scope_instance_id",
            )?,
            scope_node_id: identifier(NodeId::new, completed.scope_node_id, "scope_node_id")?,
            end_node_id: identifier(NodeId::new, completed.end_node_id, "end_node_id")?,
            occurred_at_epoch_ms,
        },
        wire::event_envelope::Event::WorkflowBranchCompleted(completed) => {
            DomainEvent::WorkflowBranchCompleted {
                end_node_id: identifier(NodeId::new, completed.end_node_id, "end_node_id")?,
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
            workflow_type: identifier(
                WorkflowType::new,
                metadata.workflow_type,
                "metadata.workflow_type",
            )?,
            workflow_version: identifier(
                WorkflowVersion::new,
                metadata.workflow_version,
                "metadata.workflow_version",
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
        WorkflowValue::List(items) => {
            wire::workflow_variable::Value::ListValue(workflow_value_list_to_wire(items))
        }
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
        wire::workflow_variable::Value::ListValue(value) => workflow_value_list_from_wire(value)?,
    };
    Ok((name, value))
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
) -> Result<WorkflowValue, EventCodecError> {
    Ok(WorkflowValue::List(
        list.items
            .into_iter()
            .map(workflow_value_item_from_wire)
            .collect::<Result<_, _>>()?,
    ))
}

fn workflow_value_item_from_wire(
    item: wire::WorkflowValueItem,
) -> Result<WorkflowValue, EventCodecError> {
    use wire::workflow_value_item::Value;
    match item
        .value
        .ok_or(EventCodecError::MissingWorkflowVariableValue)?
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

fn multi_instance_mode_from_wire(value: i32) -> Result<MultiInstanceMode, EventCodecError> {
    match wire::MultiInstanceMode::try_from(value) {
        Ok(wire::MultiInstanceMode::Sequential) => Ok(MultiInstanceMode::Sequential),
        Ok(wire::MultiInstanceMode::Parallel) => Ok(MultiInstanceMode::Parallel),
        _ => Err(EventCodecError::InvalidMultiInstanceMode(value)),
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
) -> Result<BoundaryTrigger, EventCodecError> {
    match wire::BoundaryTriggerKind::try_from(kind) {
        Ok(wire::BoundaryTriggerKind::TimerDate) => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Date,
            expression: non_empty(reference, "boundary_trigger.reference")?,
        }),
        Ok(wire::BoundaryTriggerKind::TimerDuration) => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Duration,
            expression: non_empty(reference, "boundary_trigger.reference")?,
        }),
        Ok(wire::BoundaryTriggerKind::TimerCycle) => Ok(BoundaryTrigger::Timer {
            kind: BoundaryTimerKind::Cycle,
            expression: non_empty(reference, "boundary_trigger.reference")?,
        }),
        Ok(wire::BoundaryTriggerKind::Error) => Ok(BoundaryTrigger::Error {
            error_ref: optional_non_empty(reference),
        }),
        Ok(wire::BoundaryTriggerKind::Message) => Ok(BoundaryTrigger::Message {
            message_ref: non_empty(reference, "boundary_trigger.reference")?,
        }),
        _ => Err(EventCodecError::InvalidBoundaryTriggerKind(kind)),
    }
}

fn optional_non_empty(value: String) -> Option<String> {
    (!value.trim().is_empty()).then_some(value)
}

fn positive(value: u32, field: &'static str) -> Result<u32, EventCodecError> {
    if value == 0 {
        Err(EventCodecError::InvalidPositiveField(field))
    } else {
        Ok(value)
    }
}

fn positive_u64(value: u64, field: &'static str) -> Result<u64, EventCodecError> {
    if value == 0 {
        Err(EventCodecError::InvalidPositiveField(field))
    } else {
        Ok(value)
    }
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
    #[error("event contains unknown multi-instance mode {0}")]
    InvalidMultiInstanceMode(i32),
    #[error("event field {0} must be greater than zero")]
    InvalidPositiveField(&'static str),
    #[error("event contains unknown boundary trigger kind {0}")]
    InvalidBoundaryTriggerKind(i32),
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
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
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
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
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
    fn user_and_script_task_events_round_trip_typed_execution_contracts() {
        let metadata = EventMetadata {
            event_id: "event-task".into(),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            sequence: 2,
            schema_version: EVENT_SCHEMA_VERSION,
            correlation_id: CorrelationId::new("correlation-1").unwrap(),
            causation_command_id: CommandId::new("command-1").unwrap(),
            occurred_at_epoch_ms: 123,
            config_version: ConfigVersion::new("config-7").unwrap(),
            policy_version: PolicyVersion::new("policy-3").unwrap(),
            actor_id: ActorId::new("actor-1").unwrap(),
            encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            workflow_type: WorkflowType::new("approval").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
        };
        let events = [
            DomainEvent::UserTaskActivated {
                node_id: NodeId::new("review").unwrap(),
                task_type: TaskType::new("review-request").unwrap(),
                assignment_policy_ref: "approval-reviewers".into(),
                form_key: Some("approval-form-v2".into()),
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::UserTaskCompleted {
                node_id: NodeId::new("review").unwrap(),
                decision: "approved".into(),
                result_variable: "review_result".into(),
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::ScriptTaskActivated {
                node_id: NodeId::new("calculate").unwrap(),
                task_type: TaskType::new("calculate-risk").unwrap(),
                implementation_ref: "wasm://risk/calculate".into(),
                implementation_version: "sha256:abc123".into(),
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::ScriptTaskCompleted {
                node_id: NodeId::new("calculate").unwrap(),
                occurred_at_epoch_ms: 123,
            },
        ];

        for event in events {
            let expected = EventEnvelope {
                metadata: metadata.clone(),
                event,
            };
            assert_eq!(
                EventCodec::decode(&EventCodec::encode(&expected)).unwrap(),
                expected
            );
        }
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
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
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

    #[test]
    fn gateway_split_event_round_trips_selected_token_obligation() {
        let expected = EventEnvelope {
            metadata: EventMetadata {
                event_id: "event-2".into(),
                tenant_id: TenantId::new("tenant-a").unwrap(),
                instance_id: InstanceId::new("instance-1").unwrap(),
                sequence: 2,
                schema_version: EVENT_SCHEMA_VERSION,
                correlation_id: CorrelationId::new("correlation-1").unwrap(),
                causation_command_id: CommandId::new("command-1").unwrap(),
                occurred_at_epoch_ms: 123,
                config_version: ConfigVersion::new("config-7").unwrap(),
                policy_version: PolicyVersion::new("policy-3").unwrap(),
                actor_id: ActorId::new("actor-1").unwrap(),
                encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
                workflow_type: WorkflowType::new("order").unwrap(),
                workflow_version: WorkflowVersion::new("1").unwrap(),
            },
            event: DomainEvent::GatewaySplitActivated {
                gateway_id: NodeId::new("fork").unwrap(),
                join_gateway_id: NodeId::new("join").unwrap(),
                selected_targets: vec![
                    NodeId::new("charge").unwrap(),
                    NodeId::new("reserve").unwrap(),
                ],
                occurred_at_epoch_ms: 123,
            },
        };

        assert_eq!(
            EventCodec::decode(&EventCodec::encode(&expected)).unwrap(),
            expected
        );
    }

    #[test]
    fn durable_multi_instance_and_boundary_events_round_trip() {
        let metadata = EventMetadata {
            event_id: "event-runtime".into(),
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-1").unwrap(),
            sequence: 8,
            schema_version: EVENT_SCHEMA_VERSION,
            correlation_id: CorrelationId::new("correlation-1").unwrap(),
            causation_command_id: CommandId::new("command-1").unwrap(),
            occurred_at_epoch_ms: 123,
            config_version: ConfigVersion::new("config-7").unwrap(),
            policy_version: PolicyVersion::new("policy-3").unwrap(),
            actor_id: ActorId::new("actor-1").unwrap(),
            encryption_key_scope: KeyScope::new("tenant-a/operational").unwrap(),
            workflow_type: WorkflowType::new("order").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
        };
        let node_id = NodeId::new("notify").unwrap();
        let events = vec![
            DomainEvent::BoundaryEventArmed {
                boundary_event_id: NodeId::new("timeout").unwrap(),
                attached_node_id: node_id.clone(),
                target_node_id: NodeId::new("recovery").unwrap(),
                cancel_activity: true,
                trigger: BoundaryTrigger::Timer {
                    kind: BoundaryTimerKind::Duration,
                    expression: "PT5M".into(),
                },
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::BoundaryEventsDisarmed {
                attached_node_id: node_id.clone(),
                boundary_event_ids: vec![NodeId::new("timeout").unwrap()],
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::MultiInstanceStarted {
                node_id: node_id.clone(),
                task_type: TaskType::new("notify-recipient").unwrap(),
                mode: MultiInstanceMode::Parallel,
                total_instances: 3,
                max_parallelism: 2,
                item_variable: Some("recipient".into()),
                items: vec![WorkflowValue::String("a".into())],
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::MultiInstanceIterationActivated {
                node_id: node_id.clone(),
                task_type: TaskType::new("notify-recipient").unwrap(),
                iteration: 0,
                item: Some(WorkflowValue::String("a".into())),
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::MultiInstanceIterationCompleted {
                node_id: node_id.clone(),
                iteration: 0,
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::MultiInstanceCompleted {
                node_id: node_id.clone(),
                completion_condition_satisfied: true,
                cancelled_iterations: vec![1, 2],
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::BoundaryEventTriggered {
                boundary_event_id: NodeId::new("timeout").unwrap(),
                attached_node_id: node_id,
                target_node_id: NodeId::new("recovery").unwrap(),
                cancel_activity: true,
                cancelled_iterations: vec![1, 2],
                cancelled_task_tokens: 0,
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::WorkflowBranchCompleted {
                end_node_id: NodeId::new("boundary-end").unwrap(),
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::ScopeEntered {
                scope_instance_id: ScopeInstanceId::new("review#1").unwrap(),
                scope_node_id: NodeId::new("review").unwrap(),
                start_node_id: NodeId::new("review-start").unwrap(),
                parent_scope_instance_id: None,
                invocation: 1,
                occurred_at_epoch_ms: 123,
            },
            DomainEvent::ScopeCompleted {
                scope_instance_id: ScopeInstanceId::new("review#1").unwrap(),
                scope_node_id: NodeId::new("review").unwrap(),
                end_node_id: NodeId::new("review-end").unwrap(),
                occurred_at_epoch_ms: 123,
            },
        ];

        for event in events {
            let expected = EventEnvelope {
                metadata: metadata.clone(),
                event,
            };
            assert_eq!(
                EventCodec::decode(&EventCodec::encode(&expected)).unwrap(),
                expected
            );
        }
    }
}
