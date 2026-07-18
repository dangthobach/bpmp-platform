use std::collections::{BTreeMap, BTreeSet, VecDeque};

use bpmp_contracts::WIR_SCHEMA_VERSION;
use bpmp_contracts::wir::v1::{
    EndNode, Node, ServiceTaskNode, StartNode, WorkflowIntermediateRepresentation, node,
};
use quick_xml::XmlVersion;
use quick_xml::events::{BytesStart, Event};
use quick_xml::name::ResolveResult;
use quick_xml::reader::NsReader;
use thiserror::Error;

use crate::{CompileDiagnostic, DiagnosticKind, SourceSpan};

const BPMN_MODEL_NAMESPACE: &[u8] = b"http://www.omg.org/spec/BPMN/20100524/MODEL";

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct CompilerLimits {
    pub max_input_bytes: usize,
    pub max_xml_depth: u32,
}

impl CompilerLimits {
    /// Creates explicit resource limits for untrusted BPMN input.
    ///
    /// # Errors
    ///
    /// Returns an error when either configured limit is zero.
    pub fn new(max_input_bytes: usize, max_xml_depth: u32) -> Result<Self, CompilerConfigError> {
        if max_input_bytes == 0 {
            return Err(CompilerConfigError::NonPositive("max_input_bytes"));
        }
        if max_xml_depth == 0 {
            return Err(CompilerConfigError::NonPositive("max_xml_depth"));
        }
        Ok(Self {
            max_input_bytes,
            max_xml_depth,
        })
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum CompilerConfigError {
    #[error("compiler setting {0} must be greater than zero")]
    NonPositive(&'static str),
}

#[derive(Debug, Clone, Copy)]
pub struct SourceDocument<'a> {
    pub name: &'a str,
    pub bytes: &'a [u8],
}

pub struct BpmnCompiler {
    limits: CompilerLimits,
}

impl BpmnCompiler {
    pub const fn new(limits: CompilerLimits) -> Self {
        Self { limits }
    }

    /// Compiles one BPMN process into the canonical WIR v1 contract.
    ///
    /// The current phase accepts a linear process made of `startEvent`,
    /// `serviceTask`, `endEvent`, and `sequenceFlow`. Other semantic elements are
    /// rejected explicitly so unsupported behavior cannot enter runtime.
    ///
    /// # Errors
    ///
    /// Returns all diagnostics collected from parsing, symbol resolution, and
    /// graph validation. Malformed XML is fatal after its diagnostic is recorded.
    pub fn compile(
        &self,
        source: SourceDocument<'_>,
        workflow_version: &str,
    ) -> Result<WorkflowIntermediateRepresentation, Vec<CompileDiagnostic>> {
        let locations = SourceLocations::new(source.name, source.bytes);
        if workflow_version.trim().is_empty() {
            return Err(vec![locations.diagnostic(
                0,
                DiagnosticKind::MissingAttribute {
                    element: "compiler invocation".to_owned(),
                    attribute: "workflow_version",
                },
            )]);
        }
        if source.bytes.len() > self.limits.max_input_bytes {
            return Err(vec![locations.diagnostic(
                0,
                DiagnosticKind::InputTooLarge {
                    actual: source.bytes.len(),
                    configured_limit: self.limits.max_input_bytes,
                },
            )]);
        }
        let mut parsed = parse(source, self.limits, &locations);
        validate_graph(&mut parsed, &locations);
        if !parsed.diagnostics.is_empty() {
            parsed.diagnostics.sort_by_key(|diagnostic| {
                (diagnostic.span.byte_offset, diagnostic.kind.to_string())
            });
            return Err(parsed.diagnostics);
        }
        Ok(lower(parsed, workflow_version))
    }

    /// Prints WIR as deterministic normalized BPMN for review and round-trip checks.
    ///
    /// # Errors
    ///
    /// Returns [`crate::PrintError`] when the WIR contains a missing node kind or
    /// output cannot be represented as UTF-8.
    pub fn print(
        &self,
        wir: &WorkflowIntermediateRepresentation,
    ) -> Result<String, crate::PrintError> {
        crate::printer::print_canonical(wir)
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
enum RawNodeKind {
    Start,
    ServiceTask,
    End,
}

#[derive(Debug, Clone)]
struct RawNode {
    kind: RawNodeKind,
    task_type: Option<String>,
    offset: usize,
}

#[derive(Debug, Clone)]
struct RawFlow {
    id: String,
    source: String,
    target: String,
    offset: usize,
}

#[derive(Default)]
struct ParsedProcess {
    process_id: Option<String>,
    process_count: usize,
    nodes: BTreeMap<String, RawNode>,
    flows: Vec<RawFlow>,
    diagnostics: Vec<CompileDiagnostic>,
}

fn parse(
    source: SourceDocument<'_>,
    limits: CompilerLimits,
    locations: &SourceLocations,
) -> ParsedProcess {
    let mut parsed = ParsedProcess::default();
    let mut reader = NsReader::from_reader(source.bytes);
    reader.config_mut().trim_text(true);
    let decoder = reader.decoder();
    let mut buffer = Vec::new();
    let mut depth = 0_u32;
    let mut previous_position = 0_usize;

    loop {
        let offset = next_tag_offset(source.bytes, previous_position);
        let event = reader.read_resolved_event_into(&mut buffer);
        match event {
            Ok((namespace, Event::Start(element))) => {
                depth = depth.saturating_add(1);
                if depth > limits.max_xml_depth {
                    parsed.diagnostics.push(locations.diagnostic(
                        offset,
                        DiagnosticKind::XmlDepthExceeded {
                            actual: depth,
                            configured_limit: limits.max_xml_depth,
                        },
                    ));
                }
                inspect_element(
                    &namespace,
                    &element,
                    decoder,
                    offset,
                    &mut parsed,
                    locations,
                );
            }
            Ok((namespace, Event::Empty(element))) => {
                inspect_element(
                    &namespace,
                    &element,
                    decoder,
                    offset,
                    &mut parsed,
                    locations,
                );
            }
            Ok((_, Event::End(_))) => depth = depth.saturating_sub(1),
            Ok((_, Event::DocType(_))) => parsed
                .diagnostics
                .push(locations.diagnostic(offset, DiagnosticKind::ForbiddenDocumentType)),
            Ok((_, Event::Eof)) => break,
            Ok(_) => {}
            Err(error) => {
                parsed.diagnostics.push(locations.diagnostic(
                    offset,
                    DiagnosticKind::Xml {
                        detail: error.to_string(),
                    },
                ));
                break;
            }
        }
        previous_position = usize::try_from(reader.buffer_position()).unwrap_or(source.bytes.len());
        buffer.clear();
    }
    parsed
}

fn inspect_element(
    namespace: &ResolveResult<'_>,
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) {
    let local_name = String::from_utf8_lossy(element.local_name().as_ref()).into_owned();
    let semantic = matches!(
        local_name.as_str(),
        "process"
            | "startEvent"
            | "serviceTask"
            | "endEvent"
            | "sequenceFlow"
            | "exclusiveGateway"
            | "inclusiveGateway"
            | "parallelGateway"
            | "userTask"
            | "scriptTask"
            | "callActivity"
            | "subProcess"
    );
    let is_bpmn = matches!(namespace, ResolveResult::Bound(namespace) if namespace.as_ref() == BPMN_MODEL_NAMESPACE);
    if semantic && !is_bpmn {
        parsed.diagnostics.push(locations.diagnostic(
            offset,
            DiagnosticKind::ElementOutsideBpmnNamespace {
                element: local_name,
            },
        ));
        return;
    }
    if !is_bpmn {
        return;
    }

    match local_name.as_str() {
        "process" => {
            parsed.process_count += 1;
            if parsed.process_id.is_none() {
                parsed.process_id = required_attribute(
                    element, decoder, "process", b"id", offset, parsed, locations,
                );
            }
        }
        "startEvent" => insert_node(
            element,
            decoder,
            RawNodeKind::Start,
            None,
            offset,
            parsed,
            locations,
        ),
        "serviceTask" => {
            let task_type = optional_attribute(element, decoder, b"name")
                .filter(|value| !value.trim().is_empty());
            insert_node(
                element,
                decoder,
                RawNodeKind::ServiceTask,
                task_type,
                offset,
                parsed,
                locations,
            );
        }
        "endEvent" => insert_node(
            element,
            decoder,
            RawNodeKind::End,
            None,
            offset,
            parsed,
            locations,
        ),
        "sequenceFlow" => insert_flow(element, decoder, offset, parsed, locations),
        "exclusiveGateway" | "inclusiveGateway" | "parallelGateway" | "userTask" | "scriptTask"
        | "callActivity" | "subProcess" => {
            parsed.diagnostics.push(locations.diagnostic(
                offset,
                DiagnosticKind::UnsupportedElement {
                    element: local_name,
                },
            ));
        }
        _ => {}
    }
}

fn insert_node(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    kind: RawNodeKind,
    task_type: Option<String>,
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) {
    let element_name = match kind {
        RawNodeKind::Start => "startEvent",
        RawNodeKind::ServiceTask => "serviceTask",
        RawNodeKind::End => "endEvent",
    };
    let Some(id) = required_attribute(
        element,
        decoder,
        element_name,
        b"id",
        offset,
        parsed,
        locations,
    ) else {
        return;
    };
    if parsed
        .nodes
        .insert(
            id.clone(),
            RawNode {
                kind,
                task_type,
                offset,
            },
        )
        .is_some()
    {
        parsed
            .diagnostics
            .push(locations.diagnostic(offset, DiagnosticKind::DuplicateId { id }));
    }
}

fn insert_flow(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) {
    let id = required_attribute(
        element,
        decoder,
        "sequenceFlow",
        b"id",
        offset,
        parsed,
        locations,
    );
    let source = required_attribute(
        element,
        decoder,
        "sequenceFlow",
        b"sourceRef",
        offset,
        parsed,
        locations,
    );
    let target = required_attribute(
        element,
        decoder,
        "sequenceFlow",
        b"targetRef",
        offset,
        parsed,
        locations,
    );
    if let (Some(id), Some(source), Some(target)) = (id, source, target) {
        parsed.flows.push(RawFlow {
            id,
            source,
            target,
            offset,
        });
    }
}

fn required_attribute(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    element_name: &str,
    attribute_name: &'static [u8],
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) -> Option<String> {
    optional_attribute(element, decoder, attribute_name).or_else(|| {
        parsed.diagnostics.push(locations.diagnostic(
            offset,
            DiagnosticKind::MissingAttribute {
                element: element_name.to_owned(),
                attribute: std::str::from_utf8(attribute_name).unwrap_or("unknown"),
            },
        ));
        None
    })
}

fn optional_attribute(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    attribute_name: &[u8],
) -> Option<String> {
    element.attributes().flatten().find_map(|attribute| {
        (attribute.key.local_name().as_ref() == attribute_name).then(|| {
            attribute
                .decoded_and_normalized_value(XmlVersion::Implicit1_0, decoder)
                .map_or_else(|_| String::new(), std::borrow::Cow::into_owned)
        })
    })
}

// The passes share one diagnostic set and borrowed graph indexes.
#[allow(clippy::too_many_lines)]
fn validate_graph(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    if parsed.process_count == 0 {
        parsed
            .diagnostics
            .push(locations.diagnostic(0, DiagnosticKind::MissingProcess));
    } else if parsed.process_count > 1 {
        parsed
            .diagnostics
            .push(locations.diagnostic(0, DiagnosticKind::MultipleProcesses));
    }

    let starts: Vec<_> = parsed
        .nodes
        .iter()
        .filter(|(_, node)| node.kind == RawNodeKind::Start)
        .map(|(id, node)| (id.clone(), node.offset))
        .collect();
    if starts.is_empty() {
        parsed
            .diagnostics
            .push(locations.diagnostic(0, DiagnosticKind::MissingStartEvent));
    } else if starts.len() > 1 {
        parsed
            .diagnostics
            .push(locations.diagnostic(starts[1].1, DiagnosticKind::MultipleStartEvents));
    }
    if !parsed
        .nodes
        .values()
        .any(|node| node.kind == RawNodeKind::End)
    {
        parsed
            .diagnostics
            .push(locations.diagnostic(0, DiagnosticKind::MissingEndEvent));
    }

    let mut outgoing: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    let mut incoming: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    for flow in &parsed.flows {
        if !parsed.nodes.contains_key(&flow.source) {
            parsed.diagnostics.push(locations.diagnostic(
                flow.offset,
                DiagnosticKind::UnresolvedReference {
                    flow_id: flow.id.clone(),
                    missing_id: flow.source.clone(),
                },
            ));
        }
        if !parsed.nodes.contains_key(&flow.target) {
            parsed.diagnostics.push(locations.diagnostic(
                flow.offset,
                DiagnosticKind::UnresolvedReference {
                    flow_id: flow.id.clone(),
                    missing_id: flow.target.clone(),
                },
            ));
        }
        outgoing.entry(&flow.source).or_default().push(&flow.target);
        incoming.entry(&flow.target).or_default().push(&flow.source);
    }

    for (id, node) in &parsed.nodes {
        let outgoing_count = outgoing.get(id.as_str()).map_or(0, Vec::len);
        let incoming_count = incoming.get(id.as_str()).map_or(0, Vec::len);
        if node.kind != RawNodeKind::End && outgoing_count != 1 {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::InvalidOutgoingCount {
                    node_id: id.clone(),
                    actual: outgoing_count,
                },
            ));
        }
        if node.kind == RawNodeKind::End && outgoing_count != 0 {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::InvalidOutgoingCount {
                    node_id: id.clone(),
                    actual: outgoing_count,
                },
            ));
        }
        let expected_incoming = usize::from(node.kind != RawNodeKind::Start);
        if incoming_count != expected_incoming {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::InvalidIncomingCount {
                    node_id: id.clone(),
                    actual: incoming_count,
                },
            ));
        }
    }

    if let Some((start, _)) = starts.first() {
        let reachable = traverse(start, &outgoing);
        let ends: Vec<_> = parsed
            .nodes
            .iter()
            .filter(|(_, node)| node.kind == RawNodeKind::End)
            .map(|(id, _)| id.as_str())
            .collect();
        let can_reach_end = traverse_many(&ends, &incoming);
        for (id, node) in &parsed.nodes {
            if !reachable.contains(id.as_str()) {
                parsed.diagnostics.push(locations.diagnostic(
                    node.offset,
                    DiagnosticKind::UnreachablePath {
                        element_id: id.clone(),
                    },
                ));
            }
            if !can_reach_end.contains(id.as_str()) {
                parsed.diagnostics.push(locations.diagnostic(
                    node.offset,
                    DiagnosticKind::DeadPath {
                        element_id: id.clone(),
                    },
                ));
            }
        }
    }
}

