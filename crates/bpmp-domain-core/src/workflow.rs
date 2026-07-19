use std::collections::{BTreeMap, BTreeSet, VecDeque};

use thiserror::Error;

use crate::{
    NodeId, ResolvedConfigSnapshot, ScopeInstanceId, TaskType, TenantId, WorkflowType,
    WorkflowVersion,
};

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
    CallActivity {
        called_workflow: WorkflowType,
        called_version: Option<WorkflowVersion>,
        next: NodeId,
    },
    SubProcess {
        start: NodeId,
        end: NodeId,
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
    pub expression: Option<BooleanExpression>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum BooleanExpression {
    Comparison(GuardExpression),
    Conjunction(Vec<Self>),
    Disjunction(Vec<Self>),
    Negation(Box<Self>),
    Constant(bool),
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
    List(Vec<Self>),
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
    Symbolic,
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
    boundary_events: BTreeMap<NodeId, Vec<BoundaryEventDefinition>>,
    node_metadata: BTreeMap<NodeId, NodeExecutionMetadata>,
    properties: Vec<ExtensionProperty>,
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

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct BoundaryEventDefinition {
    pub id: NodeId,
    pub cancel_activity: bool,
    pub target: NodeId,
    pub trigger: BoundaryTrigger,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum BoundaryTrigger {
    Timer {
        kind: BoundaryTimerKind,
        expression: String,
    },
    Error {
        error_ref: Option<String>,
    },
    Message {
        message_ref: String,
    },
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum BoundaryTimerKind {
    Date,
    Duration,
    Cycle,
}

#[derive(Debug, Clone, Eq, PartialEq, Default)]
pub struct WorkflowExecutionContracts {
    pub boundary_events: Vec<(NodeId, BoundaryEventDefinition)>,
    pub node_metadata: Vec<(NodeId, NodeExecutionMetadata)>,
    pub properties: Vec<ExtensionProperty>,
}

#[derive(Debug, Clone, Eq, PartialEq, Default)]
pub struct NodeExecutionMetadata {
    pub multi_instance: Option<MultiInstanceDefinition>,
    pub properties: Vec<ExtensionProperty>,
    pub owner_scope_id: Option<NodeId>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct MultiInstanceDefinition {
    pub mode: MultiInstanceMode,
    pub collection_expression: Option<String>,
    pub item_variable: Option<String>,
    pub cardinality_expression: Option<String>,
    pub max_parallelism: Option<u32>,
    pub completion_condition: Option<BooleanExpression>,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum MultiInstanceMode {
    Sequential,
    Parallel,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ActiveMultiInstance {
    pub task_type: TaskType,
    pub mode: MultiInstanceMode,
    pub total_instances: u32,
    pub next_iteration: u32,
    pub max_parallelism: u32,
    pub item_variable: Option<String>,
    pub items: Vec<WorkflowValue>,
    pub active_iterations: BTreeSet<u32>,
    pub completed_iterations: BTreeSet<u32>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ActiveBoundarySubscription {
    pub attached_node_id: NodeId,
    pub target_node_id: NodeId,
    pub cancel_activity: bool,
    pub trigger: BoundaryTrigger,
    pub armed_at_epoch_ms: u64,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ActiveExecutionScope {
    pub scope_node_id: NodeId,
    pub start_node_id: NodeId,
    pub parent_scope_instance_id: Option<ScopeInstanceId>,
    pub invocation: u64,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ExtensionProperty {
    pub namespace_uri: String,
    pub element_name: String,
    pub name: String,
    pub value: ExtensionPropertyValue,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum ExtensionPropertyValue {
    String(String),
    Integer(i64),
    Boolean(bool),
    DurationMilliseconds(u64),
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
        Self::new_with_execution_contracts(
            tenant_id,
            workflow_type,
            workflow_version,
            start_node,
            nodes,
            decision_tables,
            WorkflowExecutionContracts::default(),
        )
    }

    /// Builds a workflow definition with embedded decisions and boundary events.
    ///
    /// # Errors
    ///
    /// Returns a [`DomainError`] for invalid graph, decision, owner, or target data.
    #[allow(clippy::too_many_arguments)]
    pub fn new_with_execution_contracts(
        tenant_id: TenantId,
        workflow_type: WorkflowType,
        workflow_version: WorkflowVersion,
        start_node: NodeId,
        nodes: impl IntoIterator<Item = (NodeId, Node)>,
        decision_tables: impl IntoIterator<Item = DecisionTable>,
        contracts: WorkflowExecutionContracts,
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
                let enters_owned_start = matches!(
                    node,
                    Node::SubProcess { start, .. } if start == next
                );
                if matches!(indexed.get(next), Some(Node::Start { .. })) && !enters_owned_start {
                    return Err(DomainError::TransitionToStartNode(next.clone()));
                }
            }
        }

        let mut indexed_boundaries = BTreeMap::<NodeId, Vec<BoundaryEventDefinition>>::new();
        let mut boundary_ids = BTreeSet::new();
        for (owner, boundary) in contracts.boundary_events {
            if !indexed.contains_key(&owner) {
                return Err(DomainError::UnknownBoundaryOwner(owner));
            }
            if !indexed.contains_key(&boundary.target) {
                return Err(DomainError::MissingTransitionTarget {
                    source_node: owner,
                    target: boundary.target,
                });
            }
            if !boundary_ids.insert(boundary.id.clone()) {
                return Err(DomainError::DuplicateBoundaryEvent(boundary.id));
            }
            indexed_boundaries.entry(owner).or_default().push(boundary);
        }
        let mut indexed_metadata = BTreeMap::new();
        for (owner, metadata) in contracts.node_metadata {
            if !indexed.contains_key(&owner) {
                return Err(DomainError::UnknownNodeMetadataOwner(owner));
            }
            if metadata.multi_instance.is_some()
                && !matches!(
                    indexed.get(&owner),
                    Some(Node::ServiceTask { .. } | Node::CallActivity { .. })
                )
            {
                return Err(DomainError::InvalidMultiInstance {
                    node: owner,
                    detail: "runtime multi-instance is supported only for service tasks and call activities".into(),
                });
            }
            validate_node_metadata(&owner, &metadata)?;
            if indexed_metadata.insert(owner.clone(), metadata).is_some() {
                return Err(DomainError::DuplicateNodeMetadata(owner));
            }
        }
        validate_scope_ownership(&indexed, &indexed_metadata)?;
        validate_extension_properties(&contracts.properties)?;
        let definition = Self {
            tenant_id,
            workflow_type,
            workflow_version,
            start_node,
            nodes: indexed,
            decision_tables: indexed_tables,
            boundary_events: indexed_boundaries,
            node_metadata: indexed_metadata,
            properties: contracts.properties,
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
            if let Some(boundaries) = self.boundary_events.get(&node_id) {
                pending.extend(boundaries.iter().map(|boundary| boundary.target.clone()));
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

    pub fn node_execution_metadata(&self, node_id: &NodeId) -> Option<&NodeExecutionMetadata> {
        self.node_metadata.get(node_id)
    }

    fn owner_scope_id(&self, node_id: &NodeId) -> Option<&NodeId> {
        self.node_metadata
            .get(node_id)
            .and_then(|metadata| metadata.owner_scope_id.as_ref())
    }

    fn sub_process_exit(&self, scope_node_id: &NodeId) -> Result<&NodeId, DomainError> {
        match self.node(scope_node_id)? {
            Node::SubProcess { next, .. } => Ok(next),
            _ => Err(DomainError::InvalidScopeOwner(scope_node_id.clone())),
        }
    }

    fn active_scope_for_node(
        &self,
        state: &InstanceState,
        node_id: &NodeId,
    ) -> Result<Option<ScopeInstanceId>, DomainError> {
        let Some(owner_scope_id) = self.owner_scope_id(node_id) else {
            return Ok(None);
        };
        let mut matches = state
            .active_scopes
            .iter()
            .filter(|(_, scope)| &scope.scope_node_id == owner_scope_id)
            .map(|(scope_instance_id, _)| scope_instance_id.clone());
        let found = matches.next();
        if found.is_some() && matches.next().is_some() {
            return Err(DomainError::AmbiguousActiveScope(owner_scope_id.clone()));
        }
        found
            .map(Some)
            .ok_or_else(|| DomainError::ScopeNotActive(owner_scope_id.clone()))
    }

    pub fn properties(&self) -> &[ExtensionProperty] {
        &self.properties
    }

    pub fn boundary_events(&self, node_id: &NodeId) -> &[BoundaryEventDefinition] {
        self.boundary_events.get(node_id).map_or(&[], Vec::as_slice)
    }

    fn boundary_event(
        &self,
        boundary_event_id: &NodeId,
    ) -> Option<(&NodeId, &BoundaryEventDefinition)> {
        self.boundary_events.iter().find_map(|(owner, boundaries)| {
            boundaries
                .iter()
                .find(|boundary| &boundary.id == boundary_event_id)
                .map(|boundary| (owner, boundary))
        })
    }
}

fn validate_node_metadata(
    owner: &NodeId,
    metadata: &NodeExecutionMetadata,
) -> Result<(), DomainError> {
    validate_extension_properties(&metadata.properties)?;
    let Some(spec) = &metadata.multi_instance else {
        return Ok(());
    };
    if spec.collection_expression.is_none() && spec.cardinality_expression.is_none() {
        return Err(DomainError::InvalidMultiInstance {
            node: owner.clone(),
            detail: "collection or cardinality expression is required".into(),
        });
    }
    if spec.collection_expression.is_some() && spec.item_variable.is_none() {
        return Err(DomainError::InvalidMultiInstance {
            node: owner.clone(),
            detail: "collection iteration requires an item variable".into(),
        });
    }
    if spec.max_parallelism == Some(0) {
        return Err(DomainError::InvalidMultiInstance {
            node: owner.clone(),
            detail: "max parallelism must be greater than zero".into(),
        });
    }
    Ok(())
}

fn validate_scope_ownership(
    nodes: &BTreeMap<NodeId, Node>,
    metadata: &BTreeMap<NodeId, NodeExecutionMetadata>,
) -> Result<(), DomainError> {
    for (node_id, node_metadata) in metadata {
        if let Some(owner) = &node_metadata.owner_scope_id
            && !matches!(nodes.get(owner), Some(Node::SubProcess { .. }))
        {
            return Err(DomainError::InvalidScopeOwner(owner.clone()));
        }
        if node_metadata.owner_scope_id.as_ref() == Some(node_id) {
            return Err(DomainError::RecursiveScopeOwnership(node_id.clone()));
        }
    }
    for (scope_node_id, node) in nodes {
        let Node::SubProcess { start, end, .. } = node else {
            continue;
        };
        for child in [start, end] {
            if metadata
                .get(child)
                .and_then(|item| item.owner_scope_id.as_ref())
                != Some(scope_node_id)
            {
                return Err(DomainError::ScopeBoundaryOwnershipMismatch {
                    scope: scope_node_id.clone(),
                    child: child.clone(),
                });
            }
        }
    }
    Ok(())
}

fn validate_extension_properties(properties: &[ExtensionProperty]) -> Result<(), DomainError> {
    let mut keys = BTreeSet::new();
    for property in properties {
        if property.namespace_uri.trim().is_empty()
            || property.element_name.trim().is_empty()
            || property.name.trim().is_empty()
        {
            return Err(DomainError::InvalidExtensionProperty);
        }
        if !keys.insert((
            property.namespace_uri.as_str(),
            property.element_name.as_str(),
            property.name.as_str(),
        )) {
            return Err(DomainError::DuplicateExtensionProperty);
        }
    }
    Ok(())
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
        Node::Start { .. }
        | Node::ServiceTask { .. }
        | Node::DecisionTask { .. }
        | Node::CallActivity { .. }
        | Node::SubProcess { .. }
        | Node::End => Ok(()),
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
        .filter(|transition| transition.is_default())
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
    if matches!(coverage.domain, GatewayCoverageDomain::Symbolic) {
        if transitions.iter().any(GuardedTransition::is_default) {
            return Err(DomainError::InvalidGatewayCoverage {
                gateway: node_id.clone(),
                detail: "symbolic coverage cannot be combined with default branches".into(),
            });
        }
        if transitions
            .iter()
            .any(|transition| transition.guard.is_some() == transition.expression.is_some())
        {
            return Err(DomainError::InvalidGatewayCoverage {
                gateway: node_id.clone(),
                detail: "each symbolic branch must contain exactly one guard representation".into(),
            });
        }
        return Ok(());
    }
    if coverage.variable.trim().is_empty() {
        return Err(DomainError::InvalidGatewayCoverage {
            gateway: node_id.clone(),
            detail: "coverage variable is empty".into(),
        });
    }
    if transitions.iter().any(GuardedTransition::is_default) {
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
        GatewayCoverageDomain::Symbolic => unreachable!("handled above"),
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
            | Self::CallActivity { next, .. }
            | Self::ParallelJoin { next, .. }
            | Self::InclusiveJoin { next, .. } => Box::new(std::iter::once(next)),
            Self::SubProcess { start, next, .. } => Box::new([start, next].into_iter()),
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
    let default_count = transitions.iter().filter(|item| item.is_default()).count();
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
    CompleteMultiInstanceIteration {
        node_id: NodeId,
        iteration: u32,
        occurred_at_epoch_ms: u64,
    },
    TriggerBoundaryEvent {
        boundary_event_id: NodeId,
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
    BoundaryEventArmed {
        boundary_event_id: NodeId,
        attached_node_id: NodeId,
        target_node_id: NodeId,
        cancel_activity: bool,
        trigger: BoundaryTrigger,
        occurred_at_epoch_ms: u64,
    },
    BoundaryEventsDisarmed {
        attached_node_id: NodeId,
        boundary_event_ids: Vec<NodeId>,
        occurred_at_epoch_ms: u64,
    },
    MultiInstanceStarted {
        node_id: NodeId,
        task_type: TaskType,
        mode: MultiInstanceMode,
        total_instances: u32,
        max_parallelism: u32,
        item_variable: Option<String>,
        items: Vec<WorkflowValue>,
        occurred_at_epoch_ms: u64,
    },
    MultiInstanceIterationActivated {
        node_id: NodeId,
        task_type: TaskType,
        iteration: u32,
        item: Option<WorkflowValue>,
        occurred_at_epoch_ms: u64,
    },
    MultiInstanceIterationCompleted {
        node_id: NodeId,
        iteration: u32,
        occurred_at_epoch_ms: u64,
    },
    MultiInstanceCompleted {
        node_id: NodeId,
        completion_condition_satisfied: bool,
        cancelled_iterations: Vec<u32>,
        occurred_at_epoch_ms: u64,
    },
    BoundaryEventTriggered {
        boundary_event_id: NodeId,
        attached_node_id: NodeId,
        target_node_id: NodeId,
        cancel_activity: bool,
        cancelled_iterations: Vec<u32>,
        cancelled_task_tokens: u32,
        occurred_at_epoch_ms: u64,
    },
    ScopeEntered {
        scope_instance_id: ScopeInstanceId,
        scope_node_id: NodeId,
        start_node_id: NodeId,
        parent_scope_instance_id: Option<ScopeInstanceId>,
        invocation: u64,
        occurred_at_epoch_ms: u64,
    },
    ScopeCompleted {
        scope_instance_id: ScopeInstanceId,
        scope_node_id: NodeId,
        end_node_id: NodeId,
        occurred_at_epoch_ms: u64,
    },
    WorkflowBranchCompleted {
        end_node_id: NodeId,
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
    pub active_multi_instances: BTreeMap<NodeId, ActiveMultiInstance>,
    pub active_boundary_subscriptions: BTreeMap<NodeId, ActiveBoundarySubscription>,
    pub active_scopes: BTreeMap<ScopeInstanceId, ActiveExecutionScope>,
    pub scope_invocation_counts: BTreeMap<NodeId, u64>,
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
            active_multi_instances: BTreeMap::new(),
            active_boundary_subscriptions: BTreeMap::new(),
            active_scopes: BTreeMap::new(),
            scope_invocation_counts: BTreeMap::new(),
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
#[allow(clippy::too_many_lines)]
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
                context.configuration,
                *occurred_at_epoch_ms,
                None,
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
            let (Node::ServiceTask { next, .. } | Node::CallActivity { next, .. }) =
                definition.node(node_id)?
            else {
                return Err(DomainError::NodeIsNotServiceTask(node_id.clone()));
            };
            let mut events = vec![DomainEvent::ServiceTaskCompleted {
                node_id: node_id.clone(),
                occurred_at_epoch_ms: *occurred_at_epoch_ms,
            }];
            let mut variables = state_variables_with_context(state, context.variables);
            let mut routing = RoutingState::from(state);
            routing.complete_task(node_id)?;
            disarm_boundary_events(node_id, &mut routing, *occurred_at_epoch_ms, &mut events);
            events.extend(activation_events(
                definition,
                next,
                &mut variables,
                &mut routing,
                context.configuration,
                *occurred_at_epoch_ms,
                definition.active_scope_for_node(state, node_id)?,
            )?);
            events
        }
        (Command::CompleteServiceTask { node_id, .. }, Lifecycle::Active { .. }) => {
            return Err(DomainError::TaskNotActive(node_id.clone()));
        }
        (
            Command::CompleteMultiInstanceIteration {
                node_id,
                iteration,
                occurred_at_epoch_ms,
            },
            Lifecycle::Active { .. },
        ) => complete_multi_instance_iteration(
            definition,
            state,
            node_id,
            *iteration,
            context,
            *occurred_at_epoch_ms,
        )?,
        (
            Command::TriggerBoundaryEvent {
                boundary_event_id,
                occurred_at_epoch_ms,
            },
            Lifecycle::Active { .. },
        ) => trigger_boundary_event(
            definition,
            state,
            boundary_event_id,
            context,
            *occurred_at_epoch_ms,
        )?,
        (
            Command::CompleteServiceTask { .. }
            | Command::CompleteMultiInstanceIteration { .. }
            | Command::TriggerBoundaryEvent { .. },
            Lifecycle::Initial,
        ) => {
            return Err(DomainError::NotStarted);
        }
        (
            Command::CompleteServiceTask { .. }
            | Command::CompleteMultiInstanceIteration { .. }
            | Command::TriggerBoundaryEvent { .. },
            Lifecycle::Completed,
        ) => {
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

fn complete_multi_instance_iteration(
    definition: &WorkflowDefinition,
    state: &InstanceState,
    node_id: &NodeId,
    iteration: u32,
    context: DecisionContext<'_>,
    occurred_at_epoch_ms: u64,
) -> Result<Vec<DomainEvent>, DomainError> {
    let (Node::ServiceTask { next, .. } | Node::CallActivity { next, .. }) =
        definition.node(node_id)?
    else {
        return Err(DomainError::NodeIsNotServiceTask(node_id.clone()));
    };
    let active = state
        .active_multi_instances
        .get(node_id)
        .ok_or_else(|| DomainError::MultiInstanceNotActive(node_id.clone()))?;
    if !active.active_iterations.contains(&iteration) {
        return Err(DomainError::MultiInstanceIterationNotActive {
            node: node_id.clone(),
            iteration,
        });
    }

    let mut events = vec![DomainEvent::MultiInstanceIterationCompleted {
        node_id: node_id.clone(),
        iteration,
        occurred_at_epoch_ms,
    }];
    let mut routing = RoutingState::from(state);
    let progress = routing.complete_multi_instance_iteration(node_id, iteration)?;
    let spec = definition
        .node_execution_metadata(node_id)
        .and_then(|metadata| metadata.multi_instance.as_ref())
        .ok_or_else(|| DomainError::MultiInstanceNotActive(node_id.clone()))?;
    let completion_condition_satisfied = !progress.completed
        && evaluate_multi_instance_completion(node_id, spec, &routing, state, context.variables)?;
    if progress.completed || completion_condition_satisfied {
        let cancelled_iterations = if completion_condition_satisfied {
            routing
                .active_multi_instances
                .get(node_id)
                .map(|active| active.active_iterations.iter().copied().collect())
                .unwrap_or_default()
        } else {
            Vec::new()
        };
        routing.finish_multi_instance(node_id);
        events.push(DomainEvent::MultiInstanceCompleted {
            node_id: node_id.clone(),
            completion_condition_satisfied,
            cancelled_iterations,
            occurred_at_epoch_ms,
        });
        disarm_boundary_events(node_id, &mut routing, occurred_at_epoch_ms, &mut events);
        let mut variables = state_variables_with_context(state, context.variables);
        events.extend(activation_events(
            definition,
            next,
            &mut variables,
            &mut routing,
            context.configuration,
            occurred_at_epoch_ms,
            definition.active_scope_for_node(state, node_id)?,
        )?);
    } else {
        let active = routing
            .active_multi_instances
            .get(node_id)
            .ok_or_else(|| DomainError::MultiInstanceNotActive(node_id.clone()))?;
        if active.next_iteration < active.total_instances {
            let (next_iteration, task_type, item) =
                routing.activate_next_multi_instance(node_id)?;
            events.push(DomainEvent::MultiInstanceIterationActivated {
                node_id: node_id.clone(),
                task_type,
                iteration: next_iteration,
                item,
                occurred_at_epoch_ms,
            });
        }
    }
    Ok(events)
}

fn evaluate_multi_instance_completion(
    node_id: &NodeId,
    spec: &MultiInstanceDefinition,
    routing: &RoutingState,
    state: &InstanceState,
    context_variables: &BTreeMap<String, WorkflowValue>,
) -> Result<bool, DomainError> {
    let Some(condition) = spec.completion_condition.as_ref() else {
        return Ok(false);
    };
    let active = routing
        .active_multi_instances
        .get(node_id)
        .ok_or_else(|| DomainError::MultiInstanceNotActive(node_id.clone()))?;
    let mut variables = state_variables_with_context(state, context_variables);
    variables.insert(
        "nrOfInstances".into(),
        WorkflowValue::Integer(i64::from(active.total_instances)),
    );
    variables.insert(
        "nrOfActiveInstances".into(),
        WorkflowValue::Integer(
            i64::try_from(active.active_iterations.len())
                .map_err(|_| DomainError::TokenCountOverflow(node_id.clone()))?,
        ),
    );
    variables.insert(
        "nrOfCompletedInstances".into(),
        WorkflowValue::Integer(
            i64::try_from(active.completed_iterations.len())
                .map_err(|_| DomainError::TokenCountOverflow(node_id.clone()))?,
        ),
    );
    variables.insert("nrOfTerminatedInstances".into(), WorkflowValue::Integer(0));
    condition.evaluate(&variables)
}

fn trigger_boundary_event(
    definition: &WorkflowDefinition,
    state: &InstanceState,
    boundary_event_id: &NodeId,
    context: DecisionContext<'_>,
    occurred_at_epoch_ms: u64,
) -> Result<Vec<DomainEvent>, DomainError> {
    let subscription = state
        .active_boundary_subscriptions
        .get(boundary_event_id)
        .ok_or_else(|| DomainError::BoundaryEventNotArmed(boundary_event_id.clone()))?;
    let owner = &subscription.attached_node_id;
    let boundary = definition
        .boundary_event(boundary_event_id)
        .map(|(_, boundary)| boundary)
        .ok_or_else(|| DomainError::UnknownBoundaryEvent(boundary_event_id.clone()))?;
    if boundary.target != subscription.target_node_id
        || boundary.cancel_activity != subscription.cancel_activity
        || boundary.trigger != subscription.trigger
    {
        return Err(DomainError::BoundarySubscriptionDefinitionMismatch(
            boundary_event_id.clone(),
        ));
    }
    let normal_tokens = state.active_tokens.get(owner).copied().unwrap_or_default();
    let multi_instance = state.active_multi_instances.get(owner);
    let active_scope = state
        .active_scopes
        .values()
        .find(|scope| &scope.scope_node_id == owner);
    if normal_tokens == 0 && multi_instance.is_none() && active_scope.is_none() {
        return Err(DomainError::BoundaryOwnerNotActive(owner.clone()));
    }
    if boundary.cancel_activity && active_scope.is_some() {
        return Err(DomainError::InterruptingScopeBoundaryUnsupported(
            owner.clone(),
        ));
    }

    let cancelled_iterations = if boundary.cancel_activity {
        multi_instance
            .map(|active| active.active_iterations.iter().copied().collect())
            .unwrap_or_default()
    } else {
        Vec::new()
    };
    let cancelled_task_tokens = u32::from(boundary.cancel_activity && normal_tokens > 0);
    let mut events = vec![DomainEvent::BoundaryEventTriggered {
        boundary_event_id: boundary.id.clone(),
        attached_node_id: owner.clone(),
        target_node_id: boundary.target.clone(),
        cancel_activity: boundary.cancel_activity,
        cancelled_iterations,
        cancelled_task_tokens,
        occurred_at_epoch_ms,
    }];
    let mut routing = RoutingState::from(state);
    if boundary.cancel_activity {
        routing.cancel_activity(owner, cancelled_task_tokens)?;
    }
    let mut variables = state_variables_with_context(state, context.variables);
    events.extend(activation_events(
        definition,
        &boundary.target,
        &mut variables,
        &mut routing,
        context.configuration,
        occurred_at_epoch_ms,
        definition.active_scope_for_node(state, owner)?,
    )?);
    Ok(events)
}

#[allow(clippy::too_many_arguments)]
fn start_multi_instance(
    node_id: &NodeId,
    task_type: &TaskType,
    spec: &MultiInstanceDefinition,
    variables: &BTreeMap<String, WorkflowValue>,
    routing: &mut RoutingState,
    configuration: &ResolvedConfigSnapshot,
    occurred_at_epoch_ms: u64,
    events: &mut Vec<DomainEvent>,
) -> Result<bool, DomainError> {
    let (total_instances, items) = materialize_multi_instance(node_id, spec, variables)?;
    if total_instances > configuration.engine.max_multi_instance_cardinality {
        return Err(DomainError::MultiInstanceCardinalityExceeded {
            node: node_id.clone(),
            actual: total_instances,
            configured_limit: configuration.engine.max_multi_instance_cardinality,
        });
    }
    let max_parallelism = match spec.mode {
        MultiInstanceMode::Sequential => 1,
        MultiInstanceMode::Parallel => spec
            .max_parallelism
            .unwrap_or(configuration.engine.default_multi_instance_parallelism),
    }
    .min(total_instances.max(1));
    let active = ActiveMultiInstance {
        task_type: task_type.clone(),
        mode: spec.mode,
        total_instances,
        next_iteration: 0,
        max_parallelism,
        item_variable: spec.item_variable.clone(),
        items: items.clone(),
        active_iterations: BTreeSet::new(),
        completed_iterations: BTreeSet::new(),
    };
    routing.start_multi_instance(node_id, active)?;
    events.push(DomainEvent::MultiInstanceStarted {
        node_id: node_id.clone(),
        task_type: task_type.clone(),
        mode: spec.mode,
        total_instances,
        max_parallelism,
        item_variable: spec.item_variable.clone(),
        items,
        occurred_at_epoch_ms,
    });

    if total_instances == 0 {
        routing.finish_multi_instance(node_id);
        events.push(DomainEvent::MultiInstanceCompleted {
            node_id: node_id.clone(),
            completion_condition_satisfied: false,
            cancelled_iterations: Vec::new(),
            occurred_at_epoch_ms,
        });
        return Ok(true);
    }
    for _ in 0..max_parallelism {
        let (iteration, task_type, item) = routing.activate_next_multi_instance(node_id)?;
        events.push(DomainEvent::MultiInstanceIterationActivated {
            node_id: node_id.clone(),
            task_type,
            iteration,
            item,
            occurred_at_epoch_ms,
        });
    }
    Ok(false)
}

fn materialize_multi_instance(
    node_id: &NodeId,
    spec: &MultiInstanceDefinition,
    variables: &BTreeMap<String, WorkflowValue>,
) -> Result<(u32, Vec<WorkflowValue>), DomainError> {
    let items = if let Some(expression) = &spec.collection_expression {
        let variable = expression_variable(expression);
        match variables.get(variable) {
            Some(WorkflowValue::List(items)) if items.iter().all(WorkflowValue::is_scalar) => {
                items.clone()
            }
            Some(value) => {
                return Err(DomainError::MultiInstanceCollectionTypeMismatch {
                    node: node_id.clone(),
                    actual: value.type_name(),
                });
            }
            None => {
                return Err(DomainError::MultiInstanceInputMissing {
                    node: node_id.clone(),
                    expression: expression.clone(),
                });
            }
        }
    } else {
        Vec::new()
    };
    let collection_count =
        u32::try_from(items.len()).map_err(|_| DomainError::TokenCountOverflow(node_id.clone()))?;
    let cardinality = spec
        .cardinality_expression
        .as_deref()
        .map(|expression| resolve_cardinality(node_id, expression, variables))
        .transpose()?;
    if spec.collection_expression.is_some()
        && let Some(cardinality) = cardinality
        && cardinality != collection_count
    {
        return Err(DomainError::MultiInstanceCardinalityMismatch {
            node: node_id.clone(),
            collection_count,
            cardinality,
        });
    }
    Ok((cardinality.unwrap_or(collection_count), items))
}

fn resolve_cardinality(
    node_id: &NodeId,
    expression: &str,
    variables: &BTreeMap<String, WorkflowValue>,
) -> Result<u32, DomainError> {
    let value = expression
        .trim()
        .parse::<i64>()
        .ok()
        .or_else(|| match variables.get(expression_variable(expression)) {
            Some(WorkflowValue::Integer(value)) => Some(*value),
            _ => None,
        })
        .ok_or_else(|| DomainError::InvalidMultiInstanceCardinality {
            node: node_id.clone(),
            expression: expression.to_owned(),
        })?;
    u32::try_from(value).map_err(|_| DomainError::InvalidMultiInstanceCardinality {
        node: node_id.clone(),
        expression: expression.to_owned(),
    })
}

fn expression_variable(expression: &str) -> &str {
    let trimmed = expression.trim();
    trimmed
        .strip_prefix("${")
        .and_then(|value| value.strip_suffix('}'))
        .unwrap_or(trimmed)
        .trim()
}

fn arm_boundary_events(
    definition: &WorkflowDefinition,
    node_id: &NodeId,
    routing: &mut RoutingState,
    occurred_at_epoch_ms: u64,
    events: &mut Vec<DomainEvent>,
) -> Result<(), DomainError> {
    for boundary in definition.boundary_events(node_id) {
        routing.arm_boundary(
            boundary.id.clone(),
            ActiveBoundarySubscription {
                attached_node_id: node_id.clone(),
                target_node_id: boundary.target.clone(),
                cancel_activity: boundary.cancel_activity,
                trigger: boundary.trigger.clone(),
                armed_at_epoch_ms: occurred_at_epoch_ms,
            },
        )?;
        events.push(DomainEvent::BoundaryEventArmed {
            boundary_event_id: boundary.id.clone(),
            attached_node_id: node_id.clone(),
            target_node_id: boundary.target.clone(),
            cancel_activity: boundary.cancel_activity,
            trigger: boundary.trigger.clone(),
            occurred_at_epoch_ms,
        });
    }
    Ok(())
}

fn disarm_boundary_events(
    node_id: &NodeId,
    routing: &mut RoutingState,
    occurred_at_epoch_ms: u64,
    events: &mut Vec<DomainEvent>,
) {
    let boundary_event_ids = routing.disarm_boundaries(node_id);
    if !boundary_event_ids.is_empty() {
        events.push(DomainEvent::BoundaryEventsDisarmed {
            attached_node_id: node_id.clone(),
            boundary_event_ids,
            occurred_at_epoch_ms,
        });
    }
}

#[allow(clippy::too_many_lines)]
fn activation_events(
    definition: &WorkflowDefinition,
    node_id: &NodeId,
    variables: &mut BTreeMap<String, WorkflowValue>,
    routing: &mut RoutingState,
    configuration: &ResolvedConfigSnapshot,
    occurred_at_epoch_ms: u64,
    initial_scope_instance_id: Option<ScopeInstanceId>,
) -> Result<Vec<DomainEvent>, DomainError> {
    let mut pending = vec![(node_id.clone(), initial_scope_instance_id)];
    let mut events = Vec::new();
    let max_steps = definition.nodes.len().saturating_mul(4).max(1);
    let mut steps = 0_usize;
    while let Some((current, current_scope_instance_id)) = pending.pop() {
        steps = steps.saturating_add(1);
        if steps > max_steps {
            return Err(DomainError::AutomaticTransitionLimitExceeded(
                node_id.clone(),
            ));
        }
        match definition.node(&current)? {
            Node::ServiceTask { task_type, next } => {
                if let Some(spec) = definition
                    .node_execution_metadata(&current)
                    .and_then(|metadata| metadata.multi_instance.as_ref())
                {
                    let completed = start_multi_instance(
                        &current,
                        task_type,
                        spec,
                        variables,
                        routing,
                        configuration,
                        occurred_at_epoch_ms,
                        &mut events,
                    )?;
                    if completed {
                        pending.push((next.clone(), current_scope_instance_id.clone()));
                    } else {
                        arm_boundary_events(
                            definition,
                            &current,
                            routing,
                            occurred_at_epoch_ms,
                            &mut events,
                        )?;
                    }
                } else {
                    routing.activate_task(&current)?;
                    events.push(DomainEvent::ServiceTaskActivated {
                        node_id: current.clone(),
                        task_type: task_type.clone(),
                        occurred_at_epoch_ms,
                    });
                    arm_boundary_events(
                        definition,
                        &current,
                        routing,
                        occurred_at_epoch_ms,
                        &mut events,
                    )?;
                }
            }
            Node::CallActivity {
                called_workflow,
                called_version,
                next,
            } => {
                let task_type = TaskType::new(match called_version {
                    Some(version) => format!("call:{called_workflow}@{version}"),
                    None => format!("call:{called_workflow}"),
                })
                .expect("validated call activity produces a non-empty task type");
                if let Some(spec) = definition
                    .node_execution_metadata(&current)
                    .and_then(|metadata| metadata.multi_instance.as_ref())
                {
                    let completed = start_multi_instance(
                        &current,
                        &task_type,
                        spec,
                        variables,
                        routing,
                        configuration,
                        occurred_at_epoch_ms,
                        &mut events,
                    )?;
                    if completed {
                        pending.push((next.clone(), current_scope_instance_id.clone()));
                    } else {
                        arm_boundary_events(
                            definition,
                            &current,
                            routing,
                            occurred_at_epoch_ms,
                            &mut events,
                        )?;
                    }
                } else {
                    routing.activate_task(&current)?;
                    events.push(DomainEvent::ServiceTaskActivated {
                        node_id: current.clone(),
                        task_type,
                        occurred_at_epoch_ms,
                    });
                    arm_boundary_events(
                        definition,
                        &current,
                        routing,
                        occurred_at_epoch_ms,
                        &mut events,
                    )?;
                }
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
                pending.push((next.clone(), current_scope_instance_id.clone()));
            }
            Node::End => {
                if let Some(owner_scope_id) = definition.owner_scope_id(&current) {
                    let scope_instance_id = current_scope_instance_id
                        .ok_or_else(|| DomainError::ScopeContextMissing(current.clone()))?;
                    let completed_scope =
                        routing.complete_scope(&scope_instance_id, owner_scope_id)?;
                    events.push(DomainEvent::ScopeCompleted {
                        scope_instance_id,
                        scope_node_id: owner_scope_id.clone(),
                        end_node_id: current,
                        occurred_at_epoch_ms,
                    });
                    disarm_boundary_events(
                        owner_scope_id,
                        routing,
                        occurred_at_epoch_ms,
                        &mut events,
                    );
                    pending.push((
                        definition.sub_process_exit(owner_scope_id)?.clone(),
                        completed_scope.parent_scope_instance_id,
                    ));
                } else if routing.has_outstanding_work() {
                    events.push(DomainEvent::WorkflowBranchCompleted {
                        end_node_id: current,
                        occurred_at_epoch_ms,
                    });
                } else {
                    events.push(DomainEvent::WorkflowCompleted {
                        occurred_at_epoch_ms,
                    });
                }
            }
            Node::Start { next } => {
                if definition.owner_scope_id(&current).is_none()
                    || current_scope_instance_id.is_none()
                {
                    return Err(DomainError::TransitionToStartNode(current));
                }
                pending.push((next.clone(), current_scope_instance_id));
            }
            Node::ExclusiveGateway { transitions, .. } => {
                pending.push((
                    select_transition(&current, transitions, variables)?.clone(),
                    current_scope_instance_id,
                ));
            }
            Node::ParallelSplit { targets, join } => {
                routing.open_join(join, targets.len())?;
                events.push(DomainEvent::GatewaySplitActivated {
                    gateway_id: current,
                    join_gateway_id: join.clone(),
                    selected_targets: targets.clone(),
                    occurred_at_epoch_ms,
                });
                pending.extend(
                    targets
                        .iter()
                        .rev()
                        .cloned()
                        .map(|target| (target, current_scope_instance_id.clone())),
                );
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
                pending.extend(
                    selected
                        .into_iter()
                        .rev()
                        .map(|target| (target, current_scope_instance_id.clone())),
                );
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
                    pending.push((next.clone(), current_scope_instance_id));
                }
            }
            Node::SubProcess { start, .. } => {
                let (scope_instance_id, invocation) =
                    routing.enter_scope(&current, start, current_scope_instance_id.clone())?;
                events.push(DomainEvent::ScopeEntered {
                    scope_instance_id: scope_instance_id.clone(),
                    scope_node_id: current.clone(),
                    start_node_id: start.clone(),
                    parent_scope_instance_id: current_scope_instance_id,
                    invocation,
                    occurred_at_epoch_ms,
                });
                arm_boundary_events(
                    definition,
                    &current,
                    routing,
                    occurred_at_epoch_ms,
                    &mut events,
                )?;
                pending.push((start.clone(), Some(scope_instance_id)));
            }
        }
    }
    Ok(events)
}

#[derive(Clone)]
struct RoutingState {
    active_tokens: BTreeMap<NodeId, u32>,
    pending_joins: BTreeMap<NodeId, PendingGatewayJoin>,
    active_multi_instances: BTreeMap<NodeId, ActiveMultiInstance>,
    active_boundary_subscriptions: BTreeMap<NodeId, ActiveBoundarySubscription>,
    active_scopes: BTreeMap<ScopeInstanceId, ActiveExecutionScope>,
    scope_invocation_counts: BTreeMap<NodeId, u64>,
}

impl From<&InstanceState> for RoutingState {
    fn from(state: &InstanceState) -> Self {
        Self {
            active_tokens: state.active_tokens.clone(),
            pending_joins: state.pending_gateway_joins.clone(),
            active_multi_instances: state.active_multi_instances.clone(),
            active_boundary_subscriptions: state.active_boundary_subscriptions.clone(),
            active_scopes: state.active_scopes.clone(),
            scope_invocation_counts: state.scope_invocation_counts.clone(),
        }
    }
}

struct MultiInstanceProgress {
    completed: bool,
}

impl RoutingState {
    fn has_outstanding_work(&self) -> bool {
        !self.active_tokens.is_empty()
            || !self.pending_joins.is_empty()
            || !self.active_multi_instances.is_empty()
            || !self.active_boundary_subscriptions.is_empty()
            || !self.active_scopes.is_empty()
    }

    fn enter_scope(
        &mut self,
        scope_node_id: &NodeId,
        start_node_id: &NodeId,
        parent_scope_instance_id: Option<ScopeInstanceId>,
    ) -> Result<(ScopeInstanceId, u64), DomainError> {
        if self
            .active_scopes
            .values()
            .any(|scope| &scope.scope_node_id == scope_node_id)
        {
            return Err(DomainError::ScopeAlreadyActive(scope_node_id.clone()));
        }
        let invocation = self
            .scope_invocation_counts
            .get(scope_node_id)
            .copied()
            .unwrap_or_default()
            .checked_add(1)
            .ok_or_else(|| DomainError::ScopeInvocationOverflow(scope_node_id.clone()))?;
        let identity = parent_scope_instance_id.as_ref().map_or_else(
            || format!("{scope_node_id}#{invocation}"),
            |parent| format!("{parent}/{scope_node_id}#{invocation}"),
        );
        let scope_instance_id = ScopeInstanceId::new(identity)
            .map_err(|_| DomainError::InvalidScopeInstanceIdentity(scope_node_id.clone()))?;
        self.scope_invocation_counts
            .insert(scope_node_id.clone(), invocation);
        self.active_scopes.insert(
            scope_instance_id.clone(),
            ActiveExecutionScope {
                scope_node_id: scope_node_id.clone(),
                start_node_id: start_node_id.clone(),
                parent_scope_instance_id,
                invocation,
            },
        );
        Ok((scope_instance_id, invocation))
    }

    fn complete_scope(
        &mut self,
        scope_instance_id: &ScopeInstanceId,
        expected_scope_node_id: &NodeId,
    ) -> Result<ActiveExecutionScope, DomainError> {
        let scope = self
            .active_scopes
            .remove(scope_instance_id)
            .ok_or_else(|| DomainError::ScopeInstanceNotActive(scope_instance_id.clone()))?;
        if &scope.scope_node_id != expected_scope_node_id {
            return Err(DomainError::ScopeInstanceOwnerMismatch {
                scope_instance_id: scope_instance_id.clone(),
                expected: expected_scope_node_id.clone(),
                actual: scope.scope_node_id,
            });
        }
        Ok(scope)
    }

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

    fn start_multi_instance(
        &mut self,
        node_id: &NodeId,
        state: ActiveMultiInstance,
    ) -> Result<(), DomainError> {
        if self
            .active_multi_instances
            .insert(node_id.clone(), state)
            .is_some()
        {
            return Err(DomainError::MultiInstanceAlreadyActive(node_id.clone()));
        }
        Ok(())
    }

    fn activate_next_multi_instance(
        &mut self,
        node_id: &NodeId,
    ) -> Result<(u32, TaskType, Option<WorkflowValue>), DomainError> {
        let active = self
            .active_multi_instances
            .get_mut(node_id)
            .ok_or_else(|| DomainError::MultiInstanceNotActive(node_id.clone()))?;
        if active.next_iteration >= active.total_instances
            || active.active_iterations.len() >= active.max_parallelism as usize
        {
            return Err(DomainError::MultiInstanceActivationLimit(node_id.clone()));
        }
        let iteration = active.next_iteration;
        active.next_iteration = active
            .next_iteration
            .checked_add(1)
            .ok_or_else(|| DomainError::TokenCountOverflow(node_id.clone()))?;
        active.active_iterations.insert(iteration);
        let item = active.items.get(iteration as usize).cloned();
        Ok((iteration, active.task_type.clone(), item))
    }

    fn complete_multi_instance_iteration(
        &mut self,
        node_id: &NodeId,
        iteration: u32,
    ) -> Result<MultiInstanceProgress, DomainError> {
        let active = self
            .active_multi_instances
            .get_mut(node_id)
            .ok_or_else(|| DomainError::MultiInstanceNotActive(node_id.clone()))?;
        if !active.active_iterations.remove(&iteration) {
            return Err(DomainError::MultiInstanceIterationNotActive {
                node: node_id.clone(),
                iteration,
            });
        }
        active.completed_iterations.insert(iteration);
        let completed = active.completed_iterations.len() == active.total_instances as usize;
        if completed {
            return Ok(MultiInstanceProgress { completed: true });
        }
        Ok(MultiInstanceProgress { completed: false })
    }

    fn finish_multi_instance(&mut self, node_id: &NodeId) {
        self.active_multi_instances.remove(node_id);
    }

    fn cancel_activity(
        &mut self,
        node_id: &NodeId,
        cancelled_task_tokens: u32,
    ) -> Result<(), DomainError> {
        self.active_multi_instances.remove(node_id);
        self.disarm_boundaries(node_id);
        for _ in 0..cancelled_task_tokens {
            self.complete_task(node_id)?;
        }
        Ok(())
    }

    fn arm_boundary(
        &mut self,
        boundary_event_id: NodeId,
        subscription: ActiveBoundarySubscription,
    ) -> Result<(), DomainError> {
        if self
            .active_boundary_subscriptions
            .insert(boundary_event_id.clone(), subscription)
            .is_some()
        {
            return Err(DomainError::BoundaryEventAlreadyArmed(boundary_event_id));
        }
        Ok(())
    }

    fn disarm_boundaries(&mut self, node_id: &NodeId) -> Vec<NodeId> {
        let boundary_event_ids = self
            .active_boundary_subscriptions
            .iter()
            .filter(|(_, subscription)| &subscription.attached_node_id == node_id)
            .map(|(boundary_event_id, _)| boundary_event_id.clone())
            .collect::<Vec<_>>();
        for boundary_event_id in &boundary_event_ids {
            self.active_boundary_subscriptions.remove(boundary_event_id);
        }
        boundary_event_ids
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
        if transition.is_default() {
            continue;
        }
        if transition.evaluate(variables)? {
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
                .find(|transition| transition.is_default())
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
        if !transition.is_default() && transition.evaluate(variables)? {
            selected.push(transition.target.clone());
        }
    }
    if selected.is_empty()
        && let Some(default) = transitions
            .iter()
            .find(|transition| transition.is_default())
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

impl GuardedTransition {
    fn is_default(&self) -> bool {
        self.guard.is_none() && self.expression.is_none()
    }

    fn evaluate(&self, variables: &BTreeMap<String, WorkflowValue>) -> Result<bool, DomainError> {
        match (&self.guard, &self.expression) {
            (Some(guard), None) => guard.evaluate(variables),
            (None, Some(expression)) => expression.evaluate(variables),
            (None, None) => Ok(true),
            (Some(_), Some(_)) => Err(DomainError::ConflictingGuardRepresentations),
        }
    }
}

impl BooleanExpression {
    fn evaluate(&self, variables: &BTreeMap<String, WorkflowValue>) -> Result<bool, DomainError> {
        match self {
            Self::Comparison(guard) => guard.evaluate(variables),
            Self::Conjunction(operands) => {
                for operand in operands {
                    if !operand.evaluate(variables)? {
                        return Ok(false);
                    }
                }
                Ok(true)
            }
            Self::Disjunction(operands) => {
                for operand in operands {
                    if operand.evaluate(variables)? {
                        return Ok(true);
                    }
                }
                Ok(false)
            }
            Self::Negation(operand) => Ok(!operand.evaluate(variables)?),
            Self::Constant(value) => Ok(*value),
        }
    }
}

impl WorkflowValue {
    const fn is_scalar(&self) -> bool {
        !matches!(self, Self::List(_))
    }

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
            Self::List(_) => "list",
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

#[allow(clippy::too_many_lines)]
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
        DomainEvent::BoundaryEventArmed {
            boundary_event_id,
            attached_node_id,
            target_node_id,
            cancel_activity,
            trigger,
            occurred_at_epoch_ms,
        } => {
            state.active_boundary_subscriptions.insert(
                boundary_event_id.clone(),
                ActiveBoundarySubscription {
                    attached_node_id: attached_node_id.clone(),
                    target_node_id: target_node_id.clone(),
                    cancel_activity: *cancel_activity,
                    trigger: trigger.clone(),
                    armed_at_epoch_ms: *occurred_at_epoch_ms,
                },
            );
            state.lifecycle.clone()
        }
        DomainEvent::BoundaryEventsDisarmed {
            boundary_event_ids, ..
        } => {
            for boundary_event_id in boundary_event_ids {
                state
                    .active_boundary_subscriptions
                    .remove(boundary_event_id);
            }
            state.lifecycle.clone()
        }
        DomainEvent::MultiInstanceStarted {
            node_id,
            task_type,
            mode,
            total_instances,
            max_parallelism,
            item_variable,
            items,
            ..
        } => {
            state.active_multi_instances.insert(
                node_id.clone(),
                ActiveMultiInstance {
                    task_type: task_type.clone(),
                    mode: *mode,
                    total_instances: *total_instances,
                    next_iteration: 0,
                    max_parallelism: *max_parallelism,
                    item_variable: item_variable.clone(),
                    items: items.clone(),
                    active_iterations: BTreeSet::new(),
                    completed_iterations: BTreeSet::new(),
                },
            );
            Lifecycle::Active {
                active_node: node_id.clone(),
            }
        }
        DomainEvent::MultiInstanceIterationActivated {
            node_id, iteration, ..
        } => {
            if let Some(active) = state.active_multi_instances.get_mut(node_id) {
                active.active_iterations.insert(*iteration);
                active.next_iteration = active.next_iteration.max(iteration.saturating_add(1));
            }
            Lifecycle::Active {
                active_node: node_id.clone(),
            }
        }
        DomainEvent::MultiInstanceIterationCompleted {
            node_id, iteration, ..
        } => {
            if let Some(active) = state.active_multi_instances.get_mut(node_id) {
                active.active_iterations.remove(iteration);
                active.completed_iterations.insert(*iteration);
            }
            Lifecycle::Active {
                active_node: node_id.clone(),
            }
        }
        DomainEvent::MultiInstanceCompleted { node_id, .. } => {
            state.active_multi_instances.remove(node_id);
            Lifecycle::Active {
                active_node: node_id.clone(),
            }
        }
        DomainEvent::BoundaryEventTriggered {
            attached_node_id,
            target_node_id,
            cancel_activity,
            cancelled_task_tokens,
            ..
        } => {
            if *cancel_activity {
                state.active_multi_instances.remove(attached_node_id);
                state
                    .active_boundary_subscriptions
                    .retain(|_, subscription| &subscription.attached_node_id != attached_node_id);
                if let Some(count) = state.active_tokens.get_mut(attached_node_id) {
                    *count = count.saturating_sub(*cancelled_task_tokens);
                    if *count == 0 {
                        state.active_tokens.remove(attached_node_id);
                    }
                }
            }
            Lifecycle::Active {
                active_node: target_node_id.clone(),
            }
        }
        DomainEvent::ScopeEntered {
            scope_instance_id,
            scope_node_id,
            start_node_id,
            parent_scope_instance_id,
            invocation,
            ..
        } => {
            state.scope_invocation_counts.insert(
                scope_node_id.clone(),
                state
                    .scope_invocation_counts
                    .get(scope_node_id)
                    .copied()
                    .unwrap_or_default()
                    .max(*invocation),
            );
            state.active_scopes.insert(
                scope_instance_id.clone(),
                ActiveExecutionScope {
                    scope_node_id: scope_node_id.clone(),
                    start_node_id: start_node_id.clone(),
                    parent_scope_instance_id: parent_scope_instance_id.clone(),
                    invocation: *invocation,
                },
            );
            Lifecycle::Active {
                active_node: start_node_id.clone(),
            }
        }
        DomainEvent::ScopeCompleted {
            scope_instance_id,
            scope_node_id,
            ..
        } => {
            state.active_scopes.remove(scope_instance_id);
            Lifecycle::Active {
                active_node: scope_node_id.clone(),
            }
        }
        DomainEvent::WorkflowBranchCompleted { end_node_id, .. } => Lifecycle::Active {
            active_node: end_node_id.clone(),
        },
        DomainEvent::WorkflowCompleted { .. } => {
            state.active_tokens.clear();
            state.pending_gateway_joins.clear();
            state.active_multi_instances.clear();
            state.active_boundary_subscriptions.clear();
            state.active_scopes.clear();
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
    #[error("boundary event owner {0} does not exist")]
    UnknownBoundaryOwner(NodeId),
    #[error("workflow contains duplicate boundary event {0}")]
    DuplicateBoundaryEvent(NodeId),
    #[error("node metadata owner {0} does not exist")]
    UnknownNodeMetadataOwner(NodeId),
    #[error("scope owner {0} does not exist or is not a retained sub-process")]
    InvalidScopeOwner(NodeId),
    #[error("node {0} cannot own itself as an execution scope")]
    RecursiveScopeOwnership(NodeId),
    #[error("scope {scope} start/end node {child} is not owned by that scope")]
    ScopeBoundaryOwnershipMismatch { scope: NodeId, child: NodeId },
    #[error("workflow contains duplicate metadata for node {0}")]
    DuplicateNodeMetadata(NodeId),
    #[error("multi-instance definition on {node} is invalid: {detail}")]
    InvalidMultiInstance { node: NodeId, detail: String },
    #[error("extension property namespace, element, and name must not be empty")]
    InvalidExtensionProperty,
    #[error("extension property keys must be unique within their owner")]
    DuplicateExtensionProperty,
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
    #[error("multi-instance activity {0} is already active")]
    MultiInstanceAlreadyActive(NodeId),
    #[error("multi-instance activity {0} is not active")]
    MultiInstanceNotActive(NodeId),
    #[error("multi-instance iteration {iteration} on {node} is not active")]
    MultiInstanceIterationNotActive { node: NodeId, iteration: u32 },
    #[error("multi-instance activity {0} cannot activate another iteration")]
    MultiInstanceActivationLimit(NodeId),
    #[error("multi-instance input {expression} on {node} is missing")]
    MultiInstanceInputMissing { node: NodeId, expression: String },
    #[error(
        "multi-instance collection on {node} has type {actual}, expected list of scalar values"
    )]
    MultiInstanceCollectionTypeMismatch { node: NodeId, actual: &'static str },
    #[error(
        "multi-instance cardinality expression {expression} on {node} is not a non-negative integer"
    )]
    InvalidMultiInstanceCardinality { node: NodeId, expression: String },
    #[error(
        "multi-instance collection count {collection_count} on {node} differs from cardinality {cardinality}"
    )]
    MultiInstanceCardinalityMismatch {
        node: NodeId,
        collection_count: u32,
        cardinality: u32,
    },
    #[error(
        "multi-instance cardinality {actual} on {node} exceeds configured limit {configured_limit}"
    )]
    MultiInstanceCardinalityExceeded {
        node: NodeId,
        actual: u32,
        configured_limit: u32,
    },
    #[error("boundary event {0} does not exist")]
    UnknownBoundaryEvent(NodeId),
    #[error("boundary event {0} is not armed")]
    BoundaryEventNotArmed(NodeId),
    #[error("boundary event {0} is already armed")]
    BoundaryEventAlreadyArmed(NodeId),
    #[error("boundary event {0} subscription does not match the pinned workflow definition")]
    BoundarySubscriptionDefinitionMismatch(NodeId),
    #[error("boundary event owner {0} is not active")]
    BoundaryOwnerNotActive(NodeId),
    #[error("interrupting boundary cancellation for retained scope {0} is not enabled yet")]
    InterruptingScopeBoundaryUnsupported(NodeId),
    #[error("retained scope {0} is already active")]
    ScopeAlreadyActive(NodeId),
    #[error("retained scope {0} is not active")]
    ScopeNotActive(NodeId),
    #[error("more than one active invocation exists for retained scope {0}")]
    AmbiguousActiveScope(NodeId),
    #[error("scope context is missing while executing node {0}")]
    ScopeContextMissing(NodeId),
    #[error("scope invocation counter overflowed for {0}")]
    ScopeInvocationOverflow(NodeId),
    #[error("could not build a deterministic scope instance identity for {0}")]
    InvalidScopeInstanceIdentity(NodeId),
    #[error("scope instance {0} is not active")]
    ScopeInstanceNotActive(ScopeInstanceId),
    #[error("scope instance {scope_instance_id} belongs to {actual}, expected {expected}")]
    ScopeInstanceOwnerMismatch {
        scope_instance_id: ScopeInstanceId,
        expected: NodeId,
        actual: NodeId,
    },
    #[error("exclusive gateway {0} matched more than one branch")]
    AmbiguousGateway(NodeId),
    #[error("exclusive gateway {0} has no matching branch and no default")]
    NoGatewayBranch(NodeId),
    #[error("gateway transition contains both legacy and complex guard representations")]
    ConflictingGuardRepresentations,
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
        BoundaryRuntimePolicy, ConfigId, ConfigVersion, ConfigurationScope, EnginePolicy, KeyScope,
        LocalWasmPolicy, PolicyVersion, RetryPolicy, ScopeKind,
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
                                expression: None,
                            },
                            GuardedTransition {
                                target: rejected.clone(),
                                guard: None,
                                expression: None,
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
                                expression: None,
                            },
                            GuardedTransition {
                                target: rejected.clone(),
                                guard: Some(GuardExpression {
                                    variable: "approved".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(false),
                                }),
                                expression: None,
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
                                expression: None,
                            },
                            GuardedTransition {
                                target: rejected.clone(),
                                guard: None,
                                expression: None,
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
                                expression: None,
                            },
                            GuardedTransition {
                                target: right.clone(),
                                guard: Some(GuardExpression {
                                    variable: "right".into(),
                                    operator: ComparisonOperator::Equal,
                                    literal: WorkflowValue::Boolean(true),
                                }),
                                expression: None,
                            },
                            GuardedTransition {
                                target: fallback.clone(),
                                guard: None,
                                expression: None,
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

    fn multi_instance_definition(mode: MultiInstanceMode) -> WorkflowDefinition {
        let start = id(NodeId::new, "start");
        let task = id(NodeId::new, "notify");
        let end = id(NodeId::new, "end");
        WorkflowDefinition::new_with_execution_contracts(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "notifications"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (start, Node::Start { next: task.clone() }),
                (
                    task.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "notify-recipient"),
                        next: end.clone(),
                    },
                ),
                (end, Node::End),
            ],
            std::iter::empty(),
            WorkflowExecutionContracts {
                node_metadata: vec![(
                    task,
                    NodeExecutionMetadata {
                        multi_instance: Some(MultiInstanceDefinition {
                            mode,
                            collection_expression: Some("${recipients}".into()),
                            item_variable: Some("recipient".into()),
                            cardinality_expression: None,
                            max_parallelism: (mode == MultiInstanceMode::Parallel).then_some(2),
                            completion_condition: None,
                        }),
                        properties: Vec::new(),
                        owner_scope_id: None,
                    },
                )],
                ..WorkflowExecutionContracts::default()
            },
        )
        .unwrap()
    }

    fn cardinality_multi_instance_definition(
        cardinality: u32,
        max_parallelism: u32,
        completion_after: Option<u32>,
    ) -> WorkflowDefinition {
        let start = id(NodeId::new, "start");
        let task = id(NodeId::new, "batch-item");
        let end = id(NodeId::new, "end");
        WorkflowDefinition::new_with_execution_contracts(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "batch"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (start, Node::Start { next: task.clone() }),
                (
                    task.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "batch-item"),
                        next: end.clone(),
                    },
                ),
                (end, Node::End),
            ],
            std::iter::empty(),
            WorkflowExecutionContracts {
                node_metadata: vec![(
                    task,
                    NodeExecutionMetadata {
                        multi_instance: Some(MultiInstanceDefinition {
                            mode: MultiInstanceMode::Parallel,
                            collection_expression: None,
                            item_variable: None,
                            cardinality_expression: Some(cardinality.to_string()),
                            max_parallelism: Some(max_parallelism),
                            completion_condition: completion_after.map(|count| {
                                BooleanExpression::Comparison(GuardExpression {
                                    variable: "nrOfCompletedInstances".into(),
                                    operator: ComparisonOperator::GreaterThanOrEqual,
                                    literal: WorkflowValue::Integer(i64::from(count)),
                                })
                            }),
                        }),
                        properties: Vec::new(),
                        owner_scope_id: None,
                    },
                )],
                ..WorkflowExecutionContracts::default()
            },
        )
        .unwrap()
    }

    fn boundary_definition(multi_instance: bool, cancel_activity: bool) -> WorkflowDefinition {
        let start = id(NodeId::new, "start");
        let task = id(NodeId::new, "work");
        let normal_end = id(NodeId::new, "normal-end");
        let recovery = id(NodeId::new, "recovery");
        let recovery_end = id(NodeId::new, "recovery-end");
        let boundary = id(NodeId::new, "timeout");
        WorkflowDefinition::new_with_execution_contracts(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "boundary"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (start, Node::Start { next: task.clone() }),
                (
                    task.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "work"),
                        next: normal_end.clone(),
                    },
                ),
                (normal_end, Node::End),
                (
                    recovery.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "recover"),
                        next: recovery_end.clone(),
                    },
                ),
                (recovery_end, Node::End),
            ],
            std::iter::empty(),
            WorkflowExecutionContracts {
                boundary_events: vec![(
                    task.clone(),
                    BoundaryEventDefinition {
                        id: boundary,
                        cancel_activity,
                        target: recovery,
                        trigger: BoundaryTrigger::Timer {
                            kind: BoundaryTimerKind::Duration,
                            expression: "PT5M".into(),
                        },
                    },
                )],
                node_metadata: multi_instance
                    .then(|| {
                        (
                            task,
                            NodeExecutionMetadata {
                                multi_instance: Some(MultiInstanceDefinition {
                                    mode: MultiInstanceMode::Parallel,
                                    collection_expression: None,
                                    item_variable: None,
                                    cardinality_expression: Some("3".into()),
                                    max_parallelism: Some(2),
                                    completion_condition: None,
                                }),
                                properties: Vec::new(),
                                owner_scope_id: None,
                            },
                        )
                    })
                    .into_iter()
                    .collect(),
                properties: Vec::new(),
            },
        )
        .unwrap()
    }

    fn apply(state: &InstanceState, events: &[DomainEvent]) -> InstanceState {
        rehydrate(Some(state.clone()), events)
    }

    #[test]
    fn parallel_multi_instance_replenishes_bounded_slots_and_fans_in_durably() {
        let definition = multi_instance_definition(MultiInstanceMode::Parallel);
        let configuration = configuration(16);
        let variables = BTreeMap::from([(
            "recipients".into(),
            WorkflowValue::List(vec![
                WorkflowValue::String("a".into()),
                WorkflowValue::String("b".into()),
                WorkflowValue::String("c".into()),
            ]),
        )]);
        let start = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 10,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &variables,
            },
        )
        .unwrap();
        assert_eq!(start.len(), 4);
        assert!(matches!(
            &start[1],
            DomainEvent::MultiInstanceStarted {
                total_instances: 3,
                max_parallelism: 2,
                items,
                ..
            } if items.len() == 3
        ));
        let mut state = rehydrate(None, &start);
        let active = &state.active_multi_instances[&id(NodeId::new, "notify")];
        assert_eq!(active.active_iterations, BTreeSet::from([0, 1]));
        assert_eq!(active.next_iteration, 2);

        let complete = |state: &InstanceState, iteration| {
            decide(
                &definition,
                state,
                &Command::CompleteMultiInstanceIteration {
                    node_id: id(NodeId::new, "notify"),
                    iteration,
                    occurred_at_epoch_ms: 20 + u64::from(iteration),
                },
                DecisionContext {
                    configuration: &configuration,
                    variables: &BTreeMap::new(),
                },
            )
            .unwrap()
        };
        let completed_zero = complete(&state, 0);
        assert!(matches!(
            &completed_zero[1],
            DomainEvent::MultiInstanceIterationActivated {
                iteration: 2,
                item: Some(WorkflowValue::String(value)),
                ..
            } if value == "c"
        ));
        state = apply(&state, &completed_zero);
        state = apply(&state, &complete(&state, 1));
        let final_events = complete(&state, 2);
        assert!(matches!(
            final_events.as_slice(),
            [
                DomainEvent::MultiInstanceIterationCompleted { .. },
                DomainEvent::MultiInstanceCompleted { .. },
                DomainEvent::WorkflowCompleted { .. }
            ]
        ));
        state = apply(&state, &final_events);
        assert_eq!(state.lifecycle, Lifecycle::Completed);
        assert!(state.active_multi_instances.is_empty());
        assert_eq!(state.sequence, 10);
    }

    #[test]
    fn sequential_multi_instance_activates_exactly_one_materialized_item() {
        let definition = multi_instance_definition(MultiInstanceMode::Sequential);
        let configuration = configuration(16);
        let variables = BTreeMap::from([(
            "recipients".into(),
            WorkflowValue::List(vec![
                WorkflowValue::String("first".into()),
                WorkflowValue::String("second".into()),
            ]),
        )]);
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
        assert_eq!(events.len(), 3);
        assert!(matches!(
            &events[2],
            DomainEvent::MultiInstanceIterationActivated {
                iteration: 0,
                item: Some(WorkflowValue::String(value)),
                ..
            } if value == "first"
        ));
    }

    proptest! {
        #[test]
        fn multi_instance_incremental_evolution_equals_full_replay(
            cardinality in 1_u32..24,
            requested_parallelism in 1_u32..8,
        ) {
            let max_parallelism = requested_parallelism.min(cardinality);
            let definition = cardinality_multi_instance_definition(cardinality, max_parallelism, None);
            let configuration = configuration(64);
            let empty = BTreeMap::new();
            let mut all_events = decide(
                &definition,
                &InstanceState::default(),
                &Command::StartWorkflow {
                    tenant_id: id(TenantId::new, "tenant-a"),
                    occurred_at_epoch_ms: 1,
                },
                DecisionContext {
                    configuration: &configuration,
                    variables: &empty,
                },
            ).unwrap();
            let mut state = rehydrate(None, &all_events);
            while state.lifecycle != Lifecycle::Completed {
                let iteration = *state.active_multi_instances
                    [&id(NodeId::new, "batch-item")]
                    .active_iterations
                    .iter()
                    .next()
                    .expect("an incomplete bounded fan-out has an active iteration");
                let events = decide(
                    &definition,
                    &state,
                    &Command::CompleteMultiInstanceIteration {
                        node_id: id(NodeId::new, "batch-item"),
                        iteration,
                        occurred_at_epoch_ms: u64::from(iteration) + 2,
                    },
                    DecisionContext {
                        configuration: &configuration,
                        variables: &empty,
                    },
                ).unwrap();
                state = apply(&state, &events);
                all_events.extend(events);
            }
            prop_assert_eq!(state, rehydrate(None, &all_events));
            prop_assert_eq!(
                all_events.iter().filter(|event| matches!(
                    event,
                    DomainEvent::MultiInstanceIterationCompleted { .. }
                )).count(),
                cardinality as usize,
            );
        }
    }

    #[test]
    fn completion_condition_finishes_parallel_multi_instance_and_records_cancellation() {
        let definition = cardinality_multi_instance_definition(5, 2, Some(2));
        let configuration = configuration(16);
        let empty = BTreeMap::new();
        let started = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 1,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        let mut state = rehydrate(None, &started);
        for iteration in [0, 1] {
            let events = decide(
                &definition,
                &state,
                &Command::CompleteMultiInstanceIteration {
                    node_id: id(NodeId::new, "batch-item"),
                    iteration,
                    occurred_at_epoch_ms: u64::from(iteration) + 2,
                },
                DecisionContext {
                    configuration: &configuration,
                    variables: &empty,
                },
            )
            .unwrap();
            if iteration == 1 {
                assert!(events.iter().any(|event| matches!(
                    event,
                    DomainEvent::MultiInstanceCompleted {
                        completion_condition_satisfied: true,
                        cancelled_iterations,
                        ..
                    } if cancelled_iterations == &[2]
                )));
                assert!(!events.iter().any(|event| matches!(
                    event,
                    DomainEvent::MultiInstanceIterationActivated { iteration: 3, .. }
                )));
            }
            state = apply(&state, &events);
        }
        assert_eq!(state.lifecycle, Lifecycle::Completed);
        assert!(state.active_multi_instances.is_empty());
    }

    #[test]
    #[allow(clippy::too_many_lines)]
    fn retained_subprocess_scope_lifecycle_is_durable_and_replay_deterministic() {
        let start = id(NodeId::new, "start");
        let scope = id(NodeId::new, "review");
        let inner_start = id(NodeId::new, "review-start");
        let task = id(NodeId::new, "review-task");
        let inner_end = id(NodeId::new, "review-end");
        let end = id(NodeId::new, "end");
        let child_metadata = |owner_scope_id: NodeId| NodeExecutionMetadata {
            multi_instance: None,
            properties: Vec::new(),
            owner_scope_id: Some(owner_scope_id),
        };
        let definition = WorkflowDefinition::new_with_execution_contracts(
            id(TenantId::new, "tenant-a"),
            id(WorkflowType::new, "retained-scope"),
            id(WorkflowVersion::new, "1"),
            start.clone(),
            [
                (
                    start,
                    Node::Start {
                        next: scope.clone(),
                    },
                ),
                (
                    scope.clone(),
                    Node::SubProcess {
                        start: inner_start.clone(),
                        end: inner_end.clone(),
                        next: end.clone(),
                    },
                ),
                (inner_start.clone(), Node::Start { next: task.clone() }),
                (
                    task.clone(),
                    Node::ServiceTask {
                        task_type: id(TaskType::new, "review"),
                        next: inner_end.clone(),
                    },
                ),
                (inner_end.clone(), Node::End),
                (end, Node::End),
            ],
            std::iter::empty(),
            WorkflowExecutionContracts {
                boundary_events: Vec::new(),
                node_metadata: vec![
                    (inner_start, child_metadata(scope.clone())),
                    (task.clone(), child_metadata(scope.clone())),
                    (inner_end, child_metadata(scope.clone())),
                ],
                properties: Vec::new(),
            },
        )
        .unwrap();
        let configuration = configuration(16);
        let empty = BTreeMap::new();
        let started = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 1,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        assert!(matches!(
            &started[1],
            DomainEvent::ScopeEntered {
                scope_instance_id,
                scope_node_id,
                invocation: 1,
                ..
            } if scope_instance_id.as_str() == "review#1" && scope_node_id == &scope
        ));
        let active = rehydrate(None, &started);
        assert_eq!(active.active_scopes.len(), 1);
        assert_eq!(active.scope_invocation_counts.get(&scope), Some(&1));

        let completed = decide(
            &definition,
            &active,
            &Command::CompleteServiceTask {
                node_id: task,
                occurred_at_epoch_ms: 2,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        assert!(completed.iter().any(|event| matches!(
            event,
            DomainEvent::ScopeCompleted {
                scope_instance_id,
                scope_node_id,
                ..
            } if scope_instance_id.as_str() == "review#1" && scope_node_id == &scope
        )));
        let all_events = [started, completed].concat();
        let final_state = rehydrate(None, &all_events);
        assert_eq!(final_state.lifecycle, Lifecycle::Completed);
        assert!(final_state.active_scopes.is_empty());
        assert_eq!(final_state.scope_invocation_counts.get(&scope), Some(&1));
        assert_eq!(final_state, rehydrate(None, &all_events));
    }

    #[test]
    fn interrupting_boundary_cancels_all_active_multi_instance_iterations() {
        let definition = boundary_definition(true, true);
        let configuration = configuration(16);
        let empty = BTreeMap::new();
        let started = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 1,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        let state = rehydrate(None, &started);
        let triggered = decide(
            &definition,
            &state,
            &Command::TriggerBoundaryEvent {
                boundary_event_id: id(NodeId::new, "timeout"),
                occurred_at_epoch_ms: 2,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        assert!(matches!(
            &triggered[0],
            DomainEvent::BoundaryEventTriggered {
                cancel_activity: true,
                cancelled_iterations,
                ..
            } if cancelled_iterations == &vec![0, 1]
        ));
        let recovered = apply(&state, &triggered);
        assert!(recovered.active_multi_instances.is_empty());
        assert!(recovered.active_boundary_subscriptions.is_empty());
        assert_eq!(
            recovered.active_tokens,
            BTreeMap::from([(id(NodeId::new, "recovery"), 1)])
        );
        assert_eq!(recovered, rehydrate(None, &[started, triggered].concat()));
    }

    #[test]
    fn non_interrupting_boundary_branch_completes_without_cancelling_owner() {
        let definition = boundary_definition(false, false);
        let configuration = configuration(16);
        let empty = BTreeMap::new();
        let started = decide(
            &definition,
            &InstanceState::default(),
            &Command::StartWorkflow {
                tenant_id: id(TenantId::new, "tenant-a"),
                occurred_at_epoch_ms: 1,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        let mut state = rehydrate(None, &started);
        let triggered = decide(
            &definition,
            &state,
            &Command::TriggerBoundaryEvent {
                boundary_event_id: id(NodeId::new, "timeout"),
                occurred_at_epoch_ms: 2,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        state = apply(&state, &triggered);
        assert_eq!(state.active_tokens.len(), 2);
        assert!(
            state
                .active_boundary_subscriptions
                .contains_key(&id(NodeId::new, "timeout"))
        );
        let recovery_completed = decide(
            &definition,
            &state,
            &Command::CompleteServiceTask {
                node_id: id(NodeId::new, "recovery"),
                occurred_at_epoch_ms: 3,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        assert!(matches!(
            recovery_completed.last(),
            Some(DomainEvent::WorkflowBranchCompleted { .. })
        ));
        state = apply(&state, &recovery_completed);
        assert_eq!(state.active_tokens.len(), 1);
        let owner_completed = decide(
            &definition,
            &state,
            &Command::CompleteServiceTask {
                node_id: id(NodeId::new, "work"),
                occurred_at_epoch_ms: 4,
            },
            DecisionContext {
                configuration: &configuration,
                variables: &empty,
            },
        )
        .unwrap();
        assert!(matches!(
            owner_completed.last(),
            Some(DomainEvent::WorkflowCompleted { .. })
        ));
        state = apply(&state, &owner_completed);
        assert!(state.active_boundary_subscriptions.is_empty());
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
                max_multi_instance_cardinality: 10_000,
                default_multi_instance_parallelism: 32,
                boundary_runtime: BoundaryRuntimePolicy {
                    projection_batch_size: 128,
                    dispatch_batch_size: 32,
                    max_dispatch_attempts: 5,
                    retry_delay_ms: 1_000,
                    lease_duration_ms: 30_000,
                    max_timer_horizon_ms: 365 * 24 * 60 * 60 * 1_000,
                    max_expression_bytes: 1_024,
                    worker_id: "test-boundary-worker".into(),
                    max_signal_id_bytes: 256,
                    max_reference_bytes: 1_024,
                    max_subscriptions_per_instance: 256,
                },
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
