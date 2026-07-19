use bpmp_contracts::wir::v1::{
    BooleanExpression, ComparisonOperator, ExtensionProperty, GatewayCoverage, GatewayDirection,
    GuardExpression, MultiInstanceMode, MultiInstanceSpec, PropertyValue, TimerKind, ValueType,
    WorkflowIntermediateRepresentation, boolean_expression, event_trigger, guard_expression, node,
    property_value,
};
use quick_xml::Writer;
use quick_xml::events::{BytesDecl, BytesEnd, BytesStart, Event};
use thiserror::Error;

const BPMN_MODEL_NAMESPACE: &str = "http://www.omg.org/spec/BPMN/20100524/MODEL";
const BPMP_EXTENSION_NAMESPACE: &str = "urn:bpmp:wir:extension:v1";

#[allow(clippy::too_many_lines)]
pub(crate) fn print_canonical(
    wir: &WorkflowIntermediateRepresentation,
) -> Result<String, PrintError> {
    let mut writer = Writer::new_with_indent(Vec::new(), b' ', 2);
    writer.write_event(Event::Decl(BytesDecl::new("1.0", Some("UTF-8"), None)))?;

    let mut definitions = BytesStart::new("bpmn:definitions");
    definitions.push_attribute(("xmlns:bpmn", BPMN_MODEL_NAMESPACE));
    definitions.push_attribute(("xmlns:bpmp", BPMP_EXTENSION_NAMESPACE));
    writer.write_event(Event::Start(definitions))?;

    let mut process = BytesStart::new("bpmn:process");
    process.push_attribute(("id", wir.workflow_type.as_str()));
    writer.write_event(Event::Start(process))?;
    write_properties(&mut writer, &wir.properties)?;

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
            node::Kind::CallActivity(call) => {
                write_call_activity(&mut writer, node, call)?;
                flows.push((node.id.as_str(), call.next_node_id.as_str(), None, false));
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
                    let condition = render_transition_condition(transition)?;
                    flows.push((
                        node.id.as_str(),
                        transition.target_node_id.as_str(),
                        condition,
                        transition.is_default,
                    ));
                }
            }
            node::Kind::ParallelGateway(gateway) => {
                write_node(&mut writer, "bpmn:parallelGateway", node, None, None, None)?;
                match GatewayDirection::try_from(gateway.direction) {
                    Ok(GatewayDirection::Split | GatewayDirection::Join) => {
                        for target in &gateway.target_node_ids {
                            flows.push((node.id.as_str(), target.as_str(), None, false));
                        }
                    }
                    Ok(GatewayDirection::Unspecified) | Err(_) => {
                        return Err(PrintError::InvalidGatewayDirection(gateway.direction));
                    }
                }
            }
            node::Kind::InclusiveGateway(gateway) => {
                write_node(
                    &mut writer,
                    "bpmn:inclusiveGateway",
                    node,
                    None,
                    None,
                    enum_values(gateway.coverage.as_ref()),
                )?;
                match GatewayDirection::try_from(gateway.direction) {
                    Ok(GatewayDirection::Split) => {
                        for transition in &gateway.transitions {
                            let condition = render_transition_condition(transition)?;
                            flows.push((
                                node.id.as_str(),
                                transition.target_node_id.as_str(),
                                condition,
                                transition.is_default,
                            ));
                        }
                    }
                    Ok(GatewayDirection::Join) => {
                        flows.push((node.id.as_str(), gateway.next_node_id.as_str(), None, false));
                    }
                    Ok(GatewayDirection::Unspecified) | Err(_) => {
                        return Err(PrintError::InvalidGatewayDirection(gateway.direction));
                    }
                }
            }
            node::Kind::End(_) => {
                write_node(&mut writer, "bpmn:endEvent", node, None, None, None)?;
            }
        }
        for boundary in &node.boundary_events {
            write_boundary_event(&mut writer, &node.id, boundary)?;
            flows.push((
                boundary.id.as_str(),
                boundary.target_node_id.as_str(),
                None,
                false,
            ));
        }
    }

    for (ordinal, (source, target, condition, is_default)) in flows.into_iter().enumerate() {
        let mut flow = BytesStart::new("bpmn:sequenceFlow");
        let flow_id = format!("canonical-flow-{ordinal}");
        flow.push_attribute(("id", flow_id.as_str()));
        flow.push_attribute(("sourceRef", source));
        flow.push_attribute(("targetRef", target));
        if is_default {
            flow.push_attribute(("isDefault", "true"));
        }
        if let Some(condition) = &condition {
            writer.write_event(Event::Start(flow))?;
            writer.write_event(Event::Start(BytesStart::new("bpmn:conditionExpression")))?;
            writer.write_event(Event::Text(quick_xml::events::BytesText::new(condition)))?;
            writer.write_event(Event::End(BytesEnd::new("bpmn:conditionExpression")))?;
            writer.write_event(Event::End(BytesEnd::new("bpmn:sequenceFlow")))?;
        } else {
            writer.write_event(Event::Empty(flow))?;
        }
    }

    writer.write_event(Event::End(BytesEnd::new("bpmn:process")))?;
    writer.write_event(Event::End(BytesEnd::new("bpmn:definitions")))?;
    String::from_utf8(writer.into_inner()).map_err(PrintError::Utf8)
}