fn traverse<'a>(start: &'a str, edges: &BTreeMap<&'a str, Vec<&'a str>>) -> BTreeSet<&'a str> {
    traverse_many(&[start], edges)
}

fn traverse_many<'a>(
    starts: &[&'a str],
    edges: &BTreeMap<&'a str, Vec<&'a str>>,
) -> BTreeSet<&'a str> {
    let mut visited = BTreeSet::new();
    let mut pending = VecDeque::from(starts.to_vec());
    while let Some(node) = pending.pop_front() {
        if !visited.insert(node) {
            continue;
        }
        if let Some(next) = edges.get(node) {
            pending.extend(next.iter().copied());
        }
    }
    visited
}

fn lower(parsed: ParsedProcess, workflow_version: &str) -> WorkflowIntermediateRepresentation {
    let next_by_source: BTreeMap<_, _> = parsed
        .flows
        .iter()
        .map(|flow| (flow.source.as_str(), flow.target.as_str()))
        .collect();
    let start_node_id = parsed
        .nodes
        .iter()
        .find(|(_, node)| node.kind == RawNodeKind::Start)
        .map_or_else(String::new, |(id, _)| id.clone());
    let nodes = parsed
        .nodes
        .into_iter()
        .map(|(id, raw)| {
            let kind = match raw.kind {
                RawNodeKind::Start => node::Kind::Start(StartNode {
                    next_node_id: next_by_source[&id.as_str()].to_owned(),
                }),
                RawNodeKind::ServiceTask => node::Kind::ServiceTask(ServiceTaskNode {
                    task_type: raw.task_type.unwrap_or_else(|| id.clone()),
                    next_node_id: next_by_source[&id.as_str()].to_owned(),
                }),
                RawNodeKind::End => node::Kind::End(EndNode {}),
            };
            Node {
                id,
                kind: Some(kind),
            }
        })
        .collect();
    WorkflowIntermediateRepresentation {
        schema_version: WIR_SCHEMA_VERSION,
        workflow_type: parsed.process_id.unwrap_or_default(),
        workflow_version: workflow_version.to_owned(),
        start_node_id,
        nodes,
        content_hash: Vec::new(),
        signature: Vec::new(),
    }
}

