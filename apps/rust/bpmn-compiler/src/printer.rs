use bpmp_contracts::wir::v1::{WorkflowIntermediateRepresentation, node};
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
                write_node(&mut writer, "bpmn:startEvent", &node.id, None)?;
                flows.push((node.id.as_str(), start.next_node_id.as_str()));
            }
            node::Kind::ServiceTask(task) => {
                write_node(
                    &mut writer,
                    "bpmn:serviceTask",
                    &node.id,
                    Some(&task.task_type),
                )?;
                flows.push((node.id.as_str(), task.next_node_id.as_str()));
            }
            node::Kind::End(_) => write_node(&mut writer, "bpmn:endEvent", &node.id, None)?,
        }
    }

    for (ordinal, (source, target)) in flows.into_iter().enumerate() {
        let mut flow = BytesStart::new("bpmn:sequenceFlow");
        let flow_id = format!("canonical-flow-{ordinal}");
        flow.push_attribute(("id", flow_id.as_str()));
        flow.push_attribute(("sourceRef", source));
        flow.push_attribute(("targetRef", target));
        writer.write_event(Event::Empty(flow))?;
    }

    writer.write_event(Event::End(BytesEnd::new("bpmn:process")))?;
    writer.write_event(Event::End(BytesEnd::new("bpmn:definitions")))?;
    String::from_utf8(writer.into_inner()).map_err(PrintError::Utf8)
}

fn write_node(
    writer: &mut Writer<Vec<u8>>,
    element_name: &str,
    id: &str,
    task_type: Option<&str>,
) -> Result<(), std::io::Error> {
    let mut element = BytesStart::new(element_name);
    element.push_attribute(("id", id));
    if let Some(task_type) = task_type {
        element.push_attribute(("name", task_type));
    }
    writer.write_event(Event::Empty(element))
}

#[derive(Debug, Error)]
pub enum PrintError {
    #[error("WIR node {0} has no kind")]
    MissingNodeKind(String),
    #[error("failed to write canonical BPMN: {0}")]
    Write(#[from] std::io::Error),
    #[error("canonical BPMN is not valid UTF-8: {0}")]
    Utf8(#[from] std::string::FromUtf8Error),
}
