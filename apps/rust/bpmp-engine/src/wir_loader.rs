use bpmp_contracts::wir::v1::{
    ComparisonOperator as WireComparisonOperator, HitPolicy as WireHitPolicy,
    ValueType as WireValueType, WorkflowIntermediateRepresentation, guard_expression, node,
    unary_test, workflow_literal,
};
use bpmp_contracts::{ArtifactError, WirArtifactVerifier, WirCodec};
use bpmp_domain_core::{
    ComparisonOperator, DecisionInput, DecisionOutput, DecisionRule, DecisionTable, DomainError,
    GatewayCoverage, GatewayCoverageDomain, GuardExpression, GuardedTransition, HitPolicy,
    IdentifierError, IntegerInterval, Node, NodeId, TaskType, TenantId, UnaryTest,
    WorkflowDefinition, WorkflowType, WorkflowValue, WorkflowValueType, WorkflowVersion,
};
use thiserror::Error;

pub struct WirLoader;

impl WirLoader {
    /// Verifies and maps a canonical WIR artifact into validated engine domain data.
    ///
    /// # Errors
    ///
    /// Returns [`WirLoadError`] when integrity/schema validation fails or the
    /// decoded graph cannot construct a valid workflow definition.
    pub fn load(
        artifact: &[u8],
        verifier: &dyn WirArtifactVerifier,
    ) -> Result<WorkflowDefinition, WirLoadError> {
        let wir = WirCodec::open(artifact, verifier)?;
        map_definition(wir)
    }
}

fn map_definition(
    wir: WorkflowIntermediateRepresentation,
) -> Result<WorkflowDefinition, WirLoadError> {
    let tenant_id = TenantId::new(wir.tenant_id).map_err(|source| WirLoadError::Identifier {
        field: "tenant_id",
        source,
    })?;
    let workflow_type =
        WorkflowType::new(wir.workflow_type).map_err(|source| WirLoadError::Identifier {
            field: "workflow_type",
            source,
        })?;
    let workflow_version =
        WorkflowVersion::new(wir.workflow_version).map_err(|source| WirLoadError::Identifier {
            field: "workflow_version",
            source,
        })?;
    let start_node = NodeId::new(wir.start_node_id).map_err(|source| WirLoadError::Identifier {
        field: "start_node_id",
        source,
    })?;
    let mut nodes = Vec::with_capacity(wir.nodes.len());
    for encoded in wir.nodes {
        let node_id = NodeId::new(encoded.id).map_err(|source| WirLoadError::Identifier {
            field: "node.id",
            source,
        })?;
        let kind = match encoded
            .kind
            .ok_or_else(|| WirLoadError::MissingNodeKind(node_id.clone()))?
        {
            node::Kind::Start(start) => Node::Start {
                next: node_id_value(start.next_node_id, "start.next_node_id")?,
            },
            node::Kind::ServiceTask(task) => Node::ServiceTask {
                task_type: TaskType::new(task.task_type).map_err(|source| {
                    WirLoadError::Identifier {
                        field: "service_task.task_type",
                        source,
                    }
                })?,
                next: node_id_value(task.next_node_id, "service_task.next_node_id")?,
            },
            node::Kind::DecisionTask(task) => Node::DecisionTask {
                decision_table_id: non_empty(
                    task.decision_table_id,
                    "decision_task.decision_table_id",
                )?,
                next: node_id_value(task.next_node_id, "decision_task.next_node_id")?,
            },
            node::Kind::End(_) => Node::End,
            node::Kind::ExclusiveGateway(gateway) => Node::ExclusiveGateway {
                transitions: gateway
                    .transitions
                    .into_iter()
                    .map(map_transition)
                    .collect::<Result<_, _>>()?,
                coverage: gateway.coverage.map(map_coverage).transpose()?,
            },
        };
        nodes.push((node_id, kind));
    }
    let decision_tables = wir
        .decision_tables
        .into_iter()
        .map(map_decision_table)
        .collect::<Result<Vec<_>, _>>()?;
    WorkflowDefinition::new_with_decisions(
        tenant_id,
        workflow_type,
        workflow_version,
        start_node,
        nodes,
        decision_tables,
    )
    .map_err(WirLoadError::Domain)
}

