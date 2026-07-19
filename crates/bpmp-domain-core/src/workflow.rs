use std::collections::{BTreeMap, BTreeSet, VecDeque};

use thiserror::Error;

use crate::{NodeId, ResolvedConfigSnapshot, TaskType, TenantId, WorkflowType, WorkflowVersion};

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Node {
    Start {
        next: NodeId,
    },
    ServiceTask {
        task_type: TaskType,
        next: NodeId,
    },
    DecisionTask {
        decision_table_id: String,
        next: NodeId,
    },
    ExclusiveGateway {
        transitions: Vec<GuardedTransition>,
        coverage: Option<GatewayCoverage>,
    },
    ParallelSplit {
        targets: Vec<NodeId>,
        join: NodeId,
    },
    ParallelJoin {
        split: NodeId,
        next: NodeId,
    },
    InclusiveSplit {
        transitions: Vec<GuardedTransition>,
        coverage: Option<GatewayCoverage>,
        join: NodeId,
    },
    InclusiveJoin {
        split: NodeId,
        next: NodeId,
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
    pub tenant_id: TenantId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub start_node: NodeId,
    nodes: BTreeMap<NodeId, Node>,
    decision_tables: BTreeMap<String, DecisionTable>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct DecisionTable {
    pub id: String,
    pub hit_policy: HitPolicy,
    pub inputs: Vec<DecisionInput>,
    pub outputs: Vec<DecisionOutput>,
    pub rules: Vec<DecisionRule>,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum HitPolicy {
    Unique,
    First,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct DecisionInput {
    pub name: String,
    pub value_type: WorkflowValueType,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct DecisionOutput {
    pub name: String,
    pub value_type: WorkflowValueType,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct DecisionRule {
    pub id: String,
    pub input_tests: Vec<UnaryTest>,
    pub output_values: Vec<WorkflowValue>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum UnaryTest {
    Any,
    Equal(WorkflowValue),
    IntegerInterval(IntegerInterval),
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum WorkflowValueType {
    Boolean,
    Integer,
    String,
}

impl WorkflowDefinition {
    /// Builds a workflow definition after structural and reachability validation.
    ///
    /// # Errors
    ///
    /// Returns a [`DomainError`] for duplicate, missing, invalid, or unreachable nodes.
    pub fn new(
        tenant_id: TenantId,
        workflow_type: WorkflowType,
        workflow_version: WorkflowVersion,
        start_node: NodeId,
        nodes: impl IntoIterator<Item = (NodeId, Node)>,
    ) -> Result<Self, DomainError> {
        Self::new_with_decisions(
            tenant_id,
            workflow_type,
            workflow_version,
            start_node,
            nodes,
            std::iter::empty(),
        )
    }

    /// Builds a workflow definition with embedded pure decision tables.
    ///
    /// # Errors
    ///
    /// Returns a [`DomainError`] for invalid graph or decision-table shape.
    pub fn new_with_decisions(
        tenant_id: TenantId,
        workflow_type: WorkflowType,
        workflow_version: WorkflowVersion,
        start_node: NodeId,
        nodes: impl IntoIterator<Item = (NodeId, Node)>,
        decision_tables: impl IntoIterator<Item = DecisionTable>,
    ) -> Result<Self, DomainError> {
        let mut indexed = BTreeMap::new();
        for (node_id, node) in nodes {
            if indexed.insert(node_id.clone(), node).is_some() {
                return Err(DomainError::DuplicateNode(node_id));
            }
        }
        let mut indexed_tables = BTreeMap::new();
        for table in decision_tables {
            validate_decision_table(&table)?;
            if indexed_tables.insert(table.id.clone(), table).is_some() {
                return Err(DomainError::DuplicateDecisionTable);
            }
        }

        if !matches!(indexed.get(&start_node), Some(Node::Start { .. })) {
            return Err(DomainError::InvalidStartNode(start_node));
        }

        for (node_id, node) in &indexed {
            validate_gateway_node(node_id, node, &indexed)?;
            if let Node::DecisionTask {
                decision_table_id, ..
            } = node
                && !indexed_tables.contains_key(decision_table_id)
            {
                return Err(DomainError::MissingDecisionTable(decision_table_id.clone()));
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
            tenant_id,
            workflow_type,
            workflow_version,
            start_node,
            nodes: indexed,
            decision_tables: indexed_tables,
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

fn validate_gateway_node(
    node_id: &NodeId,
    node: &Node,
    indexed: &BTreeMap<NodeId, Node>,
) -> Result<(), DomainError> {
    match node {
        Node::ExclusiveGateway {
            transitions,
            coverage,
        } => validate_gateway(node_id, transitions, coverage.as_ref()),
        Node::ParallelSplit { targets, join } => {
            validate_parallel_split(node_id, targets)?;
            reciprocal_pair(
                indexed.get(join),
                |paired| matches!(paired, Node::ParallelJoin { split, .. } if split == node_id),
                node_id,
                join,
            )
        }
        Node::ParallelJoin { split, .. } => reciprocal_pair(
            indexed.get(split),
            |paired| matches!(paired, Node::ParallelSplit { join, .. } if join == node_id),
            node_id,
            split,
        ),
        Node::InclusiveSplit {
            transitions,
            coverage,
            join,
        } => {
            validate_inclusive_gateway(node_id, transitions, coverage.as_ref())?;
            reciprocal_pair(
                indexed.get(join),
                |paired| matches!(paired, Node::InclusiveJoin { split, .. } if split == node_id),
                node_id,
                join,
            )
        }
        Node::InclusiveJoin { split, .. } => reciprocal_pair(
            indexed.get(split),
            |paired| matches!(paired, Node::InclusiveSplit { join, .. } if join == node_id),
            node_id,
            split,
        ),
        Node::Start { .. } | Node::ServiceTask { .. } | Node::DecisionTask { .. } | Node::End => {
            Ok(())
        }
    }
}

fn reciprocal_pair(
    paired_node: Option<&Node>,
    predicate: impl FnOnce(&Node) -> bool,
    gateway: &NodeId,
    paired: &NodeId,
) -> Result<(), DomainError> {
    if paired_node.is_some_and(predicate) {
        Ok(())
    } else {
        Err(DomainError::InvalidGatewayPair {
            gateway: gateway.clone(),
            paired: paired.clone(),
        })
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

fn validate_decision_table(table: &DecisionTable) -> Result<(), DomainError> {
    if table.id.trim().is_empty() {
        return Err(DomainError::InvalidDecisionTable {
            table_id: table.id.clone(),
            detail: "decision table id is empty".into(),
        });
    }
    if table.inputs.is_empty() || table.outputs.is_empty() {
        return Err(DomainError::InvalidDecisionTable {
            table_id: table.id.clone(),
            detail: "decision table must declare inputs and outputs".into(),
        });
    }
    for rule in &table.rules {
        if rule.input_tests.len() != table.inputs.len() {
            return Err(DomainError::InvalidDecisionTable {
                table_id: table.id.clone(),
                detail: format!("rule {} input count does not match table inputs", rule.id),
            });
        }
        if rule.output_values.len() != table.outputs.len() {
            return Err(DomainError::InvalidDecisionTable {
                table_id: table.id.clone(),
                detail: format!("rule {} output count does not match table outputs", rule.id),
            });
        }
        for (value, output) in rule.output_values.iter().zip(&table.outputs) {
            if !output.value_type.matches(value) {
                return Err(DomainError::InvalidDecisionTable {
                    table_id: table.id.clone(),
                    detail: format!("rule {} output {} has wrong type", rule.id, output.name),
                });
            }
        }
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
            Self::Start { next }
            | Self::ServiceTask { next, .. }
            | Self::DecisionTask { next, .. }
            | Self::ParallelJoin { next, .. }
            | Self::InclusiveJoin { next, .. } => Box::new(std::iter::once(next)),
            Self::ExclusiveGateway { transitions, .. }
            | Self::InclusiveSplit { transitions, .. } => {
                Box::new(transitions.iter().map(|transition| &transition.target))
            }
            Self::ParallelSplit { targets, .. } => Box::new(targets.iter()),
            Self::End => Box::new(std::iter::empty()),
        }
    }
}

fn validate_parallel_split(node_id: &NodeId, targets: &[NodeId]) -> Result<(), DomainError> {
    if targets.len() < 2 {
        return Err(DomainError::GatewayRequiresBranches(node_id.clone()));
    }
    let unique = targets.iter().collect::<BTreeSet<_>>();
    if unique.len() != targets.len() {
        return Err(DomainError::DuplicateGatewayTarget {
            gateway: node_id.clone(),
            target: targets
                .iter()
                .find(|target| targets.iter().filter(|item| *item == *target).count() > 1)
                .cloned()
                .expect("duplicate target exists"),
        });
    }
    Ok(())
}

fn validate_inclusive_gateway(
    node_id: &NodeId,
    transitions: &[GuardedTransition],
    coverage: Option<&GatewayCoverage>,
) -> Result<(), DomainError> {
    if transitions.len() < 2 {
        return Err(DomainError::GatewayRequiresBranches(node_id.clone()));
    }
    let default_count = transitions
        .iter()
        .filter(|item| item.guard.is_none())
        .count();
    if default_count > 1 {
        return Err(DomainError::GatewayRequiresOneDefault {
            gateway: node_id.clone(),
            actual: default_count,
        });
    }
    if default_count == 0 && coverage.is_none() {
        return Err(DomainError::GatewayRequiresDefaultOrCoverage(
            node_id.clone(),
        ));
    }
    let unique = transitions
        .iter()
        .map(|transition| &transition.target)
        .collect::<BTreeSet<_>>();
    if unique.len() != transitions.len() {
        return Err(DomainError::DuplicateGatewayTarget {
            gateway: node_id.clone(),
            target: transitions[0].target.clone(),
        });
    }
    Ok(())
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Command {
    StartWorkflow {
        tenant_id: TenantId,
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
        tenant_id: TenantId,
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
    DecisionTaskEvaluated {
        node_id: NodeId,
        decision_table_id: String,
        outputs: BTreeMap<String, WorkflowValue>,
        occurred_at_epoch_ms: u64,
    },
    GatewaySplitActivated {
        gateway_id: NodeId,
        join_gateway_id: NodeId,
        selected_targets: Vec<NodeId>,
        occurred_at_epoch_ms: u64,
    },
    GatewayTokenArrived {
        gateway_id: NodeId,
        occurred_at_epoch_ms: u64,
    },
    GatewayJoined {
        gateway_id: NodeId,
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
    pub variables: BTreeMap<String, WorkflowValue>,
    pub active_tokens: BTreeMap<NodeId, u32>,
    pub pending_gateway_joins: BTreeMap<NodeId, PendingGatewayJoin>,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct PendingGatewayJoin {
    pub expected_tokens: u32,
    pub arrived_tokens: u32,
}

impl Default for InstanceState {
    fn default() -> Self {
        Self {
            lifecycle: Lifecycle::Initial,
            sequence: 0,
            variables: BTreeMap::new(),
            active_tokens: BTreeMap::new(),
            pending_gateway_joins: BTreeMap::new(),
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
                tenant_id,
                occurred_at_epoch_ms,
            },
            Lifecycle::Initial,
        ) => {
            if tenant_id != &definition.tenant_id {
                return Err(DomainError::TenantMismatch {
                    expected: definition.tenant_id.clone(),
                    actual: tenant_id.clone(),
                });
            }
            let Node::Start { next } = definition.node(&definition.start_node)? else {
                return Err(DomainError::InvalidStartNode(definition.start_node.clone()));
            };
            let mut events = vec![DomainEvent::WorkflowStarted {
                tenant_id: tenant_id.clone(),
                workflow_type: definition.workflow_type.clone(),
                workflow_version: definition.workflow_version.clone(),
                start_node_id: definition.start_node.clone(),
                occurred_at_epoch_ms: *occurred_at_epoch_ms,
            }];
            let mut variables = state_variables_with_context(state, context.variables);
            let mut routing = RoutingState::from(state);
            events.extend(activation_events(
                definition,
                next,
                &mut variables,
                &mut routing,
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
            Lifecycle::Active { .. },
        ) if state
            .active_tokens
            .get(node_id)
            .copied()
            .unwrap_or_default()
            > 0 =>
        {
            let Node::ServiceTask { next, .. } = definition.node(node_id)? else {
                return Err(DomainError::NodeIsNotServiceTask(node_id.clone()));
            };
            let mut events = vec![DomainEvent::ServiceTaskCompleted {
                node_id: node_id.clone(),
                occurred_at_epoch_ms: *occurred_at_epoch_ms,
            }];
            let mut variables = state_variables_with_context(state, context.variables);
            let mut routing = RoutingState::from(state);
            routing.complete_task(node_id)?;
            events.extend(activation_events(
                definition,
                next,
                &mut variables,
                &mut routing,
                *occurred_at_epoch_ms,
            )?);
            events
        }
        (Command::CompleteServiceTask { node_id, .. }, Lifecycle::Active { .. }) => {
            return Err(DomainError::TaskNotActive(node_id.clone()));
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

fn activation_events(
    definition: &WorkflowDefinition,
    node_id: &NodeId,
    variables: &mut BTreeMap<String, WorkflowValue>,
    routing: &mut RoutingState,
    occurred_at_epoch_ms: u64,
) -> Result<Vec<DomainEvent>, DomainError> {
    let mut pending = vec![node_id.clone()];
    let mut events = Vec::new();
    let max_steps = definition.nodes.len().saturating_mul(4).max(1);
    let mut steps = 0_usize;
    while let Some(current) = pending.pop() {
        steps = steps.saturating_add(1);
        if steps > max_steps {
            return Err(DomainError::AutomaticTransitionLimitExceeded(
                node_id.clone(),
            ));
        }
        match definition.node(&current)? {
            Node::ServiceTask { task_type, .. } => {
                routing.activate_task(&current)?;
                events.push(DomainEvent::ServiceTaskActivated {
                    node_id: current,
                    task_type: task_type.clone(),
                    occurred_at_epoch_ms,
                });
            }
            Node::DecisionTask {
                decision_table_id,
                next,
            } => {
                let outputs = evaluate_decision_table(
                    definition
                        .decision_tables
                        .get(decision_table_id)
                        .ok_or_else(|| {
                            DomainError::MissingDecisionTable(decision_table_id.clone())
                        })?,
                    variables,
                )?;
                variables.extend(outputs.clone());
                events.push(DomainEvent::DecisionTaskEvaluated {
                    node_id: current,
                    decision_table_id: decision_table_id.clone(),
                    outputs,
                    occurred_at_epoch_ms,
                });
                pending.push(next.clone());
            }
            Node::End => {
                if !routing.active_tokens.is_empty() || !routing.pending_joins.is_empty() {
                    return Err(DomainError::EndReachedWithOutstandingTokens(current));
                }
                events.push(DomainEvent::WorkflowCompleted {
                    occurred_at_epoch_ms,
                });
            }
            Node::Start { .. } => {
                return Err(DomainError::TransitionToStartNode(current));
            }
            Node::ExclusiveGateway { transitions, .. } => {
                pending.push(select_transition(&current, transitions, variables)?.clone());
            }
            Node::ParallelSplit { targets, join } => {
                routing.open_join(join, targets.len())?;
                events.push(DomainEvent::GatewaySplitActivated {
                    gateway_id: current,
                    join_gateway_id: join.clone(),
                    selected_targets: targets.clone(),
                    occurred_at_epoch_ms,
                });
                pending.extend(targets.iter().rev().cloned());
            }
            Node::InclusiveSplit {
                transitions, join, ..
            } => {
                let selected = select_transitions(&current, transitions, variables)?;
                routing.open_join(join, selected.len())?;
                events.push(DomainEvent::GatewaySplitActivated {
                    gateway_id: current,
                    join_gateway_id: join.clone(),
                    selected_targets: selected.clone(),
                    occurred_at_epoch_ms,
                });
                pending.extend(selected.into_iter().rev());
            }
            Node::ParallelJoin { next, .. } | Node::InclusiveJoin { next, .. } => {
                let complete = routing.arrive(&current)?;
                events.push(DomainEvent::GatewayTokenArrived {
                    gateway_id: current.clone(),
                    occurred_at_epoch_ms,
                });
                if complete {
                    routing.close_join(&current);
                    events.push(DomainEvent::GatewayJoined {
                        gateway_id: current,
                        occurred_at_epoch_ms,
                    });
                    pending.push(next.clone());
                }
            }
        }
    }
    Ok(events)
}

#[derive(Clone)]
struct RoutingState {
    active_tokens: BTreeMap<NodeId, u32>,
    pending_joins: BTreeMap<NodeId, PendingGatewayJoin>,
}

impl From<&InstanceState> for RoutingState {
    fn from(state: &InstanceState) -> Self {
        Self {
            active_tokens: state.active_tokens.clone(),
            pending_joins: state.pending_gateway_joins.clone(),
        }
    }
}

impl RoutingState {
    fn activate_task(&mut self, node_id: &NodeId) -> Result<(), DomainError> {
        let count = self.active_tokens.entry(node_id.clone()).or_default();
        *count = count
            .checked_add(1)
            .ok_or_else(|| DomainError::TokenCountOverflow(node_id.clone()))?;
        Ok(())
    }

    fn complete_task(&mut self, node_id: &NodeId) -> Result<(), DomainError> {
        let Some(count) = self.active_tokens.get_mut(node_id) else {
            return Err(DomainError::TaskNotActive(node_id.clone()));
        };
        *count -= 1;
        if *count == 0 {
            self.active_tokens.remove(node_id);
        }
        Ok(())
    }

    fn open_join(&mut self, join: &NodeId, target_count: usize) -> Result<(), DomainError> {
        let expected_tokens = u32::try_from(target_count)
            .map_err(|_| DomainError::TokenCountOverflow(join.clone()))?;
        if self
            .pending_joins
            .insert(
                join.clone(),
                PendingGatewayJoin {
                    expected_tokens,
                    arrived_tokens: 0,
                },
            )
            .is_some()
        {
            return Err(DomainError::JoinAlreadyPending(join.clone()));
        }
        Ok(())
    }

    fn arrive(&mut self, join: &NodeId) -> Result<bool, DomainError> {
        let state = self
            .pending_joins
            .get_mut(join)
            .ok_or_else(|| DomainError::UnexpectedGatewayJoin(join.clone()))?;
        state.arrived_tokens = state
            .arrived_tokens
            .checked_add(1)
            .ok_or_else(|| DomainError::TokenCountOverflow(join.clone()))?;
        if state.arrived_tokens > state.expected_tokens {
            return Err(DomainError::UnexpectedGatewayJoin(join.clone()));
        }
        Ok(state.arrived_tokens == state.expected_tokens)
    }

    fn close_join(&mut self, join: &NodeId) {
        self.pending_joins.remove(join);
    }
}

fn state_variables_with_context(
    state: &InstanceState,
    context_variables: &BTreeMap<String, WorkflowValue>,
) -> BTreeMap<String, WorkflowValue> {
    let mut variables = state.variables.clone();
    variables.extend(context_variables.clone());
    variables
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

fn select_transitions(
    gateway_id: &NodeId,
    transitions: &[GuardedTransition],
    variables: &BTreeMap<String, WorkflowValue>,
) -> Result<Vec<NodeId>, DomainError> {
    let mut selected = Vec::new();
    for transition in transitions {
        if let Some(guard) = &transition.guard
            && guard.evaluate(variables)?
        {
            selected.push(transition.target.clone());
        }
    }
    if selected.is_empty()
        && let Some(default) = transitions
            .iter()
            .find(|transition| transition.guard.is_none())
    {
        selected.push(default.target.clone());
    }
    if selected.is_empty() {
        Err(DomainError::NoGatewayBranch(gateway_id.clone()))
    } else {
        Ok(selected)
    }
}

fn evaluate_decision_table(
    table: &DecisionTable,
    variables: &BTreeMap<String, WorkflowValue>,
) -> Result<BTreeMap<String, WorkflowValue>, DomainError> {
    let mut selected = None;
    for rule in &table.rules {
        if decision_rule_matches(table, rule, variables)? {
            match table.hit_policy {
                HitPolicy::First => {
                    selected = Some(rule);
                    break;
                }
                HitPolicy::Unique if selected.is_none() => selected = Some(rule),
                HitPolicy::Unique => {
                    return Err(DomainError::AmbiguousDecisionTable(table.id.clone()));
                }
            }
        }
    }
    let rule = selected.ok_or_else(|| DomainError::NoDecisionRuleMatched(table.id.clone()))?;
    Ok(table
        .outputs
        .iter()
        .zip(&rule.output_values)
        .map(|(output, value)| (output.name.clone(), value.clone()))
        .collect())
}

fn decision_rule_matches(
    table: &DecisionTable,
    rule: &DecisionRule,
    variables: &BTreeMap<String, WorkflowValue>,
) -> Result<bool, DomainError> {
    for (input, test) in table.inputs.iter().zip(&rule.input_tests) {
        let value =
            variables
                .get(&input.name)
                .ok_or_else(|| DomainError::DecisionInputMissing {
                    table_id: table.id.clone(),
                    input: input.name.clone(),
                })?;
        if !input.value_type.matches(value) {
            return Err(DomainError::DecisionInputTypeMismatch {
                table_id: table.id.clone(),
                input: input.name.clone(),
                actual: value.type_name(),
                expected: input.value_type.type_name(),
            });
        }
        if !test.matches(value) {
            return Ok(false);
        }
    }
    Ok(true)
}

impl UnaryTest {
    fn matches(&self, value: &WorkflowValue) -> bool {
        match self {
            Self::Any => true,
            Self::Equal(expected) => value == expected,
            Self::IntegerInterval(interval) => {
                let WorkflowValue::Integer(value) = value else {
                    return false;
                };
                interval.lower.is_none_or(|lower| *value >= lower)
                    && interval.upper.is_none_or(|upper| *value <= upper)
            }
        }
    }
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

impl WorkflowValueType {
    const fn matches(self, value: &WorkflowValue) -> bool {
        matches!(
            (self, value),
            (Self::Boolean, WorkflowValue::Boolean(_))
                | (Self::Integer, WorkflowValue::Integer(_))
                | (Self::String, WorkflowValue::String(_))
        )
    }

    const fn type_name(self) -> &'static str {
        match self {
            Self::Boolean => "boolean",
            Self::Integer => "integer",
            Self::String => "string",
        }
    }
}

pub fn evolve(mut state: InstanceState, event: &DomainEvent) -> InstanceState {
    state.sequence = state.sequence.saturating_add(1);
    state.lifecycle = match event {
        DomainEvent::WorkflowStarted { start_node_id, .. } => Lifecycle::Active {
            active_node: start_node_id.clone(),
        },
        DomainEvent::ServiceTaskActivated { node_id, .. } => {
            let count = state.active_tokens.entry(node_id.clone()).or_default();
            *count = count.saturating_add(1);
            Lifecycle::Active {
                active_node: state
                    .active_tokens
                    .keys()
                    .next()
                    .cloned()
                    .unwrap_or_else(|| node_id.clone()),
            }
        }
        DomainEvent::ServiceTaskCompleted { node_id, .. } => {
            if let Some(count) = state.active_tokens.get_mut(node_id) {
                *count = count.saturating_sub(1);
                if *count == 0 {
                    state.active_tokens.remove(node_id);
                }
            }
            Lifecycle::Active {
                active_node: state
                    .active_tokens
                    .keys()
                    .next()
                    .cloned()
                    .unwrap_or_else(|| node_id.clone()),
            }
        }
        DomainEvent::DecisionTaskEvaluated { outputs, .. } => {
            state.variables.extend(outputs.clone());
            state.lifecycle.clone()
        }
        DomainEvent::GatewaySplitActivated {
            join_gateway_id,
            selected_targets,
            ..
        } => {
            state.pending_gateway_joins.insert(
                join_gateway_id.clone(),
                PendingGatewayJoin {
                    expected_tokens: u32::try_from(selected_targets.len()).unwrap_or(u32::MAX),
                    arrived_tokens: 0,
                },
            );
            state.lifecycle.clone()
        }
        DomainEvent::GatewayTokenArrived { gateway_id, .. } => {
            if let Some(join) = state.pending_gateway_joins.get_mut(gateway_id) {
                join.arrived_tokens = join.arrived_tokens.saturating_add(1);
            }
            Lifecycle::Active {
                active_node: gateway_id.clone(),
            }
        }
        DomainEvent::GatewayJoined { gateway_id, .. } => {
            state.pending_gateway_joins.remove(gateway_id);
            Lifecycle::Active {
                active_node: gateway_id.clone(),
            }
        }
        DomainEvent::WorkflowCompleted { .. } => {
            state.active_tokens.clear();
            state.pending_gateway_joins.clear();
            Lifecycle::Completed
        }
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
    #[error("command tenant {actual} does not match workflow definition tenant {expected}")]
    TenantMismatch {
        expected: TenantId,
        actual: TenantId,
    },
    #[error("node {0} is not a service task")]
    NodeIsNotServiceTask(NodeId),
    #[error("requested task {0} is not active")]
    TaskNotActive(NodeId),
    #[error("exclusive gateway {0} matched more than one branch")]
    AmbiguousGateway(NodeId),
    #[error("exclusive gateway {0} has no matching branch and no default")]
    NoGatewayBranch(NodeId),
    #[error("decision table {0} is missing from workflow definition")]
    MissingDecisionTable(String),
    #[error("workflow contains duplicate decision table")]
    DuplicateDecisionTable,
    #[error("decision table {table_id} is invalid: {detail}")]
    InvalidDecisionTable { table_id: String, detail: String },
    #[error("decision table {0} matched more than one rule")]
    AmbiguousDecisionTable(String),
    #[error("decision table {0} matched no rule")]
    NoDecisionRuleMatched(String),
    #[error("decision table {table_id} input {input} is missing")]
    DecisionInputMissing { table_id: String, input: String },
    #[error("decision table {table_id} input {input} has type {actual}, expected {expected}")]
    DecisionInputTypeMismatch {
        table_id: String,
        input: String,
        actual: &'static str,
        expected: &'static str,
    },
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
    #[error("gateway {gateway} does not form a reciprocal pair with {paired}")]
    InvalidGatewayPair { gateway: NodeId, paired: NodeId },
    #[error("gateway join {0} is already pending")]
    JoinAlreadyPending(NodeId),
    #[error("token reached gateway join {0} without a matching split obligation")]
    UnexpectedGatewayJoin(NodeId),
    #[error("token count overflow at node {0}")]
    TokenCountOverflow(NodeId),
    #[error("end node {0} was reached while tokens or joins remain active")]
    EndReachedWithOutstandingTokens(NodeId),
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
            id(TenantId::new, "tenant-a"),
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
            id(TenantId::new, "tenant-a"),
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
                tenant_id: id(TenantId::new, "tenant-a"),
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
                    tenant_id: id(TenantId::new, "tenant-a"),
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
            tenant_id: id(TenantId::new, "tenant-a"),
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
            id(TenantId::new, "tenant-a"),
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
                tenant_id: id(TenantId::new, "tenant-a"),
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

    #[test]
    #[allow(clippy::too_many_lines)]
    fn decision_task_evaluates_table_and_persists_outputs_for_replay() {
        let start = id(NodeId::new, "start");
        let decision = id(NodeId::new, "risk");
        let gateway = id(NodeId::new, "route");
        let approved = id(NodeId::new, "approved");
        let rejected = id(NodeId::new, "rejected");
        let definition = WorkflowDefinition::new_with_decisions(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "routing"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: decision.clone(),
                    },
                ),
                (
                    decision,
                    Node::DecisionTask {
                        decision_table_id: "risk-table".into(),
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
                        task_type: id(TaskType::new, "manual-review"),
                        next: rejected.clone(),
                    },
                ),
                (rejected, Node::End),
            ],
            [DecisionTable {
                id: "risk-table".into(),
                hit_policy: HitPolicy::First,
                inputs: vec![DecisionInput {
                    name: "amount".into(),
                    value_type: WorkflowValueType::Integer,
                }],
                outputs: vec![DecisionOutput {
                    name: "approved".into(),
                    value_type: WorkflowValueType::Boolean,
                }],
                rules: vec![
                    DecisionRule {
                        id: "low".into(),
                        input_tests: vec![UnaryTest::IntegerInterval(IntegerInterval {
                            lower: None,
                            upper: Some(99),
                        })],
                        output_values: vec![WorkflowValue::Boolean(false)],
                    },
                    DecisionRule {
                        id: "high".into(),
                        input_tests: vec![UnaryTest::IntegerInterval(IntegerInterval {
                            lower: Some(100),
                            upper: None,
                        })],
                        output_values: vec![WorkflowValue::Boolean(true)],
                    },
                ],
            }],
        )
        .unwrap();
        let configuration = configuration(3);
        let variables = BTreeMap::from([("amount".into(), WorkflowValue::Integer(150))]);
        let events = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
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
            DomainEvent::DecisionTaskEvaluated { outputs, .. }
                if outputs.get("approved") == Some(&WorkflowValue::Boolean(true))
        ));
        assert!(matches!(
            &events[2],
            DomainEvent::ServiceTaskActivated { node_id, .. } if node_id == &approved
        ));
        let replayed = rehydrate(None, &events);
        assert_eq!(
            replayed.variables.get("approved"),
            Some(&WorkflowValue::Boolean(true))
        );
    }

    #[test]
    fn parallel_tokens_wait_for_every_branch_and_replay_deterministically() {
        let start = id(NodeId::new, "start");
        let split = id(NodeId::new, "fork");
        let left = id(NodeId::new, "left");
        let right = id(NodeId::new, "right");
        let join = id(NodeId::new, "join");
        let end = id(NodeId::new, "end");
        let definition = WorkflowDefinition::new(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "parallel"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: split.clone(),
                    },
                ),
                (
                    split.clone(),
                    Node::ParallelSplit {
                        targets: vec![left.clone(), right.clone()],
                        join: join.clone(),
                    },
                ),
                (
                    left.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "left-task"),
                        next: join.clone(),
                    },
                ),
                (
                    right.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "right-task"),
                        next: join.clone(),
                    },
                ),
                (
                    join.clone(),
                    Node::ParallelJoin {
                        split,
                        next: end.clone(),
                    },
                ),
                (end, Node::End),
            ],
        )
        .unwrap();
        let configuration = configuration(6);
        let variables = BTreeMap::new();
        let context = DecisionContext {
            configuration: &configuration,
            variables: &variables,
        };
        let started = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 1,
            },
            context,
        )
        .unwrap();
        let after_start = rehydrate(None, &started);
        assert_eq!(after_start.active_tokens.len(), 2);
        assert_eq!(after_start.pending_gateway_joins[&join].expected_tokens, 2);

        let left_completed = decide(
            &definition,
            &after_start,
            &Command::CompleteServiceTask {
                node_id: left,
                occurred_at_epoch_ms: 2,
            },
            context,
        )
        .unwrap();
        let after_left = rehydrate(Some(after_start), &left_completed);
        assert_eq!(after_left.pending_gateway_joins[&join].arrived_tokens, 1);
        assert!(!matches!(after_left.lifecycle, Lifecycle::Completed));

        let right_completed = decide(
            &definition,
            &after_left,
            &Command::CompleteServiceTask {
                node_id: right,
                occurred_at_epoch_ms: 3,
            },
            context,
        )
        .unwrap();
        let history = [started, left_completed, right_completed].concat();
        let replayed = rehydrate(None, &history);
        assert_eq!(replayed.lifecycle, Lifecycle::Completed);
        assert_eq!(replayed, rehydrate(None, &history));
    }

    #[test]
    fn inclusive_split_persists_only_selected_branch_obligations() {
        let start = id(NodeId::new, "start");
        let split = id(NodeId::new, "inclusive-fork");
        let left = id(NodeId::new, "left");
        let right = id(NodeId::new, "right");
        let fallback = id(NodeId::new, "fallback");
        let join = id(NodeId::new, "inclusive-join");
        let end = id(NodeId::new, "end");
        let task = |task_type: &str| Node::ServiceTask {
            task_type: id(TaskType::new, task_type),
            next: join.clone(),
        };
        let definition = WorkflowDefinition::new(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "inclusive"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: split.clone(),
                    },
                ),
                (
                    split.clone(),
                    Node::InclusiveSplit {
                        transitions: vec![
                            GuardedTransition {
                                target: left.clone(),
                                guard: Some(GuardExpression {
                                    variable: "left".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(true),
                                }),
                            },
                            GuardedTransition {
                                target: right.clone(),
                                guard: Some(GuardExpression {
                                    variable: "right".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(true),
                                }),
                            },
                            GuardedTransition {
                                target: fallback.clone(),
                                guard: None,
                            },
                        ],
                        coverage: None,
                        join: join.clone(),
                    },
                ),
                (left.clone(), task("left-task")),
                (right.clone(), task("right-task")),
                (fallback.clone(), task("fallback-task")),
                (
                    join.clone(),
                    Node::InclusiveJoin {
                        split,
                        next: end.clone(),
                    },
                ),
                (end, Node::End),
            ],
        )
        .unwrap();
        let configuration = configuration(6);
        let variables = BTreeMap::from([
            ("left".into(), WorkflowValue::Boolean(true)),
            ("right".into(), WorkflowValue::Boolean(true)),
        ]);
        let events = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 1,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &variables,
            },
        )
        .unwrap();
        assert!(matches!(
            &events[1],
            DomainEvent::GatewaySplitActivated { selected_targets, .. }
                if selected_targets == &vec![left.clone(), right.clone()]
        ));
        let state = rehydrate(None, &events);
        assert_eq!(state.pending_gateway_joins[&join].expected_tokens, 2);
        assert!(!state.active_tokens.contains_key(&fallback));
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
            id(TenantId::new, "tenant-a"),
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
                &Command::StartWorkflow {
                    tenant_id: id(TenantId::new, "tenant-a"),
                    occurred_at_epoch_ms: started_at,
                },
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
                &Command::StartWorkflow {
                    tenant_id: id(TenantId::new, "tenant-a"),
                    occurred_at_epoch_ms: 1,
                },
                DecisionContext {
                    configuration: &configuration,
                    variables: &variables,
                },
            );

            prop_assert_eq!(result.is_ok(), limit >= 2);
        }
    }
}
