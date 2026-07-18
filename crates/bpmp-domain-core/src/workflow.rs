use std::collections::{BTreeMap, BTreeSet, VecDeque};

use thiserror::Error;

use crate::{NodeId, ResolvedConfigSnapshot, TaskType, WorkflowType, WorkflowVersion};

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Node {
    Start { next: NodeId },
    ServiceTask { task_type: TaskType, next: NodeId },
    End,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct WorkflowDefinition {
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub start_node: NodeId,
    nodes: BTreeMap<NodeId, Node>,
}

impl WorkflowDefinition {
    /// Builds a workflow definition after structural and reachability validation.
    ///
    /// # Errors
    ///
    /// Returns a [`DomainError`] for duplicate, missing, invalid, or unreachable nodes.
    pub fn new(
        workflow_type: WorkflowType,
        workflow_version: WorkflowVersion,
        start_node: NodeId,
        nodes: impl IntoIterator<Item = (NodeId, Node)>,
    ) -> Result<Self, DomainError> {
        let mut indexed = BTreeMap::new();
        for (node_id, node) in nodes {
            if indexed.insert(node_id.clone(), node).is_some() {
                return Err(DomainError::DuplicateNode(node_id));
            }
        }

        if !matches!(indexed.get(&start_node), Some(Node::Start { .. })) {
            return Err(DomainError::InvalidStartNode(start_node));
        }

        for (node_id, node) in &indexed {
            if let Some(next) = node.next() {
                if !indexed.contains_key(next) {
                    return Err(DomainError::MissingTransitionTarget {
                        source_node: node_id.clone(),
                        target: next.clone(),
                    });
                }
                if matches!(indexed.get(next), Some(Node::Start { .. })) {
                    return Err(DomainError::TransitionToStartNode(next.clone()));
                }
            }
        }

        let definition = Self {
            workflow_type,
            workflow_version,
            start_node,
            nodes: indexed,
        };
        definition.validate_reachability()?;
        Ok(definition)
    }

    fn validate_reachability(&self) -> Result<(), DomainError> {
        let mut visited = BTreeSet::new();
        let mut pending = VecDeque::from([self.start_node.clone()]);
        while let Some(node_id) = pending.pop_front() {
            if !visited.insert(node_id.clone()) {
                continue;
            }
            if let Some(next) = self.nodes.get(&node_id).and_then(Node::next) {
                pending.push_back(next.clone());
            }
        }
        if let Some(unreachable) = self.nodes.keys().find(|id| !visited.contains(*id)) {
            return Err(DomainError::UnreachableNode(unreachable.clone()));
        }
        Ok(())
    }

    fn node(&self, node_id: &NodeId) -> Result<&Node, DomainError> {
        self.nodes
            .get(node_id)
            .ok_or_else(|| DomainError::UnknownNode(node_id.clone()))
    }
}

impl Node {
    fn next(&self) -> Option<&NodeId> {
        match self {
            Self::Start { next } | Self::ServiceTask { next, .. } => Some(next),
            Self::End => None,
        }
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Command {
    StartWorkflow {
        occurred_at_epoch_ms: u64,
    },
    CompleteServiceTask {
        node_id: NodeId,
        occurred_at_epoch_ms: u64,
    },
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum DomainEvent {
    WorkflowStarted {
        workflow_type: WorkflowType,
        workflow_version: WorkflowVersion,
        start_node_id: NodeId,
        occurred_at_epoch_ms: u64,
    },
    ServiceTaskActivated {
        node_id: NodeId,
        task_type: TaskType,
        occurred_at_epoch_ms: u64,
    },
    ServiceTaskCompleted {
        node_id: NodeId,
        occurred_at_epoch_ms: u64,
    },
    WorkflowCompleted {
        occurred_at_epoch_ms: u64,
    },
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Lifecycle {
    Initial,
    Active { active_node: NodeId },
    Completed,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct InstanceState {
    pub lifecycle: Lifecycle,
    pub sequence: u64,
}

impl Default for InstanceState {
    fn default() -> Self {
        Self {
            lifecycle: Lifecycle::Initial,
            sequence: 0,
        }
    }
}

#[derive(Debug, Clone, Copy)]
pub struct DecisionContext<'a> {
    pub configuration: &'a ResolvedConfigSnapshot,
}

/// Produces domain events for a command without performing I/O.
///
/// # Errors
///
/// Returns a [`DomainError`] when the command is invalid for the current lifecycle,
/// the workflow is inconsistent, or the configured event limit would be exceeded.
pub fn decide(
    definition: &WorkflowDefinition,
    state: &InstanceState,
    command: &Command,
    context: DecisionContext<'_>,
) -> Result<Vec<DomainEvent>, DomainError> {
    let events = match (command, &state.lifecycle) {
        (
            Command::StartWorkflow {
                occurred_at_epoch_ms,
            },
            Lifecycle::Initial,
        ) => {
            let Node::Start { next } = definition.node(&definition.start_node)? else {
                return Err(DomainError::InvalidStartNode(definition.start_node.clone()));
            };
            let mut events = vec![DomainEvent::WorkflowStarted {
                workflow_type: definition.workflow_type.clone(),
                workflow_version: definition.workflow_version.clone(),
                start_node_id: definition.start_node.clone(),
                occurred_at_epoch_ms: *occurred_at_epoch_ms,
            }];
            events.push(activation_event(definition, next, *occurred_at_epoch_ms)?);
            events
        }
        (Command::StartWorkflow { .. }, _) => return Err(DomainError::AlreadyStarted),
        (
            Command::CompleteServiceTask {
                node_id,
                occurred_at_epoch_ms,
            },
            Lifecycle::Active { active_node },
        ) if node_id == active_node => {
            let Node::ServiceTask { next, .. } = definition.node(node_id)? else {
                return Err(DomainError::NodeIsNotServiceTask(node_id.clone()));
            };
            vec![
                DomainEvent::ServiceTaskCompleted {
                    node_id: node_id.clone(),
                    occurred_at_epoch_ms: *occurred_at_epoch_ms,
                },
                activation_event(definition, next, *occurred_at_epoch_ms)?,
            ]
        }
        (Command::CompleteServiceTask { node_id, .. }, Lifecycle::Active { active_node }) => {
            return Err(DomainError::TaskNotActive {
                requested: node_id.clone(),
                active: active_node.clone(),
            });
        }
        (Command::CompleteServiceTask { .. }, Lifecycle::Initial) => {
            return Err(DomainError::NotStarted);
        }
        (Command::CompleteServiceTask { .. }, Lifecycle::Completed) => {
            return Err(DomainError::AlreadyCompleted);
        }
    };

    let event_count =
        u32::try_from(events.len()).map_err(|_| DomainError::DecisionLimitExceeded {
            produced: u32::MAX,
            configured_limit: context.configuration.engine.max_events_per_decision,
        })?;
    if event_count > context.configuration.engine.max_events_per_decision {
        return Err(DomainError::DecisionLimitExceeded {
            produced: event_count,
            configured_limit: context.configuration.engine.max_events_per_decision,
        });
    }
    Ok(events)
}

fn activation_event(
    definition: &WorkflowDefinition,
    node_id: &NodeId,
    occurred_at_epoch_ms: u64,
) -> Result<DomainEvent, DomainError> {
    match definition.node(node_id)? {
        Node::ServiceTask { task_type, .. } => Ok(DomainEvent::ServiceTaskActivated {
            node_id: node_id.clone(),
            task_type: task_type.clone(),
            occurred_at_epoch_ms,
        }),
        Node::End => Ok(DomainEvent::WorkflowCompleted {
            occurred_at_epoch_ms,
        }),
        Node::Start { .. } => Err(DomainError::TransitionToStartNode(node_id.clone())),
    }
}

pub fn evolve(mut state: InstanceState, event: &DomainEvent) -> InstanceState {
    state.sequence = state.sequence.saturating_add(1);
    state.lifecycle = match event {
        DomainEvent::WorkflowStarted { start_node_id, .. } => Lifecycle::Active {
            active_node: start_node_id.clone(),
        },
        DomainEvent::ServiceTaskActivated { node_id, .. }
        | DomainEvent::ServiceTaskCompleted { node_id, .. } => Lifecycle::Active {
            active_node: node_id.clone(),
        },
        DomainEvent::WorkflowCompleted { .. } => Lifecycle::Completed,
    };
    state
}

pub fn rehydrate(snapshot: Option<InstanceState>, events: &[DomainEvent]) -> InstanceState {
    events.iter().fold(snapshot.unwrap_or_default(), evolve)
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum DomainError {
    #[error("workflow contains duplicate node {0}")]
    DuplicateNode(NodeId),
    #[error("workflow start node {0} is missing or is not a start node")]
    InvalidStartNode(NodeId),
    #[error("node {source_node} points to missing node {target}")]
    MissingTransitionTarget { source_node: NodeId, target: NodeId },
    #[error("transition to start node {0} is not allowed")]
    TransitionToStartNode(NodeId),
    #[error("workflow node {0} is unreachable")]
    UnreachableNode(NodeId),
    #[error("workflow node {0} does not exist")]
    UnknownNode(NodeId),
    #[error("workflow instance has already started")]
    AlreadyStarted,
    #[error("workflow instance has not started")]
    NotStarted,
    #[error("workflow instance has already completed")]
    AlreadyCompleted,
    #[error("node {0} is not a service task")]
    NodeIsNotServiceTask(NodeId),
    #[error("requested task {requested} is not active; active task is {active}")]
    TaskNotActive { requested: NodeId, active: NodeId },
    #[error("decision produced {produced} events, above configured limit {configured_limit}")]
    DecisionLimitExceeded {
        produced: u32,
        configured_limit: u32,
    },
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::*;
    use crate::{
        ConfigId, ConfigVersion, ConfigurationScope, EnginePolicy, KeyScope, LocalWasmPolicy,
        PolicyVersion, RetryPolicy, ScopeKind,
    };

    fn id<T>(
        constructor: impl FnOnce(String) -> Result<T, crate::identifiers::IdentifierError>,
        value: &str,
    ) -> T {
        constructor(value.to_owned()).expect("test identifier must be valid")
    }

    fn definition() -> WorkflowDefinition {
        let start = id(NodeId::new, "start");
        let task = id(NodeId::new, "task");
        let end = id(NodeId::new, "end");
        WorkflowDefinition::new(
            id(WorkflowType::new, "order"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (start, Node::Start { next: task.clone() }),
                (
                    task,
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "charge"),
                        next: end.clone(),
                    },
                ),
                (end, Node::End),
            ],
        )
        .expect("test workflow must be valid")
    }

    fn configuration(max_events_per_decision: u32) -> ResolvedConfigSnapshot {
        ResolvedConfigSnapshot::new(
            id(ConfigId::new, "engine"),
            id(ConfigVersion::new, "cfg-1"),
            id(PolicyVersion::new, "policy-1"),
            1,
            vec![
                ConfigurationScope::new(ScopeKind::Platform, "default")
                    .expect("test scope must be valid"),
            ],
            [7; 32],
            EnginePolicy {
                snapshot_interval_events: 100,
                max_events_per_decision,
                command_timeout_ms: 1_000,
                optimistic_conflict_retry: RetryPolicy {
                    max_attempts: 3,
                    initial_backoff_ms: 1,
                    max_backoff_ms: 5,
                    multiplier_millis: 2_000,
                },
                local_wasm: LocalWasmPolicy {
                    max_module_bytes: 64 * 1024,
                    max_input_bytes: 32 * 1024,
                    max_output_bytes: 32 * 1024,
                    max_memory_bytes: 2 * 64 * 1024,
                    max_wasm_stack_bytes: 512 * 1024,
                    max_table_elements: 1024,
                    max_instances: 1,
                    max_tables: 1,
                    max_memories: 1,
                    fuel: 100_000,
                },
                authorization_audit_key_scope: id(KeyScope::new, "tenant-a/compliance-audit"),
            },
        )
        .expect("test configuration must be valid")
    }

    #[test]
    fn rejects_unreachable_nodes() {
        let start = id(NodeId::new, "start");
        let end = id(NodeId::new, "end");
        let unreachable = id(NodeId::new, "orphan");
        let result = WorkflowDefinition::new(
            id(WorkflowType::new, "order"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (start, Node::Start { next: end.clone() }),
                (end, Node::End),
                (unreachable.clone(), Node::End),
            ],
        );
        assert_eq!(result, Err(DomainError::UnreachableNode(unreachable)));
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 11: deterministic replay
        #[test]
        fn replay_is_deterministic(started_at in any::<u64>(), completed_at in any::<u64>()) {
            let definition = definition();
            let configuration = configuration(2);
            let context = DecisionContext { configuration: &configuration };
            let initial = InstanceState::default();
            let start_events = decide(
                &definition,
                &initial,
                &Command::StartWorkflow { occurred_at_epoch_ms: started_at },
                context,
            ).expect("start must succeed");
            let active = rehydrate(None, &start_events);
            let completion_events = decide(
                &definition,
                &active,
                &Command::CompleteServiceTask {
                    node_id: id(NodeId::new, "task"),
                    occurred_at_epoch_ms: completed_at,
                },
                context,
            ).expect("completion must succeed");
            let history = [start_events, completion_events].concat();

            prop_assert_eq!(rehydrate(None, &history), rehydrate(None, &history));
            prop_assert_eq!(rehydrate(None, &history).lifecycle, Lifecycle::Completed);
        }

        // Feature: rust-bpm-platform, Property 53: versioned configuration controls behavior
        #[test]
        fn configured_decision_limit_is_enforced(limit in 1_u32..=2) {
            let definition = definition();
            let configuration = configuration(limit);
            let result = decide(
                &definition,
                &InstanceState::default(),
                &Command::StartWorkflow { occurred_at_epoch_ms: 1 },
                DecisionContext { configuration: &configuration },
            );

            prop_assert_eq!(result.is_ok(), limit >= 2);
        }
    }
}