fn next_tag_offset(input: &[u8], previous: usize) -> usize {
    let start = previous.min(input.len());
    input[start..]
        .iter()
        .position(|byte| *byte == b'<')
        .map_or(start, |relative| start + relative)
}

struct SourceLocations {
    file: String,
    line_starts: Vec<usize>,
}

impl SourceLocations {
    fn new(file: &str, bytes: &[u8]) -> Self {
        let mut line_starts = vec![0];
        line_starts.extend(
            bytes
                .iter()
                .enumerate()
                .filter_map(|(index, byte)| (*byte == b'\n').then_some(index + 1)),
        );
        Self {
            file: file.to_owned(),
            line_starts,
        }
    }

    fn diagnostic(&self, offset: usize, kind: DiagnosticKind) -> CompileDiagnostic {
        let line_index = self.line_starts.partition_point(|start| *start <= offset) - 1;
        let line = u32::try_from(line_index + 1).unwrap_or(u32::MAX);
        let column = u32::try_from(offset - self.line_starts[line_index] + 1).unwrap_or(u32::MAX);
        CompileDiagnostic {
            kind,
            span: SourceSpan {
                file: self.file.clone(),
                byte_offset: offset,
                line,
                column,
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::*;

    const VALID: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="order">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="charge" name="payment" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="charge" />
    <bpmn:sequenceFlow id="f2" sourceRef="charge" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;

    fn compiler() -> BpmnCompiler {
        BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap())
    }

    #[test]
    fn compiles_linear_bpmn_into_canonical_wir() {
        let wir = compiler()
            .compile(
                SourceDocument {
                    name: "order.bpmn",
                    bytes: VALID.as_bytes(),
                },
                "1",
            )
            .unwrap();
        assert_eq!(wir.workflow_type, "order");
        assert_eq!(wir.start_node_id, "start");
        assert_eq!(wir.nodes.len(), 3);
        assert!(wir.nodes.windows(2).all(|nodes| nodes[0].id < nodes[1].id));
    }

    #[test]
    fn canonical_print_compiles_to_equivalent_wir() {
        let compiler = compiler();
        let first = compiler
            .compile(
                SourceDocument {
                    name: "order.bpmn",
                    bytes: VALID.as_bytes(),
                },
                "1",
            )
            .unwrap();
        let printed = compiler.print(&first).unwrap();
        let second = compiler
            .compile(
                SourceDocument {
                    name: "canonical.bpmn",
                    bytes: printed.as_bytes(),
                },
                "1",
            )
            .unwrap();
        assert_eq!(first, second);
    }

    #[test]
    fn reports_unresolved_and_unreachable_nodes_with_locations() {
        let invalid = VALID
            .replace("targetRef=\"charge\"", "targetRef=\"missing\"")
            .replace("sourceRef=\"charge\"", "sourceRef=\"orphan\"");
        let diagnostics = compiler()
            .compile(
                SourceDocument {
                    name: "invalid.bpmn",
                    bytes: invalid.as_bytes(),
                },
                "1",
            )
            .unwrap_err();
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            diagnostic.kind,
            DiagnosticKind::UnresolvedReference { .. }
        )));
        assert!(
            diagnostics
                .iter()
                .all(|diagnostic| diagnostic.span.line > 0)
        );
    }

    #[test]
    fn rejects_unsupported_gateway_instead_of_lowering_incorrect_behavior() {
        let unsupported = VALID.replace(
            "<bpmn:serviceTask id=\"charge\" name=\"payment\" />",
            "<bpmn:exclusiveGateway id=\"charge\" />",
        );
        let diagnostics = compiler()
            .compile(
                SourceDocument {
                    name: "gateway.bpmn",
                    bytes: unsupported.as_bytes(),
                },
                "1",
            )
            .unwrap_err();
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            diagnostic.kind,
            DiagnosticKind::UnsupportedElement { .. }
        )));
    }

    #[test]
    fn rejects_document_type_declarations() {
        let xml =
            format!("<!DOCTYPE definitions [<!ENTITY xxe SYSTEM \"file:///secret\">]>{VALID}");
        let diagnostics = compiler()
            .compile(
                SourceDocument {
                    name: "xxe.bpmn",
                    bytes: xml.as_bytes(),
                },
                "1",
            )
            .unwrap_err();
        assert!(
            diagnostics
                .iter()
                .any(|diagnostic| matches!(diagnostic.kind, DiagnosticKind::ForbiddenDocumentType))
        );
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 1: BPMN to WIR canonical round-trip
        #[test]
        fn compile_print_compile_is_equivalent(
            process_suffix in "[a-z][a-z0-9]{0,10}",
            task_suffix in "[a-z][a-z0-9]{0,10}",
        ) {
            let source = VALID
                .replace("id=\"order\"", &format!("id=\"process-{process_suffix}\""))
                .replace("name=\"payment\"", &format!("name=\"task-{task_suffix}\""));
            let compiler = compiler();
            let first = compiler.compile(
                SourceDocument { name: "generated.bpmn", bytes: source.as_bytes() },
                "property-v1",
            ).expect("generated BPMN must compile");
            let canonical = compiler.print(&first).expect("WIR must print");
            let second = compiler.compile(
                SourceDocument { name: "canonical.bpmn", bytes: canonical.as_bytes() },
                "property-v1",
            ).expect("canonical BPMN must compile");
            prop_assert_eq!(first, second);
        }
    }
}