fn write_call_activity(
    writer: &mut Writer<Vec<u8>>,
    node: &bpmp_contracts::wir::v1::Node,
    call: &bpmp_contracts::wir::v1::CallActivityNode,
) -> Result<(), std::io::Error> {
    let mut element = BytesStart::new("bpmn:callActivity");
    element.push_attribute(("id", node.id.as_str()));
    element.push_attribute(("calledElement", call.called_element.as_str()));
    if !call.called_version.is_empty() {
        element.push_attribute(("calledVersion", call.called_version.as_str()));
    }
    write_activity_element(writer, element, node)
}

fn write_boundary_event(
    writer: &mut Writer<Vec<u8>>,
    attached_to: &str,
    boundary: &bpmp_contracts::wir::v1::BoundaryEvent,
) -> Result<(), PrintError> {
    let mut element = BytesStart::new("bpmn:boundaryEvent");
    element.push_attribute(("id", boundary.id.as_str()));
    element.push_attribute(("attachedToRef", attached_to));
    if !boundary.cancel_activity {
        element.push_attribute(("cancelActivity", "false"));
    }
    writer.write_event(Event::Start(element))?;
    let trigger = boundary
        .trigger
        .as_ref()
        .and_then(|trigger| trigger.trigger.as_ref())
        .ok_or_else(|| PrintError::InvalidBoundaryEvent(boundary.id.clone()))?;
    match trigger {
        event_trigger::Trigger::Timer(timer) => {
            writer.write_event(Event::Start(BytesStart::new("bpmn:timerEventDefinition")))?;
            let element_name = match TimerKind::try_from(timer.kind) {
                Ok(TimerKind::Date) => "bpmn:timeDate",
                Ok(TimerKind::Duration) => "bpmn:timeDuration",
                Ok(TimerKind::Cycle) => "bpmn:timeCycle",
                Ok(TimerKind::Unspecified) | Err(_) => {
                    return Err(PrintError::InvalidBoundaryEvent(boundary.id.clone()));
                }
            };
            writer.write_event(Event::Start(BytesStart::new(element_name)))?;
            writer.write_event(Event::Text(quick_xml::events::BytesText::new(
                &timer.expression,
            )))?;
            writer.write_event(Event::End(BytesEnd::new(element_name)))?;
            writer.write_event(Event::End(BytesEnd::new("bpmn:timerEventDefinition")))?;
        }
        event_trigger::Trigger::Error(error) => {
            let mut definition = BytesStart::new("bpmn:errorEventDefinition");
            if !error.error_ref.is_empty() {
                definition.push_attribute(("errorRef", error.error_ref.as_str()));
            }
            writer.write_event(Event::Empty(definition))?;
        }
        event_trigger::Trigger::Message(message) => {
            let mut definition = BytesStart::new("bpmn:messageEventDefinition");
            definition.push_attribute(("messageRef", message.message_ref.as_str()));
            writer.write_event(Event::Empty(definition))?;
        }
    }
    writer.write_event(Event::End(BytesEnd::new("bpmn:boundaryEvent")))?;
    Ok(())
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

fn render_transition_condition(
    transition: &bpmp_contracts::wir::v1::ConditionalTransition,
) -> Result<Option<String>, PrintError> {
    match (&transition.guard, &transition.expression) {
        (Some(guard), None) => render_guard(guard).map(Some),
        (None, Some(expression)) => render_boolean_expression(expression).map(Some),
        (None, None) if transition.is_default => Ok(None),
        (None, None) => Ok(None),
        (Some(_), Some(_)) => Err(PrintError::ConflictingGuardRepresentations),
    }
}

fn render_boolean_expression(expression: &BooleanExpression) -> Result<String, PrintError> {
    match expression
        .expression
        .as_ref()
        .ok_or(PrintError::MissingBooleanExpression)?
    {
        boolean_expression::Expression::Comparison(guard) => render_guard(guard),
        boolean_expression::Expression::Conjunction(junction) => render_junction("&&", junction),
        boolean_expression::Expression::Disjunction(junction) => render_junction("||", junction),
        boolean_expression::Expression::Negation(operand) => {
            Ok(format!("!({})", render_boolean_expression(operand)?))
        }
        boolean_expression::Expression::Constant(value) => Ok(value.to_string()),
    }
}

fn render_junction(
    operator: &str,
    junction: &bpmp_contracts::wir::v1::BooleanJunction,
) -> Result<String, PrintError> {
    if junction.operands.len() < 2 {
        return Err(PrintError::InvalidBooleanJunction);
    }
    let operands = junction
        .operands
        .iter()
        .map(render_boolean_expression)
        .collect::<Result<Vec<_>, _>>()?;
    Ok(format!("({})", operands.join(&format!(" {operator} "))))
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
    write_activity_element(writer, element, node)
}

fn write_activity_element(
    writer: &mut Writer<Vec<u8>>,
    element: BytesStart<'_>,
    node: &bpmp_contracts::wir::v1::Node,
) -> Result<(), std::io::Error> {
    if node.properties.is_empty() && node.multi_instance.is_none() {
        return writer.write_event(Event::Empty(element));
    }
    let name = String::from_utf8_lossy(element.name().as_ref()).into_owned();
    writer.write_event(Event::Start(element))?;
    write_properties(writer, &node.properties)?;
    if let Some(spec) = &node.multi_instance {
        write_multi_instance(writer, spec)?;
    }
    writer.write_event(Event::End(BytesEnd::new(name)))
}

fn write_properties(
    writer: &mut Writer<Vec<u8>>,
    properties: &[ExtensionProperty],
) -> Result<(), std::io::Error> {
    if properties.is_empty() {
        return Ok(());
    }
    writer.write_event(Event::Start(BytesStart::new("bpmn:extensionElements")))?;
    for property in properties {
        let mut element = BytesStart::new("bpmp:property");
        element.push_attribute(("namespaceUri", property.namespace_uri.as_str()));
        element.push_attribute(("elementName", property.element_name.as_str()));
        element.push_attribute(("name", property.name.as_str()));
        let (value_type, value) = render_property_value(property.value.as_ref());
        element.push_attribute(("type", value_type));
        element.push_attribute(("value", value.as_str()));
        writer.write_event(Event::Empty(element))?;
    }
    writer.write_event(Event::End(BytesEnd::new("bpmn:extensionElements")))
}

fn render_property_value(value: Option<&PropertyValue>) -> (&'static str, String) {
    match value.and_then(|value| value.value.as_ref()) {
        Some(property_value::Value::StringValue(value)) => ("string", value.clone()),
        Some(property_value::Value::IntegerValue(value)) => ("integer", value.to_string()),
        Some(property_value::Value::BooleanValue(value)) => ("boolean", value.to_string()),
        Some(property_value::Value::DurationMilliseconds(value)) => {
            ("durationMilliseconds", value.to_string())
        }
        None => ("string", String::new()),
    }
}

fn write_multi_instance(
    writer: &mut Writer<Vec<u8>>,
    spec: &MultiInstanceSpec,
) -> Result<(), std::io::Error> {
    let mut element = BytesStart::new("bpmn:multiInstanceLoopCharacteristics");
    match MultiInstanceMode::try_from(spec.mode) {
        Ok(MultiInstanceMode::Sequential) => element.push_attribute(("isSequential", "true")),
        Ok(MultiInstanceMode::Parallel) => element.push_attribute(("isSequential", "false")),
        Ok(MultiInstanceMode::Unspecified) | Err(_) => {}
    }
    if !spec.collection_expression.is_empty() {
        element.push_attribute(("collection", spec.collection_expression.as_str()));
    }
    if !spec.item_variable.is_empty() {
        element.push_attribute(("elementVariable", spec.item_variable.as_str()));
    }
    let max_parallelism = spec.max_parallelism.to_string();
    if spec.max_parallelism > 0 {
        element.push_attribute(("maxParallelism", max_parallelism.as_str()));
    }
    if spec.cardinality_expression.is_empty() {
        return writer.write_event(Event::Empty(element));
    }
    writer.write_event(Event::Start(element))?;
    writer.write_event(Event::Start(BytesStart::new("bpmn:loopCardinality")))?;
    writer.write_event(Event::Text(quick_xml::events::BytesText::new(
        &spec.cardinality_expression,
    )))?;
    writer.write_event(Event::End(BytesEnd::new("bpmn:loopCardinality")))?;
    writer.write_event(Event::End(BytesEnd::new(
        "bpmn:multiInstanceLoopCharacteristics",
    )))
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
    #[error("gateway has unsupported direction tag {0}")]
    InvalidGatewayDirection(i32),
    #[error("gateway guard has no literal")]
    MissingGuardLiteral,
    #[error("gateway transition contains both legacy and complex guard representations")]
    ConflictingGuardRepresentations,
    #[error("gateway boolean expression has no expression")]
    MissingBooleanExpression,
    #[error("gateway boolean junction must contain at least two operands")]
    InvalidBooleanJunction,
    #[error("boundary event {0} has an invalid trigger")]
    InvalidBoundaryEvent(String),
    #[error("failed to write canonical BPMN: {0}")]
    Write(#[from] std::io::Error),
    #[error("canonical BPMN is not valid UTF-8: {0}")]
    Utf8(#[from] std::string::FromUtf8Error),
}
