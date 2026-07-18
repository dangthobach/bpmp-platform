use bpmp_contracts::wir::v1::{
    ComparisonOperator, GatewayCoverage, GuardExpression, ValueType,
    WorkflowIntermediateRepresentation, guard_expression, node,
};
use quick_xml::Writer;
use quick_xml::events::{BytesDecl, BytesEnd, BytesStart, Event};
use thiserror::Error;

const BPMN_MODEL_NAMESPACE: &str = "http://www.omg.org/spec/BPMN/20100524/MODEL";

pub(crate) fn print_canonical(
    wir: &WorkflowIntermediateRepresentation,
) -> Result<String, PrintError> {
    let mut writer = Writer::new_with_indent(Vec::new(), b' ', 2);
    writer.write_event(Event::Decl(BytesDecl::new("1.0", Some("UTF-8"), None)))?;

    let mut definitions = BytesStart::new("bpmn:definitions");
    definitions.push_attribute(("xmlns:bpmn", BPMN_MODEL_NAMESPACE));
    writer.write_event(Event::Start(definitions))?;

    let mut process = BytesStart::new("bpmn:process");
    process.push_attribute(("id", wir.workflow_type.as_str()));
    writer.write_event(Event::Start(process))?;

    let mut flows = Vec::new();
    for node in &wir.nodes {
        let kind = node
            .kind
            .as_ref()
            .ok_or_else(|| PrintError::MissingNodeKind(node.id.clone()))?;
        match kind {
            node::Kind::Start(start) => {
                write_node(&mut writer, "bpmn:startEvent", node, None, None, None)?;
                flows.push((node.id.as_str(), start.next_node_id.as_str(), None, false));
            }
            node::Kind::ServiceTask(task) => {
                write_node(
                    &mut writer,
                    "bpmn:serviceTask",
                    node,
                    Some(&task.task_type),
                    None,
                    None,
                )?;
                flows.push((node.id.as_str(), task.next_node_id.as_str(), None, false));
            }
            node::Kind::DecisionTask(task) => {
                write_node(
                    &mut writer,
                    "bpmn:businessRuleTask",
                    node,
                    None,
                    Some(&task.decision_table_id),
                    None,
                )?;
                flows.push((node.id.as_str(), task.next_node_id.as_str(), None, false));
            }
            node::Kind::ExclusiveGateway(gateway) => {
                write_node(
                    &mut writer,
                    "bpmn:exclusiveGateway",
                    node,
                    None,
                    None,
                    enum_values(gateway.coverage.as_ref()),
                )?;
                for transition in &gateway.transitions {
                    let condition = transition.guard.as_ref().map(render_guard).transpose()?;
                    flows.push((
                        node.id.as_str(),
                        transition.target_node_id.as_str(),
                        condition,
                        transition.is_default,
                    ));
                }
            }
            node::Kind::End(_) => {
                write_node(&mut writer, "bpmn:endEvent", node, None, None, None)?;
            }
        }
    }

    for (ordinal, (source, target, condition, is_default)) in flows.into_iter().enumerate() {
        let mut flow = BytesStart::new("bpmn:sequenceFlow");
        let flow_id = format!("canonical-flow-{ordinal}");
        flow.push_attribute(("id", flow_id.as_str()));
        flow.push_attribute(("sourceRef", source));
        flow.push_attribute(("targetRef", target));
        if let Some(condition) = &condition {
            flow.push_attribute(("condition", condition.as_str()));
        }
        if is_default {
            flow.push_attribute(("isDefault", "true"));
        }
        writer.write_event(Event::Empty(flow))?;
    }

    writer.write_event(Event::End(BytesEnd::new("bpmn:process")))?;
    writer.write_event(Event::End(BytesEnd::new("bpmn:definitions")))?;
    String::from_utf8(writer.into_inner()).map_err(PrintError::Utf8)
}

fn render_guard(guard: &GuardExpression) -> Result<String, PrintError> {
    let operator = match ComparisonOperator::try_from(guard.operator) {
        Ok(ComparisonOperator::Equal) => "==",
        Ok(ComparisonOperator::NotEqual) => "!=",
        Ok(ComparisonOperator::LessThan) => "<",
        Ok(ComparisonOperator::LessThanOrEqual) => "<=",
        Ok(ComparisonOperator::GreaterThan) => ">",
        Ok(ComparisonOperator::GreaterThanOrEqual) => ">=",
        Ok(ComparisonOperator::Unspecified) | Err(_) => {
            return Err(PrintError::InvalidGuardOperator(guard.operator));
        }
    };
    let literal = match guard
        .literal
        .as_ref()
        .ok_or(PrintError::MissingGuardLiteral)?
    {
        guard_expression::Literal::BooleanValue(value) => value.to_string(),
        guard_expression::Literal::IntegerValue(value) => value.to_string(),
        guard_expression::Literal::StringValue(value) => format!("\"{value}\""),
    };
    Ok(format!("{} {operator} {literal}", guard.variable))
}

fn write_node(
    writer: &mut Writer<Vec<u8>>,
    element_name: &str,
    node: &bpmp_contracts::wir::v1::Node,
    task_type: Option<&str>,
    decision_ref: Option<&str>,
    enum_values: Option<&[String]>,
) -> Result<(), std::io::Error> {
    let mut element = BytesStart::new(element_name);
    element.push_attribute(("id", node.id.as_str()));
    if let Some(task_type) = task_type {
        element.push_attribute(("name", task_type));
    }
    if let Some(decision_ref) = decision_ref {
        element.push_attribute(("decisionRef", decision_ref));
    }
    if let Some(contract) = &node.data_contract {
        if !contract.input_type.is_empty() {
            element.push_attribute(("inputType", contract.input_type.as_str()));
        }
        if !contract.output_type.is_empty() {
            element.push_attribute(("outputType", contract.output_type.as_str()));
        }
    }
    let sla = node.sla_milliseconds.to_string();
    if node.sla_milliseconds > 0 {
        element.push_attribute(("slaMilliseconds", sla.as_str()));
    }
    if !node.compensation_handler_id.is_empty() {
        element.push_attribute(("compensationHandler", node.compensation_handler_id.as_str()));
        element.push_attribute(("requiresCompensation", "true"));
    }
    let joined_enum_values = enum_values.map(|values| values.join(","));
    if let Some(values) = &joined_enum_values {
        element.push_attribute(("enumValues", values.as_str()));
    }
    writer.write_event(Event::Empty(element))
}

fn enum_values(coverage: Option<&GatewayCoverage>) -> Option<&[String]> {
    let coverage = coverage?;
    matches!(
        ValueType::try_from(coverage.value_type),
        Ok(ValueType::String)
    )
    .then_some(coverage.enum_values.as_slice())
}

#[derive(Debug, Error)]
pub enum PrintError {
    #[error("WIR node {0} has no kind")]
    MissingNodeKind(String),
    #[error("gateway guard has unsupported comparison operator tag {0}")]
    InvalidGuardOperator(i32),
    #[error("gateway guard has no literal")]
    MissingGuardLiteral,
    #[error("failed to write canonical BPMN: {0}")]
    Write(#[from] std::io::Error),
    #[error("canonical BPMN is not valid UTF-8: {0}")]
    Utf8(#[from] std::string::FromUtf8Error),
}