fn map_decision_table(
    table: bpmp_contracts::wir::v1::DecisionTable,
) -> Result<DecisionTable, WirLoadError> {
    let hit_policy = match WireHitPolicy::try_from(table.hit_policy) {
        Ok(WireHitPolicy::Unique) => HitPolicy::Unique,
        Ok(WireHitPolicy::First) => HitPolicy::First,
        Ok(WireHitPolicy::Collect) => return Err(WirLoadError::UnsupportedHitPolicy("COLLECT")),
        Ok(WireHitPolicy::Unspecified) | Err(_) => {
            return Err(WirLoadError::InvalidHitPolicy(table.hit_policy));
        }
    };
    Ok(DecisionTable {
        id: non_empty(table.id, "decision_table.id")?,
        hit_policy,
        inputs: table
            .inputs
            .into_iter()
            .map(|input| {
                Ok(DecisionInput {
                    name: non_empty(input.name, "decision_input.name")?,
                    value_type: map_value_type(input.value_type)?,
                })
            })
            .collect::<Result<_, WirLoadError>>()?,
        outputs: table
            .outputs
            .into_iter()
            .map(|output| {
                Ok(DecisionOutput {
                    name: non_empty(output.name, "decision_output.name")?,
                    value_type: map_value_type(output.value_type)?,
                })
            })
            .collect::<Result<_, WirLoadError>>()?,
        rules: table
            .rules
            .into_iter()
            .map(map_decision_rule)
            .collect::<Result<_, _>>()?,
    })
}

fn map_decision_rule(
    rule: bpmp_contracts::wir::v1::DecisionRule,
) -> Result<DecisionRule, WirLoadError> {
    Ok(DecisionRule {
        id: non_empty(rule.id, "decision_rule.id")?,
        input_tests: rule
            .input_tests
            .into_iter()
            .map(map_unary_test)
            .collect::<Result<_, _>>()?,
        output_values: rule
            .output_values
            .into_iter()
            .map(map_workflow_literal)
            .collect::<Result<_, _>>()?,
    })
}

fn map_unary_test(test: bpmp_contracts::wir::v1::UnaryTest) -> Result<UnaryTest, WirLoadError> {
    match test.test.ok_or(WirLoadError::MissingUnaryTest)? {
        unary_test::Test::Any(_) => Ok(UnaryTest::Any),
        unary_test::Test::Equal(value) => Ok(UnaryTest::Equal(map_workflow_literal(value)?)),
        unary_test::Test::IntegerInterval(interval) => {
            Ok(UnaryTest::IntegerInterval(map_integer_interval(interval)))
        }
    }
}

fn map_workflow_literal(
    value: bpmp_contracts::wir::v1::WorkflowLiteral,
) -> Result<WorkflowValue, WirLoadError> {
    match value.value.ok_or(WirLoadError::MissingWorkflowLiteral)? {
        workflow_literal::Value::BooleanValue(value) => Ok(WorkflowValue::Boolean(value)),
        workflow_literal::Value::IntegerValue(value) => Ok(WorkflowValue::Integer(value)),
        workflow_literal::Value::StringValue(value) => Ok(WorkflowValue::String(value)),
    }
}

fn map_coverage(
    coverage: bpmp_contracts::wir::v1::GatewayCoverage,
) -> Result<GatewayCoverage, WirLoadError> {
    if coverage.variable.trim().is_empty() {
        return Err(WirLoadError::EmptyCoverageVariable);
    }
    let domain = match WireValueType::try_from(coverage.value_type) {
        Ok(WireValueType::Boolean) => {
            if !coverage.enum_values.is_empty() || !coverage.integer_intervals.is_empty() {
                return Err(WirLoadError::InvalidCoverage(
                    "boolean coverage must not include enum values or integer intervals",
                ));
            }
            GatewayCoverageDomain::Boolean
        }
        Ok(WireValueType::String) => {
            if coverage.enum_values.is_empty() || !coverage.integer_intervals.is_empty() {
                return Err(WirLoadError::InvalidCoverage(
                    "string enum coverage requires enum values and no integer intervals",
                ));
            }
            GatewayCoverageDomain::Enum {
                values: coverage.enum_values,
            }
        }
        Ok(WireValueType::Integer) => {
            if !coverage.enum_values.is_empty() || coverage.integer_intervals.is_empty() {
                return Err(WirLoadError::InvalidCoverage(
                    "integer coverage requires intervals and no enum values",
                ));
            }
            GatewayCoverageDomain::Integer {
                intervals: coverage
                    .integer_intervals
                    .into_iter()
                    .map(map_integer_interval)
                    .collect(),
            }
        }
        Ok(WireValueType::Unspecified) | Err(_) => {
            return Err(WirLoadError::InvalidCoverageValueType(coverage.value_type));
        }
    };
    Ok(GatewayCoverage {
        variable: coverage.variable,
        domain,
    })
}

fn map_transition(
    transition: bpmp_contracts::wir::v1::ConditionalTransition,
) -> Result<GuardedTransition, WirLoadError> {
    let target = node_id_value(transition.target_node_id, "gateway.target_node_id")?;
    let guard = if transition.is_default {
        if transition.guard.is_some() {
            return Err(WirLoadError::DefaultTransitionHasGuard(target));
        }
        None
    } else {
        Some(map_guard(transition.guard.ok_or_else(|| {
            WirLoadError::MissingTransitionGuard(target.clone())
        })?)?)
    };
    Ok(GuardedTransition { target, guard })
}

