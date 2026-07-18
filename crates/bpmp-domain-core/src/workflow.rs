use std::collections::{BTreeMap, BTreeSet, VecDeque};

use thiserror::Error;

use crate::{NodeId, ResolvedConfigSnapshot, TaskType, WorkflowType, WorkflowVersion};

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Node {
    Start {
        next: NodeId,
    },
    ServiceTask {
        task_type: TaskType,
        next: NodeId,
    },
    ExclusiveGateway {
        transitions: Vec<GuardedTransition>,
        coverage: Option<GatewayCoverage>,
    },
    End,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct GuardedTransition {
    pub target: NodeId,
    pub guard: Option<GuardExpression>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct GuardExpression {
    pub variable: String,
    pub operator: ComparisonOperator,
    pub literal: WorkflowValue,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum ComparisonOperator {
    Equal,
    NotEqual,
    LessThan,
    LessThanOrEqual,
    GreaterThan,
    GreaterThanOrEqual,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum WorkflowValue {
    Boolean(bool),
    Integer(i64),
    String(String),
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct GatewayCoverage {
    pub variable: String,
    pub domain: GatewayCoverageDomain,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum GatewayCoverageDomain {
    Boolean,
    Enum { values: Vec<String> },
    Integer { intervals: Vec<IntegerInterval> },
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct IntegerInterval {
    pub lower: Option<i64>,
    pub upper: Option<i64>,
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
            if let Node::ExclusiveGateway {
                transitions,
                coverage,
            } = node
            {
                validate_gateway(node_id, transitions, coverage.as_ref())?;
            }
            for next in node.targets() {
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
            if let Some(node) = self.nodes.get(&node_id) {
                pending.extend(node.targets().cloned());
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

fn validate_gateway(
    node_id: &NodeId,
    transitions: &[GuardedTransition],
    coverage: Option<&GatewayCoverage>,
) -> Result<(), DomainError> {
    if transitions.len() < 2 {
        return Err(DomainError::GatewayRequiresBranches(node_id.clone()));
    }
    let defaults = transitions
        .iter()
        .filter(|transition| transition.guard.is_none())
        .count();
    match defaults {
        0 => validate_gateway_coverage(
            node_id,
            transitions,
            coverage
                .ok_or_else(|| DomainError::GatewayRequiresDefaultOrCoverage(node_id.clone()))?,
        )?,
        1 => {
            if coverage.is_some() {
                return Err(DomainError::GatewayCoverageWithDefault(node_id.clone()));
            }
        }
        actual => {
            return Err(DomainError::GatewayRequiresOneDefault {
                gateway: node_id.clone(),
                actual,
            });
        }
    }
    let mut targets = BTreeSet::new();
    if let Some(duplicate) = transitions
        .iter()
        .map(|transition| &transition.target)
        .find(|target| !targets.insert((*target).clone()))
    {
        return Err(DomainError::DuplicateGatewayTarget {
            gateway: node_id.clone(),
            target: duplicate.clone(),
        });
    }
    Ok(())
}

fn validate_gateway_coverage(
    node_id: &NodeId,
    transitions: &[GuardedTransition],
    coverage: &GatewayCoverage,
) -> Result<(), DomainError> {
    if coverage.variable.trim().is_empty() {
        return Err(DomainError::InvalidGatewayCoverage {
            gateway: node_id.clone(),
            detail: "coverage variable is empty".into(),
        });
    }
    if transitions
        .iter()
        .any(|transition| transition.guard.is_none())
    {
        return Err(DomainError::InvalidGatewayCoverage {
            gateway: node_id.clone(),
            detail: "coverage proof cannot be combined with default branches".into(),
        });
    }
    let guards = transitions
        .iter()
        .map(|transition| transition.guard.as_ref().expect("checked above"))
        .collect::<Vec<_>>();
    if guards
        .iter()
        .any(|guard| guard.variable != coverage.variable)
    {
        return Err(DomainError::InvalidGatewayCoverage {
            gateway: node_id.clone(),
            detail: "all covered guards must use the coverage variable".into(),
        });
    }
    match &coverage.domain {
        GatewayCoverageDomain::Boolean => validate_boolean_coverage(node_id, &guards),
        GatewayCoverageDomain::Enum { values } => validate_enum_coverage(node_id, &guards, values),
        GatewayCoverageDomain::Integer { intervals } => {
            validate_integer_coverage(node_id, &guards, intervals)
        }
    }
}

fn validate_boolean_coverage(
    node_id: &NodeId,
    guards: &[&GuardExpression],
) -> Result<(), DomainError> {
    let mut covered = BTreeSet::new();
    for guard in guards {
        let WorkflowValue::Boolean(value) = &guard.literal else {
            return invalid_gateway_coverage(node_id, "boolean coverage cannot mix literal types");
        };
        let values = match guard.operator {
            ComparisonOperator::Equal => vec![*value],
            ComparisonOperator::NotEqual => vec![!*value],
            _ => {
                return invalid_gateway_coverage(
                    node_id,
                    "boolean coverage supports only == and !=",
                );
            }
        };
        for value in values {
            if !covered.insert(value) {
                return invalid_gateway_coverage(
                    node_id,
                    "boolean value is matched by more than one branch",
                );
            }
        }
    }
    if covered.len() == 2 {
        Ok(())
    } else {
        invalid_gateway_coverage(node_id, "boolean domain is not fully covered")
    }
}

fn validate_enum_coverage(
    node_id: &NodeId,
    guards: &[&GuardExpression],
    values: &[String],
) -> Result<(), DomainError> {
    if values.is_empty() {
        return invalid_gateway_coverage(node_id, "enum coverage domain is empty");
    }
    let declared = values.iter().cloned().collect::<BTreeSet<_>>();
    if declared.len() != values.len() {
        return invalid_gateway_coverage(node_id, "enum coverage domain contains duplicates");
    }
    let mut covered = BTreeSet::new();
    for guard in guards {
        let WorkflowValue::String(value) = &guard.literal else {
            return invalid_gateway_coverage(node_id, "enum coverage cannot mix literal types");
        };
        if guard.operator != ComparisonOperator::Equal {
            return invalid_gateway_coverage(node_id, "enum coverage supports only equality");
        }
        if !declared.contains(value) {
            return invalid_gateway_coverage(node_id, "enum guard is outside coverage domain");
        }
        if !covered.insert(value.clone()) {
            return invalid_gateway_coverage(
                node_id,
                "enum value is matched by more than one branch",
            );
        }
    }
    if declared == covered {
        Ok(())
    } else {
        invalid_gateway_coverage(node_id, "enum domain is not fully covered")
    }
}

fn validate_integer_coverage(
    node_id: &NodeId,
    guards: &[&GuardExpression],
    proof: &[IntegerInterval],
) -> Result<(), DomainError> {
    let mut intervals = Vec::new();
    for guard in guards {
        let WorkflowValue::Integer(value) = &guard.literal else {
            return invalid_gateway_coverage(node_id, "integer coverage cannot mix literal types");
        };
        intervals.extend(integer_guard_intervals(guard.operator, *value));
    }
    validate_disjoint_integer_cover(node_id, &mut intervals)?;
    let mut normalized_proof = proof.to_vec();
    validate_disjoint_integer_cover(node_id, &mut normalized_proof)?;
    if intervals == normalized_proof {
        Ok(())
    } else {
        invalid_gateway_coverage(node_id, "integer coverage proof does not match guards")
    }
}

fn integer_guard_intervals(operator: ComparisonOperator, value: i64) -> Vec<IntegerInterval> {
    match operator {
        ComparisonOperator::Equal => vec![IntegerInterval {
            lower: Some(value),
            upper: Some(value),
        }],
        ComparisonOperator::NotEqual => {
            let mut intervals = Vec::new();
            if let Some(upper) = value.checked_sub(1) {
                intervals.push(IntegerInterval {
                    lower: None,
                    upper: Some(upper),
                });
            }
            if let Some(lower) = value.checked_add(1) {
                intervals.push(IntegerInterval {
                    lower: Some(lower),
                    upper: None,
                });
            }
            intervals
        }
        ComparisonOperator::LessThan => value
            .checked_sub(1)
            .map(|upper| {
                vec![IntegerInterval {
                    lower: None,
                    upper: Some(upper),
                }]
            })
            .unwrap_or_default(),
        ComparisonOperator::LessThanOrEqual => vec![IntegerInterval {
            lower: None,
            upper: Some(value),
        }],
        ComparisonOperator::GreaterThan => value
            .checked_add(1)
            .map(|lower| {
                vec![IntegerInterval {
                    lower: Some(lower),
                    upper: None,
                }]
            })
            .unwrap_or_default(),
        ComparisonOperator::GreaterThanOrEqual => vec![IntegerInterval {
            lower: Some(value),
            upper: None,
        }],
    }
}

fn validate_disjoint_integer_cover(
    node_id: &NodeId,
    intervals: &mut [IntegerInterval],
) -> Result<(), DomainError> {
    intervals.sort_unstable_by_key(|interval| (interval.lower.unwrap_or(i64::MIN), interval.upper));
    let mut expected_lower = None;
    for interval in intervals {
        match compare_interval_lower(interval.lower, expected_lower) {
            std::cmp::Ordering::Less => {
                return invalid_gateway_coverage(node_id, "integer intervals overlap");
            }
            std::cmp::Ordering::Greater => {
                return invalid_gateway_coverage(node_id, "integer intervals leave a gap");
            }
            std::cmp::Ordering::Equal => {}
        }
        if let (Some(lower), Some(upper)) = (interval.lower, interval.upper)
            && lower > upper
        {
            return invalid_gateway_coverage(
                node_id,
                "integer interval lower bound is above upper bound",
            );
        }
        expected_lower = match interval.upper {
            Some(i64::MAX) | None => return Ok(()),
            Some(value) => value.checked_add(1),
        };
        if expected_lower.is_none() {
            return invalid_gateway_coverage(node_id, "integer interval upper bound overflows");
        }
    }
    invalid_gateway_coverage(node_id, "integer intervals do not cover upper range")
}

fn compare_interval_lower(actual: Option<i64>, expected: Option<i64>) -> std::cmp::Ordering {
    actual
        .unwrap_or(i64::MIN)
        .cmp(&expected.unwrap_or(i64::MIN))
}

fn invalid_gateway_coverage<T>(node_id: &NodeId, detail: &str) -> Result<T, DomainError> {
    Err(DomainError::InvalidGatewayCoverage {
        gateway: node_id.clone(),
        detail: detail.to_owned(),
    })
}

impl Node {
    fn targets(&self) -> Box<dyn Iterator<Item = &NodeId> + '_> {
        match self {
            Self::Start { next } | Self::ServiceTask { next, .. } => {
                Box::new(std::iter::once(next))
            }
            Self::ExclusiveGateway { transitions, .. } => {
                Box::new(transitions.iter().map(|transition| &transition.target))
            }
            Self::End => Box::new(std::iter::empty()),
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
    pub variables: &'a BTreeMap<String, WorkflowValue>,
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
            events.push(activation_event(
                definition,
                next,
                context.variables,
                *occurred_at_epoch_ms,
            )?);
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
                activation_event(definition, next, context.variables, *occurred_at_epoch_ms)?,
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
    variables: &BTreeMap<String, WorkflowValue>,
    occurred_at_epoch_ms: u64,
) -> Result<DomainEvent, DomainError> {
    let mut current = node_id;
    for _ in 0..definition.nodes.len() {
        match definition.node(current)? {
            Node::ServiceTask { task_type, .. } => {
                return Ok(DomainEvent::ServiceTaskActivated {
                    node_id: current.clone(),
                    task_type: task_type.clone(),
                    occurred_at_epoch_ms,
                });
            }
            Node::End => {
                return Ok(DomainEvent::WorkflowCompleted {
                    occurred_at_epoch_ms,
                });
            }
            Node::Start { .. } => {
                return Err(DomainError::TransitionToStartNode(current.clone()));
            }
            Node::ExclusiveGateway { transitions, .. } => {
                current = select_transition(current, transitions, variables)?;
            }
        }
    }
    Err(DomainError::AutomaticTransitionLimitExceeded(
        node_id.clone(),
    ))
}

fn select_transition<'a>(
    gateway_id: &NodeId,
    transitions: &'a [GuardedTransition],
    variables: &BTreeMap<String, WorkflowValue>,
) -> Result<&'a NodeId, DomainError> {
    let mut selected = None;
    for transition in transitions {
        let Some(guard) = &transition.guard else {
            continue;
        };
        if guard.evaluate(variables)? {
            if selected.is_some() {
                return Err(DomainError::AmbiguousGateway(gateway_id.clone()));
            }
            selected = Some(&transition.target);
        }
    }
    selected
        .or_else(|| {
            transitions
                .iter()
                .find(|transition| transition.guard.is_none())
                .map(|transition| &transition.target)
        })
        .ok_or_else(|| DomainError::NoGatewayBranch(gateway_id.clone()))
}

impl GuardExpression {
    fn evaluate(&self, variables: &BTreeMap<String, WorkflowValue>) -> Result<bool, DomainError> {
        let actual = variables
            .get(&self.variable)
            .ok_or_else(|| DomainError::GuardVariableMissing(self.variable.clone()))?;
        let ordering =
            actual
                .same_type_cmp(&self.literal)
                .ok_or_else(|| DomainError::GuardTypeMismatch {
                    variable: self.variable.clone(),
                    actual: actual.type_name(),
                    expected: self.literal.type_name(),
                })?;
        Ok(match self.operator {
            ComparisonOperator::Equal => ordering.is_eq(),
            ComparisonOperator::NotEqual => !ordering.is_eq(),
            ComparisonOperator::LessThan => ordering.is_lt(),
            ComparisonOperator::LessThanOrEqual => ordering.is_le(),
            ComparisonOperator::GreaterThan => ordering.is_gt(),
            ComparisonOperator::GreaterThanOrEqual => ordering.is_ge(),
        })
    }
}

impl WorkflowValue {
    fn same_type_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        match (self, other) {
            (Self::Integer(left), Self::Integer(right)) => Some(left.cmp(right)),
            (Self::String(left), Self::String(right)) => Some(left.cmp(right)),
            (Self::Boolean(left), Self::Boolean(right)) => Some(left.cmp(right)),
            _ => None,
        }
    }

    const fn type_name(&self) -> &'static str {
        match self {
            Self::Boolean(_) => "boolean",
            Self::Integer(_) => "integer",
            Self::String(_) => "string",
        }
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
    #[error("exclusive gateway {0} matched more than one branch")]
    AmbiguousGateway(NodeId),
    #[error("exclusive gateway {0} has no matching branch and no default")]
    NoGatewayBranch(NodeId),
    #[error("automatic transition chain from {0} exceeds workflow node count")]
    AutomaticTransitionLimitExceeded(NodeId),
    #[error("exclusive gateway {0} must have at least two branches")]
    GatewayRequiresBranches(NodeId),
    #[error("exclusive gateway {0} must have either one default branch or valid static coverage")]
    GatewayRequiresDefaultOrCoverage(NodeId),
    #[error("exclusive gateway {0} must not declare static coverage when a default branch exists")]
    GatewayCoverageWithDefault(NodeId),
    #[error("exclusive gateway {gateway} must have exactly one default branch, found {actual}")]
    GatewayRequiresOneDefault { gateway: NodeId, actual: usize },
    #[error("exclusive gateway {gateway} has invalid coverage proof: {detail}")]
    InvalidGatewayCoverage { gateway: NodeId, detail: String },
    #[error("exclusive gateway {gateway} has duplicate target {target}")]
    DuplicateGatewayTarget { gateway: NodeId, target: NodeId },
    #[error("gateway guard variable {0} is missing")]
    GuardVariableMissing(String),
    #[error("gateway guard variable {variable} has type {actual}, expected {expected}")]
    GuardTypeMismatch {
        variable: String,
        actual: &'static str,
        expected: &'static str,
    },
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

    fn gateway_definition() -> WorkflowDefinition {
        let start = id(NodeId::new, "start");
        let gateway = id(NodeId::new, "route");
        let approved = id(NodeId::new, "approved");
        let rejected = id(NodeId::new, "rejected");
        WorkflowDefinition::new(
            id(WorkflowType::new, "routing"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: gateway.clone(),
                    },
                ),
                (
                    gateway,
                    Node::ExclusiveGateway {
                        transitions: vec![
                            GuardedTransition {
                                target: approved.clone(),
                                guard: Some(GuardExpression {
                                    variable: "approved".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(true),
                                }),
                            },
                            GuardedTransition {
                                target: rejected.clone(),
                                guard: None,
                            },
                        ],
                        coverage: None,
                    },
                ),
                (
                    approved.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "approve"),
                        next: rejected.clone(),
                    },
                ),
                (rejected, Node::End),
            ],
        )
        .unwrap()
    }

    #[test]
    fn gateway_evaluation_is_typed_and_deterministic() {
        let definition = gateway_definition();
        let configuration = configuration(2);
        let variables = BTreeMap::from([("approved".into(), WorkflowValue::Boolean(true))]);
        let events = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                occurred_at_epoch_ms: 7,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &variables,
            },
        )
        .unwrap();
        assert!(matches!(
            &events[1],
            DomainEvent::ServiceTaskActivated { node_id, .. } if node_id.as_str() == "approved"
        ));
        assert_eq!(
            events,
            decide(
                &definition,
                &InstanceState::default(),
                &Command::StartWorkflow {
                    occurred_at_epoch_ms: 7
                },
                DecisionContext {
                    configuration: &configuration,
                    variables: &variables
                },
            )
            .unwrap()
        );
    }

    #[test]
    fn gateway_guard_fails_closed_for_missing_or_wrong_type_variable() {
        let definition = gateway_definition();
        let configuration = configuration(2);
        let command = Command::StartWorkflow {
            occurred_at_epoch_ms: 7,
        };
        let missing = BTreeMap::new();
        assert_eq!(
            decide(
                &definition,
                &InstanceState::default(),
                &command,
                DecisionContext {
                    configuration: &configuration,
                    variables: &missing,
                },
            ),
            Err(DomainError::GuardVariableMissing("approved".into()))
        );

        let wrong_type = BTreeMap::from([("approved".into(), WorkflowValue::Integer(1))]);
        assert!(matches!(
            decide(
                &definition,
                &InstanceState::default(),
                &command,
                DecisionContext {
                    configuration: &configuration,
                    variables: &wrong_type,
                },
            ),
            Err(DomainError::GuardTypeMismatch { .. })
        ));
    }

    #[test]
    fn exhaustive_gateway_without_default_uses_valid_coverage_proof() {
        let start = id(NodeId::new, "start");
        let gateway = id(NodeId::new, "route");
        let approved = id(NodeId::new, "approved");
        let rejected = id(NodeId::new, "rejected");
        let definition = WorkflowDefinition::new(
            id(WorkflowType::new, "routing"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: gateway.clone(),
                    },
                ),
                (
                    gateway,
                    Node::ExclusiveGateway {
                        transitions: vec![
                            GuardedTransition {
                                target: approved.clone(),
                                guard: Some(GuardExpression {
                                    variable: "approved".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(true),
                                }),
                            },
                            GuardedTransition {
                                target: rejected.clone(),
                                guard: Some(GuardExpression {
                                    variable: "approved".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(false),
                                }),
                            },
                        ],
                        coverage: Some(GatewayCoverage {
                            variable: "approved".into(),
                            domain: GatewayCoverageDomain::Boolean,
                        }),
                    },
                ),
                (
                    approved.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "approve"),
                        next: rejected.clone(),
                    },
                ),
                (rejected, Node::End),
            ],
        )
        .unwrap();
        let configuration = configuration(2);
        let variables = BTreeMap::from([("approved".into(), WorkflowValue::Boolean(true))]);
        let events = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                occurred_at_epoch_ms: 7,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &variables,
            },
        )
        .unwrap();
        assert!(matches!(
            &events[1],
            DomainEvent::ServiceTaskActivated { node_id, .. } if node_id == &approved
        ));
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
            let variables = BTreeMap::new();
            let context = DecisionContext {
                configuration: &configuration,
                variables: &variables,
            };
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
            let variables = BTreeMap::new();
            let result = decide(
                &definition,
                &InstanceState::default(),
                &Command::StartWorkflow { occurred_at_epoch_ms: 1 },
                DecisionContext {
                    configuration: &configuration,
                    variables: &variables,
                },
            );

            prop_assert_eq!(result.is_ok(), limit >= 2);
        }
    }
}