fn map_guard(
    guard: bpmp_contracts::wir::v1::GuardExpression,
) -> Result<GuardExpression, WirLoadError> {
    let operator = match WireComparisonOperator::try_from(guard.operator) {
        Ok(WireComparisonOperator::Equal) => ComparisonOperator::Equal,
        Ok(WireComparisonOperator::NotEqual) => ComparisonOperator::NotEqual,
        Ok(WireComparisonOperator::LessThan) => ComparisonOperator::LessThan,
        Ok(WireComparisonOperator::LessThanOrEqual) => ComparisonOperator::LessThanOrEqual,
        Ok(WireComparisonOperator::GreaterThan) => ComparisonOperator::GreaterThan,
        Ok(WireComparisonOperator::GreaterThanOrEqual) => ComparisonOperator::GreaterThanOrEqual,
        Ok(WireComparisonOperator::Unspecified) | Err(_) => {
            return Err(WirLoadError::InvalidComparisonOperator(guard.operator));
        }
    };
    let literal = match guard.literal.ok_or(WirLoadError::MissingGuardLiteral)? {
        guard_expression::Literal::BooleanValue(value) => WorkflowValue::Boolean(value),
        guard_expression::Literal::IntegerValue(value) => WorkflowValue::Integer(value),
        guard_expression::Literal::StringValue(value) => WorkflowValue::String(value),
    };
    if guard.variable.trim().is_empty() {
        return Err(WirLoadError::EmptyGuardVariable);
    }
    Ok(GuardExpression {
        variable: guard.variable,
        operator,
        literal,
    })
}

fn node_id_value(value: String, field: &'static str) -> Result<NodeId, WirLoadError> {
    NodeId::new(value).map_err(|source| WirLoadError::Identifier { field, source })
}

fn map_value_type(value_type: i32) -> Result<WorkflowValueType, WirLoadError> {
    match WireValueType::try_from(value_type) {
        Ok(WireValueType::Boolean) => Ok(WorkflowValueType::Boolean),
        Ok(WireValueType::Integer) => Ok(WorkflowValueType::Integer),
        Ok(WireValueType::String) => Ok(WorkflowValueType::String),
        Ok(WireValueType::Unspecified) | Err(_) => Err(WirLoadError::InvalidValueType(value_type)),
    }
}

fn map_integer_interval(interval: bpmp_contracts::wir::v1::IntegerInterval) -> IntegerInterval {
    IntegerInterval {
        lower: (!interval.lower_unbounded).then_some(interval.lower_bound),
        upper: (!interval.upper_unbounded).then_some(interval.upper_bound),
    }
}

fn non_empty(value: String, field: &'static str) -> Result<String, WirLoadError> {
    if value.trim().is_empty() {
        Err(WirLoadError::EmptyField(field))
    } else {
        Ok(value)
    }
}

#[derive(Debug, Error)]
pub enum WirLoadError {
    #[error(transparent)]
    Artifact(#[from] ArtifactError),
    #[error("invalid identifier in WIR field {field}: {source}")]
    Identifier {
        field: &'static str,
        source: IdentifierError,
    },
    #[error("WIR node {0} has no kind")]
    MissingNodeKind(NodeId),
    #[error("default gateway transition to {0} must not contain a guard")]
    DefaultTransitionHasGuard(NodeId),
    #[error("non-default gateway transition to {0} is missing a guard")]
    MissingTransitionGuard(NodeId),
    #[error("gateway guard has unsupported comparison operator tag {0}")]
    InvalidComparisonOperator(i32),
    #[error("gateway guard has no literal")]
    MissingGuardLiteral,
    #[error("gateway guard variable is empty")]
    EmptyGuardVariable,
    #[error("gateway coverage variable is empty")]
    EmptyCoverageVariable,
    #[error("gateway coverage has unsupported value type tag {0}")]
    InvalidCoverageValueType(i32),
    #[error("gateway coverage is invalid: {0}")]
    InvalidCoverage(&'static str),
    #[error("decision table hit policy tag {0} is invalid")]
    InvalidHitPolicy(i32),
    #[error("decision table hit policy {0} is not supported by this engine phase")]
    UnsupportedHitPolicy(&'static str),
    #[error("WIR value type tag {0} is invalid")]
    InvalidValueType(i32),
    #[error("WIR field {0} must not be empty")]
    EmptyField(&'static str),
    #[error("decision unary test has no typed test")]
    MissingUnaryTest,
    #[error("decision literal has no typed value")]
    MissingWorkflowLiteral,
    #[error(transparent)]
    Domain(DomainError),
}
