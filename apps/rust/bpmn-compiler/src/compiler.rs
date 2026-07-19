use std::collections::{BTreeMap, BTreeSet, VecDeque};

use bpmp_contracts::WIR_SCHEMA_VERSION;
use bpmp_contracts::wir::v1::{
    CaseMilestone, CaseModel, CaseSentry, CaseStage, ComparisonOperator, ConditionalTransition,
    DataContract, DecisionInput, DecisionOutput, DecisionRule, DecisionTable, DecisionTaskNode,
    EndNode, ExclusiveGatewayNode, GatewayCoverage, GatewayDirection, GuardExpression, HitPolicy,
    InclusiveGatewayNode, IntegerInterval, Node, ParallelGatewayNode, ServiceTaskNode, StartNode,
    UnaryTest, ValueType, WorkflowIntermediateRepresentation, WorkflowLiteral, guard_expression,
    node, unary_test, workflow_literal,
};
use quick_xml::XmlVersion;
use quick_xml::events::{BytesStart, Event};
use quick_xml::name::ResolveResult;
use quick_xml::reader::NsReader;
use thiserror::Error;

use crate::{CompileDiagnostic, DiagnosticKind, SourceSpan};

const BPMN_MODEL_NAMESPACE: &[u8] = b"http://www.omg.org/spec/BPMN/20100524/MODEL";
const DMN_MODEL_NAMESPACES: [&[u8]; 2] = [
    b"https://www.omg.org/spec/DMN/20191111/MODEL/",
    b"http://www.omg.org/spec/DMN/20191111/MODEL/",
];
const CMMN_MODEL_NAMESPACES: [&[u8]; 2] = [
    b"https://www.omg.org/spec/CMMN/20151109/MODEL",
    b"http://www.omg.org/spec/CMMN/20151109/MODEL",
];

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
        tenant_id: &str,
        workflow_version: &str,
    ) -> Result<WorkflowIntermediateRepresentation, Vec<CompileDiagnostic>> {
        self.compile_with_decisions(source, &[], tenant_id, workflow_version)
    }

    /// Compiles one BPMN process plus optional DMN decision tables into WIR.
    ///
    /// # Errors
    ///
    /// Returns collected diagnostics when BPMN/DMN parsing, semantic validation,
    /// graph validation, or typed decision-table validation fails.
    pub fn compile_with_decisions(
        &self,
        source: SourceDocument<'_>,
        decision_sources: &[SourceDocument<'_>],
        tenant_id: &str,
        workflow_version: &str,
    ) -> Result<WorkflowIntermediateRepresentation, Vec<CompileDiagnostic>> {
        self.compile_with_models(source, decision_sources, &[], tenant_id, workflow_version)
    }

    /// Compiles BPMN with optional DMN and CMMN documents into one canonical WIR.
    ///
    /// # Errors
    ///
    /// Returns collected source diagnostics for any invalid input document.
    #[allow(clippy::too_many_lines)]
    pub fn compile_with_models(
        &self,
        source: SourceDocument<'_>,
        decision_sources: &[SourceDocument<'_>],
        case_sources: &[SourceDocument<'_>],
        tenant_id: &str,
        workflow_version: &str,
    ) -> Result<WorkflowIntermediateRepresentation, Vec<CompileDiagnostic>> {
        let locations = SourceLocations::new(source.name, source.bytes);
        if tenant_id.trim().is_empty() {
            return Err(vec![locations.diagnostic(
                0,
                DiagnosticKind::MissingAttribute {
                    element: "compiler invocation".to_owned(),
                    attribute: "tenant_id",
                },
            )]);
        }
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
        let mut diagnostics = Vec::new();
        let mut decision_tables = Vec::new();
        for decision_source in decision_sources {
            let decision_locations =
                SourceLocations::new(decision_source.name, decision_source.bytes);
            if decision_source.bytes.len() > self.limits.max_input_bytes {
                diagnostics.push(decision_locations.diagnostic(
                    0,
                    DiagnosticKind::InputTooLarge {
                        actual: decision_source.bytes.len(),
                        configured_limit: self.limits.max_input_bytes,
                    },
                ));
                continue;
            }
            match parse_dmn(*decision_source, self.limits, &decision_locations) {
                Ok(mut tables) => decision_tables.append(&mut tables),
                Err(mut errors) => diagnostics.append(&mut errors),
            }
        }
        let mut case_models = Vec::new();
        for case_source in case_sources {
            let case_locations = SourceLocations::new(case_source.name, case_source.bytes);
            if case_source.bytes.len() > self.limits.max_input_bytes {
                diagnostics.push(case_locations.diagnostic(
                    0,
                    DiagnosticKind::InputTooLarge {
                        actual: case_source.bytes.len(),
                        configured_limit: self.limits.max_input_bytes,
                    },
                ));
                continue;
            }
            match parse_cmmn(*case_source, self.limits, &case_locations) {
                Ok(mut models) => case_models.append(&mut models),
                Err(mut errors) => diagnostics.append(&mut errors),
            }
        }
        let mut case_ids = BTreeSet::new();
        for model in &case_models {
            if !case_ids.insert(model.id.as_str()) {
                diagnostics.push(locations.diagnostic(
                    0,
                    DiagnosticKind::InvalidCaseModel {
                        case_id: model.id.clone(),
                        detail: "duplicate case id across CMMN sources".into(),
                    },
                ));
            }
        }
        validate_decision_references(&parsed, &decision_tables, &locations, &mut diagnostics);
        enrich_decision_contracts(&mut parsed, &decision_tables, &locations, &mut diagnostics);
        validate_data_flow_paths(&parsed, &locations, &mut diagnostics);
        if diagnostics.is_empty() {
            let mut wir = lower(parsed, tenant_id, workflow_version);
            wir.decision_tables = decision_tables;
            wir.decision_tables
                .sort_unstable_by(|left, right| left.id.cmp(&right.id));
            wir.case_models = case_models;
            wir.case_models
                .sort_unstable_by(|left, right| left.id.cmp(&right.id));
            Ok(wir)
        } else {
            diagnostics.sort_by_key(|diagnostic| {
                (diagnostic.span.byte_offset, diagnostic.kind.to_string())
            });
            Err(diagnostics)
        }
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

    /// Generates a standalone, deterministic Rust transition table from WIR.
    ///
    /// # Errors
    ///
    /// Returns [`crate::CodegenError`] when WIR contains an invalid node contract.
    pub fn generate_rust(
        &self,
        wir: &WorkflowIntermediateRepresentation,
    ) -> Result<String, crate::CodegenError> {
        crate::codegen::generate_rust(wir)
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
enum RawNodeKind {
    Start,
    ServiceTask,
    DecisionTask,
    ExclusiveGateway,
    InclusiveGateway,
    ParallelGateway,
    End,
}

#[derive(Debug, Clone)]
struct RawNode {
    kind: RawNodeKind,
    task_type: Option<String>,
    input_type: Option<String>,
    output_type: Option<String>,
    decision_ref: Option<String>,
    sla_milliseconds: Option<u64>,
    compensation_handler_id: Option<String>,
    requires_compensation: bool,
    is_compensation_handler: bool,
    default_flow_id: Option<String>,
    enum_values: Vec<String>,
    offset: usize,
}

#[derive(Debug, Clone)]
struct RawFlow {
    id: String,
    source: String,
    target: String,
    condition: Option<String>,
    is_default: bool,
    offset: usize,
}

#[derive(Debug, Clone)]
struct RawBoundaryEvent {
    id: String,
    attached_to: String,
    is_compensation: bool,
    offset: usize,
}

#[derive(Debug, Clone)]
struct RawAssociation {
    id: String,
    source: String,
    target: String,
    offset: usize,
}

#[derive(Default)]
struct ParsedProcess {
    process_id: Option<String>,
    process_count: usize,
    process_sla_milliseconds: Option<u64>,
    nodes: BTreeMap<String, RawNode>,
    flows: Vec<RawFlow>,
    boundary_events: Vec<RawBoundaryEvent>,
    associations: Vec<RawAssociation>,
    open_boundary_event: Option<String>,
    gateway_pairs: BTreeMap<String, String>,
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
                if element.local_name().as_ref() == b"boundaryEvent" {
                    parsed.open_boundary_event = None;
                }
            }
            Ok((_, Event::End(element))) => {
                if element.local_name().as_ref() == b"boundaryEvent" {
                    parsed.open_boundary_event = None;
                }
                depth = depth.saturating_sub(1);
            }
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

#[allow(clippy::too_many_lines)]
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
            | "businessRuleTask"
            | "endEvent"
            | "sequenceFlow"
            | "exclusiveGateway"
            | "inclusiveGateway"
            | "parallelGateway"
            | "boundaryEvent"
            | "compensateEventDefinition"
            | "association"
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
            parsed.process_sla_milliseconds = parse_optional_u64_attribute(
                element,
                decoder,
                b"slaMilliseconds",
                "process",
                offset,
                parsed,
                locations,
            );
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
        "businessRuleTask" => insert_node(
            element,
            decoder,
            RawNodeKind::DecisionTask,
            None,
            offset,
            parsed,
            locations,
        ),
        "exclusiveGateway" => insert_node(
            element,
            decoder,
            RawNodeKind::ExclusiveGateway,
            None,
            offset,
            parsed,
            locations,
        ),
        "inclusiveGateway" => insert_node(
            element,
            decoder,
            RawNodeKind::InclusiveGateway,
            None,
            offset,
            parsed,
            locations,
        ),
        "parallelGateway" => insert_node(
            element,
            decoder,
            RawNodeKind::ParallelGateway,
            None,
            offset,
            parsed,
            locations,
        ),
        "boundaryEvent" => insert_boundary_event(element, decoder, offset, parsed, locations),
        "compensateEventDefinition" => mark_compensation_boundary(parsed),
        "association" => insert_association(element, decoder, offset, parsed, locations),
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
        "userTask" | "scriptTask" | "callActivity" | "subProcess" => {
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
        RawNodeKind::DecisionTask => "businessRuleTask",
        RawNodeKind::ExclusiveGateway => "exclusiveGateway",
        RawNodeKind::InclusiveGateway => "inclusiveGateway",
        RawNodeKind::ParallelGateway => "parallelGateway",
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
    let input_type = optional_non_empty_attribute(element, decoder, b"inputType");
    let output_type = optional_non_empty_attribute(element, decoder, b"outputType");
    let decision_ref = optional_non_empty_attribute(element, decoder, b"decisionRef");
    let sla_milliseconds = parse_optional_u64_attribute(
        element,
        decoder,
        b"slaMilliseconds",
        element_name,
        offset,
        parsed,
        locations,
    );
    let compensation_handler_id =
        optional_non_empty_attribute(element, decoder, b"compensationHandler");
    let requires_compensation = optional_attribute(element, decoder, b"requiresCompensation")
        .is_some_and(|value| value == "true");
    let is_compensation_handler = optional_attribute(element, decoder, b"isForCompensation")
        .is_some_and(|value| value == "true");
    let default_flow_id = optional_non_empty_attribute(element, decoder, b"default");
    let enum_values = optional_non_empty_attribute(element, decoder, b"enumValues")
        .map(|values| {
            values
                .split(',')
                .map(str::trim)
                .filter(|value| !value.is_empty())
                .map(ToOwned::to_owned)
                .collect()
        })
        .unwrap_or_default();
    if parsed
        .nodes
        .insert(
            id.clone(),
            RawNode {
                kind,
                task_type,
                input_type,
                output_type,
                decision_ref,
                sla_milliseconds,
                compensation_handler_id,
                requires_compensation,
                is_compensation_handler,
                default_flow_id,
                enum_values,
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

fn insert_boundary_event(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) {
    let id = required_attribute(
        element,
        decoder,
        "boundaryEvent",
        b"id",
        offset,
        parsed,
        locations,
    );
    let attached_to = required_attribute(
        element,
        decoder,
        "boundaryEvent",
        b"attachedToRef",
        offset,
        parsed,
        locations,
    );
    if let (Some(id), Some(attached_to)) = (id, attached_to) {
        if parsed.nodes.contains_key(&id)
            || parsed.boundary_events.iter().any(|event| event.id == id)
        {
            parsed
                .diagnostics
                .push(locations.diagnostic(offset, DiagnosticKind::DuplicateId { id }));
            return;
        }
        parsed.open_boundary_event = Some(id.clone());
        parsed.boundary_events.push(RawBoundaryEvent {
            id,
            attached_to,
            is_compensation: false,
            offset,
        });
    }
}

fn mark_compensation_boundary(parsed: &mut ParsedProcess) {
    let Some(boundary_id) = parsed.open_boundary_event.as_deref() else {
        return;
    };
    if let Some(boundary) = parsed
        .boundary_events
        .iter_mut()
        .find(|boundary| boundary.id == boundary_id)
    {
        boundary.is_compensation = true;
    }
}

fn insert_association(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) {
    let id = required_attribute(
        element,
        decoder,
        "association",
        b"id",
        offset,
        parsed,
        locations,
    );
    let source = required_attribute(
        element,
        decoder,
        "association",
        b"sourceRef",
        offset,
        parsed,
        locations,
    );
    let target = required_attribute(
        element,
        decoder,
        "association",
        b"targetRef",
        offset,
        parsed,
        locations,
    );
    if let (Some(id), Some(source), Some(target)) = (id, source, target) {
        parsed.associations.push(RawAssociation {
            id,
            source,
            target,
            offset,
        });
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
            condition: optional_non_empty_attribute(element, decoder, b"condition"),
            is_default: optional_attribute(element, decoder, b"isDefault")
                .is_some_and(|value| value == "true"),
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

fn optional_non_empty_attribute(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    attribute_name: &[u8],
) -> Option<String> {
    optional_attribute(element, decoder, attribute_name).filter(|value| !value.trim().is_empty())
}

fn parse_optional_u64_attribute(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    attribute_name: &'static [u8],
    element_name: &str,
    offset: usize,
    parsed: &mut ParsedProcess,
    locations: &SourceLocations,
) -> Option<u64> {
    let value = optional_non_empty_attribute(element, decoder, attribute_name)?;
    match value.parse::<u64>() {
        Ok(value) if value > 0 => Some(value),
        _ => {
            parsed.diagnostics.push(locations.diagnostic(
                offset,
                DiagnosticKind::Xml {
                    detail: format!(
                        "{element_name} attribute {} must be a positive integer",
                        String::from_utf8_lossy(attribute_name)
                    ),
                },
            ));
            None
        }
    }
}

// The passes share one diagnostic set and borrowed graph indexes.
#[allow(clippy::match_same_arms, clippy::too_many_lines)]
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
    resolve_compensation_handlers(parsed, locations);

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
        if node.is_compensation_handler {
            if outgoing_count != 0 || incoming_count != 0 {
                parsed.diagnostics.push(
                    locations.diagnostic(
                        node.offset,
                        DiagnosticKind::InvalidCompensation {
                            activity_id: id.clone(),
                            detail:
                                "compensation handler must not be on the normal sequence-flow path"
                                    .into(),
                        },
                    ),
                );
            }
            continue;
        }
        let invalid_outgoing = match node.kind {
            RawNodeKind::ExclusiveGateway => outgoing_count < 2,
            RawNodeKind::InclusiveGateway | RawNodeKind::ParallelGateway => {
                !((incoming_count == 1 && outgoing_count >= 2)
                    || (incoming_count >= 2 && outgoing_count == 1))
            }
            RawNodeKind::End => outgoing_count != 0,
            RawNodeKind::Start | RawNodeKind::ServiceTask | RawNodeKind::DecisionTask => {
                outgoing_count != 1
            }
        };
        if invalid_outgoing {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::InvalidOutgoingCount {
                    node_id: id.clone(),
                    actual: outgoing_count,
                },
            ));
        }
        let valid_incoming = match node.kind {
            RawNodeKind::Start => incoming_count == 0,
            RawNodeKind::InclusiveGateway | RawNodeKind::ParallelGateway => {
                (incoming_count == 1 && outgoing_count >= 2)
                    || (incoming_count >= 2 && outgoing_count == 1)
            }
            _ => incoming_count == 1,
        };
        if !valid_incoming {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::InvalidIncomingCount {
                    node_id: id.clone(),
                    actual: incoming_count,
                },
            ));
        }

        if node.requires_compensation && node.compensation_handler_id.is_none() {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::MissingCompensation {
                    activity_id: id.clone(),
                },
            ));
        }
        if node.kind == RawNodeKind::DecisionTask && node.decision_ref.is_none() {
            parsed.diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::MissingAttribute {
                    element: "businessRuleTask".to_owned(),
                    attribute: "decisionRef",
                },
            ));
        }

        if node.kind == RawNodeKind::ExclusiveGateway
            || (node.kind == RawNodeKind::InclusiveGateway && outgoing_count >= 2)
        {
            let outgoing_flows: Vec<_> = parsed
                .flows
                .iter()
                .filter(|flow| flow.source == *id)
                .collect();
            let default_count = outgoing_flows
                .iter()
                .filter(|flow| {
                    flow.is_default || node.default_flow_id.as_deref() == Some(flow.id.as_str())
                })
                .count();
            if default_count > 1 {
                parsed.diagnostics.push(locations.diagnostic(
                    node.offset,
                    DiagnosticKind::NonExhaustiveGateway {
                        gateway_id: id.clone(),
                        detail: "configure at most one default flow".into(),
                    },
                ));
                continue;
            }
            let all_non_default_conditioned = outgoing_flows.iter().all(|flow| {
                flow.is_default
                    || node.default_flow_id.as_deref() == Some(flow.id.as_str())
                    || flow.condition.is_some()
            });
            if !all_non_default_conditioned {
                parsed.diagnostics.push(locations.diagnostic(
                    node.offset,
                    DiagnosticKind::NonExhaustiveGateway {
                        gateway_id: id.clone(),
                        detail: "every non-default branch must declare a guard".into(),
                    },
                ));
                continue;
            }
            if default_count == 0 {
                match analyze_gateway_coverage(
                    &outgoing_flows,
                    &node.enum_values,
                    node.kind == RawNodeKind::InclusiveGateway,
                ) {
                    Ok(_) => {}
                    Err(CoverageError::Ambiguous(detail))
                        if node.kind == RawNodeKind::ExclusiveGateway =>
                    {
                        parsed.diagnostics.push(locations.diagnostic(
                            node.offset,
                            DiagnosticKind::AmbiguousGatewayCoverage {
                                gateway_id: id.clone(),
                                detail,
                            },
                        ));
                    }
                    Err(CoverageError::Ambiguous(_)) => {}
                    Err(CoverageError::NonExhaustive(detail)) => {
                        parsed.diagnostics.push(locations.diagnostic(
                            node.offset,
                            DiagnosticKind::NonExhaustiveGateway {
                                gateway_id: id.clone(),
                                detail,
                            },
                        ));
                    }
                }
            }
        }
    }

    for flow in &parsed.flows {
        if !parsed.nodes.contains_key(&flow.source) || !parsed.nodes.contains_key(&flow.target) {
            continue;
        }
        if let Some(condition) = &flow.condition
            && let Err(detail) = parse_guard(condition)
        {
            parsed.diagnostics.push(locations.diagnostic(
                flow.offset,
                DiagnosticKind::InvalidGuardExpression {
                    flow_id: flow.id.clone(),
                    detail,
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
            if node.is_compensation_handler {
                continue;
            }
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
    let mut data_diagnostics = Vec::new();
    validate_data_flow_paths(parsed, locations, &mut data_diagnostics);
    parsed.diagnostics.append(&mut data_diagnostics);
    validate_gateway_structure(parsed, locations);
    validate_sla_paths(parsed, locations);
}

fn validate_sla_paths(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    let Some(process_sla) = parsed.process_sla_milliseconds else {
        return;
    };
    let normal_nodes = parsed
        .nodes
        .iter()
        .filter(|(_, node)| !node.is_compensation_handler)
        .map(|(id, node)| (id.clone(), node))
        .collect::<BTreeMap<_, _>>();
    let Some(start) = normal_nodes
        .iter()
        .find(|(_, node)| node.kind == RawNodeKind::Start)
        .map(|(id, _)| id.clone())
    else {
        return;
    };
    let mut indegree = normal_nodes
        .keys()
        .map(|id| (id.clone(), 0_usize))
        .collect::<BTreeMap<_, _>>();
    let mut outgoing = BTreeMap::<String, Vec<String>>::new();
    for flow in &parsed.flows {
        if indegree.contains_key(&flow.source) && indegree.contains_key(&flow.target) {
            outgoing
                .entry(flow.source.clone())
                .or_default()
                .push(flow.target.clone());
            *indegree.get_mut(&flow.target).expect("target was checked") += 1;
        }
    }
    let mut pending = indegree
        .iter()
        .filter(|(_, count)| **count == 0)
        .map(|(id, _)| id.clone())
        .collect::<VecDeque<_>>();
    let mut longest = BTreeMap::<String, (u64, Vec<String>)>::new();
    longest.insert(
        start.clone(),
        (
            normal_nodes[&start].sla_milliseconds.unwrap_or_default(),
            vec![start],
        ),
    );
    while let Some(node_id) = pending.pop_front() {
        let current = longest.get(&node_id).cloned();
        for target in outgoing.get(&node_id).into_iter().flatten() {
            if let Some((sum, path)) = &current {
                let candidate =
                    sum.saturating_add(normal_nodes[target].sla_milliseconds.unwrap_or_default());
                if longest
                    .get(target)
                    .is_none_or(|(known, _)| candidate > *known)
                {
                    let mut candidate_path = path.clone();
                    candidate_path.push(target.clone());
                    longest.insert(target.clone(), (candidate, candidate_path));
                }
            }
            let count = indegree.get_mut(target).expect("target was checked");
            *count -= 1;
            if *count == 0 {
                pending.push_back(target.clone());
            }
        }
    }
    let violation = normal_nodes
        .iter()
        .filter(|(_, node)| node.kind == RawNodeKind::End)
        .filter_map(|(id, node)| {
            longest
                .get(id)
                .filter(|(sum, _)| *sum > process_sla)
                .map(|(sum, path)| (*sum, path.clone(), node.offset))
        })
        .max_by_key(|(sum, _, _)| *sum);
    if let Some((sum, path, offset)) = violation {
        parsed.diagnostics.push(locations.diagnostic(
            offset,
            DiagnosticKind::SlaConflict {
                detail: format!(
                    "path {} totals {sum}ms, exceeding process SLA {process_sla}ms",
                    path.join(" -> ")
                ),
            },
        ));
    }
}

fn resolve_compensation_handlers(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    let boundaries = parsed
        .boundary_events
        .iter()
        .filter(|boundary| boundary.is_compensation)
        .cloned()
        .collect::<Vec<_>>();
    for boundary in boundaries {
        let Some(activity) = parsed.nodes.get(&boundary.attached_to) else {
            parsed.diagnostics.push(locations.diagnostic(
                boundary.offset,
                DiagnosticKind::UnresolvedReference {
                    flow_id: boundary.id,
                    missing_id: boundary.attached_to,
                },
            ));
            continue;
        };
        let targets = parsed
            .associations
            .iter()
            .filter(|association| association.source == boundary.id)
            .collect::<Vec<_>>();
        let Some(association) = targets.first() else {
            parsed.diagnostics.push(locations.diagnostic(
                activity.offset,
                DiagnosticKind::MissingCompensation {
                    activity_id: boundary.attached_to,
                },
            ));
            continue;
        };
        if targets.len() > 1 {
            parsed.diagnostics.push(locations.diagnostic(
                association.offset,
                DiagnosticKind::InvalidCompensation {
                    activity_id: boundary.attached_to,
                    detail: "compensation boundary must target exactly one handler".into(),
                },
            ));
            continue;
        }
        let Some(handler) = parsed.nodes.get(&association.target) else {
            parsed.diagnostics.push(locations.diagnostic(
                association.offset,
                DiagnosticKind::UnresolvedReference {
                    flow_id: association.id.clone(),
                    missing_id: association.target.clone(),
                },
            ));
            continue;
        };
        if !handler.is_compensation_handler {
            parsed.diagnostics.push(locations.diagnostic(
                handler.offset,
                DiagnosticKind::InvalidCompensation {
                    activity_id: boundary.attached_to,
                    detail: format!(
                        "association target {} must declare isForCompensation=true",
                        association.target
                    ),
                },
            ));
            continue;
        }
        if let Some(activity) = parsed.nodes.get_mut(&boundary.attached_to) {
            activity.requires_compensation = true;
            activity.compensation_handler_id = Some(association.target.clone());
        }
    }
}

fn traverse<'a>(start: &'a str, edges: &BTreeMap<&'a str, Vec<&'a str>>) -> BTreeSet<&'a str> {
    traverse_many(&[start], edges)
}

#[allow(clippy::too_many_lines)]
fn validate_gateway_structure(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    let mut outgoing = BTreeMap::<String, Vec<String>>::new();
    let mut incoming = BTreeMap::<String, Vec<String>>::new();
    for flow in &parsed.flows {
        outgoing
            .entry(flow.source.clone())
            .or_default()
            .push(flow.target.clone());
        incoming
            .entry(flow.target.clone())
            .or_default()
            .push(flow.source.clone());
    }
    for flow in &parsed.flows {
        if parsed.nodes.get(&flow.source).is_some_and(|node| {
            node.kind == RawNodeKind::ParallelGateway
                && outgoing
                    .get(&flow.source)
                    .is_some_and(|targets| targets.len() >= 2)
        }) && (flow.condition.is_some() || flow.is_default)
        {
            parsed.diagnostics.push(locations.diagnostic(
                flow.offset,
                DiagnosticKind::InvalidGatewayFlow {
                    gateway_id: flow.source.clone(),
                    detail: "parallel split flows cannot declare guards or defaults".into(),
                },
            ));
        }
    }

    if let Some(cycle_node) = first_cycle(&parsed.nodes, &outgoing) {
        let offset = parsed.nodes.get(&cycle_node).map_or(0, |node| node.offset);
        parsed.diagnostics.push(locations.diagnostic(
            offset,
            DiagnosticKind::UnexpectedCycle {
                node_id: cycle_node,
            },
        ));
        return;
    }

    let gateways = parsed
        .nodes
        .iter()
        .filter(|(_, node)| {
            matches!(
                node.kind,
                RawNodeKind::ParallelGateway | RawNodeKind::InclusiveGateway
            )
        })
        .map(|(id, node)| (id.clone(), node.kind, node.offset))
        .collect::<Vec<_>>();
    let joins = gateways
        .iter()
        .filter(|(id, _, _)| incoming.get(id).map_or(0, Vec::len) >= 2)
        .collect::<Vec<_>>();
    let mut paired_joins = BTreeSet::new();

    for (split_id, split_kind, split_offset) in gateways
        .iter()
        .filter(|(id, _, _)| outgoing.get(id).map_or(0, Vec::len) >= 2)
    {
        let branches = outgoing.get(split_id).map_or(&[][..], Vec::as_slice);
        let mut candidates = joins
            .iter()
            .filter(|(_, join_kind, _)| join_kind == split_kind)
            .filter_map(|(join_id, _, _)| {
                let distances = branches
                    .iter()
                    .map(|branch| shortest_distance(branch, join_id, &outgoing))
                    .collect::<Option<Vec<_>>>()?;
                Some((
                    distances.into_iter().max().unwrap_or_default(),
                    (*join_id).clone(),
                ))
            })
            .collect::<Vec<_>>();
        candidates.sort_unstable();
        let Some((_, join_id)) = candidates.first() else {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: split_id.clone(),
                    detail: "split has no reachable matching join".into(),
                },
            ));
            continue;
        };
        if !branches
            .iter()
            .all(|branch| all_paths_reach_join(branch, join_id, &outgoing))
        {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: split_id.clone(),
                    detail: format!("a branch can escape before paired join {join_id}"),
                },
            ));
            continue;
        }
        if !paired_joins.insert(join_id.clone()) {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: split_id.clone(),
                    detail: format!("join {join_id} is paired with more than one split"),
                },
            ));
            continue;
        }
        parsed
            .gateway_pairs
            .insert(split_id.clone(), join_id.clone());
        parsed
            .gateway_pairs
            .insert(join_id.clone(), split_id.clone());
    }

    for (join_id, _, offset) in joins {
        if !paired_joins.contains(join_id) {
            parsed.diagnostics.push(locations.diagnostic(
                *offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: join_id.clone(),
                    detail: "join has no matching split".into(),
                },
            ));
        }
    }
}

fn shortest_distance(
    start: &str,
    target: &str,
    outgoing: &BTreeMap<String, Vec<String>>,
) -> Option<usize> {
    let mut pending = VecDeque::from([(start, 0_usize)]);
    let mut visited = BTreeSet::new();
    while let Some((node, distance)) = pending.pop_front() {
        if node == target {
            return Some(distance);
        }
        if !visited.insert(node) {
            continue;
        }
        if let Some(targets) = outgoing.get(node) {
            pending.extend(targets.iter().map(|next| (next.as_str(), distance + 1)));
        }
    }
    None
}

fn all_paths_reach_join(start: &str, join: &str, outgoing: &BTreeMap<String, Vec<String>>) -> bool {
    let mut pending = vec![start];
    let mut visited = BTreeSet::new();
    while let Some(node) = pending.pop() {
        if node == join || !visited.insert(node) {
            continue;
        }
        let Some(targets) = outgoing.get(node).filter(|targets| !targets.is_empty()) else {
            return false;
        };
        pending.extend(targets.iter().map(String::as_str));
    }
    true
}

fn first_cycle(
    nodes: &BTreeMap<String, RawNode>,
    outgoing: &BTreeMap<String, Vec<String>>,
) -> Option<String> {
    let mut indegree = nodes
        .keys()
        .map(|node| (node.clone(), 0_usize))
        .collect::<BTreeMap<_, _>>();
    for targets in outgoing.values() {
        for target in targets {
            if let Some(count) = indegree.get_mut(target) {
                *count = count.saturating_add(1);
            }
        }
    }
    let mut ready = indegree
        .iter()
        .filter(|(_, count)| **count == 0)
        .map(|(node, _)| node.clone())
        .collect::<VecDeque<_>>();
    while let Some(node) = ready.pop_front() {
        if let Some(targets) = outgoing.get(&node) {
            for target in targets {
                let Some(count) = indegree.get_mut(target) else {
                    continue;
                };
                *count -= 1;
                if *count == 0 {
                    ready.push_back(target.clone());
                }
            }
        }
        indegree.remove(&node);
    }
    indegree.into_keys().next()
}

fn validate_decision_references(
    parsed: &ParsedProcess,
    decision_tables: &[DecisionTable],
    locations: &SourceLocations,
    diagnostics: &mut Vec<CompileDiagnostic>,
) {
    let table_ids = decision_tables
        .iter()
        .map(|table| table.id.as_str())
        .collect::<BTreeSet<_>>();
    for (task_id, node) in &parsed.nodes {
        if node.kind != RawNodeKind::DecisionTask {
            continue;
        }
        let Some(decision_ref) = &node.decision_ref else {
            continue;
        };
        if !table_ids.contains(decision_ref.as_str()) {
            diagnostics.push(locations.diagnostic(
                node.offset,
                DiagnosticKind::MissingDecisionTable {
                    task_id: task_id.clone(),
                    table_id: decision_ref.clone(),
                },
            ));
        }
    }
}

fn enrich_decision_contracts(
    parsed: &mut ParsedProcess,
    decision_tables: &[DecisionTable],
    locations: &SourceLocations,
    diagnostics: &mut Vec<CompileDiagnostic>,
) {
    let tables = decision_tables
        .iter()
        .map(|table| (table.id.as_str(), table))
        .collect::<BTreeMap<_, _>>();
    for (node_id, node) in &mut parsed.nodes {
        if node.kind != RawNodeKind::DecisionTask {
            continue;
        }
        let Some(table) = node
            .decision_ref
            .as_deref()
            .and_then(|decision_ref| tables.get(decision_ref).copied())
        else {
            continue;
        };
        if table.inputs.len() == 1 {
            merge_inferred_contract(
                node_id,
                &mut node.input_type,
                value_type_name(table.inputs[0].value_type),
                "DMN input",
                node.offset,
                locations,
                diagnostics,
            );
        }
        if table.outputs.len() == 1 {
            merge_inferred_contract(
                node_id,
                &mut node.output_type,
                value_type_name(table.outputs[0].value_type),
                "DMN output",
                node.offset,
                locations,
                diagnostics,
            );
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn merge_inferred_contract(
    node_id: &str,
    declared: &mut Option<String>,
    inferred: Option<&'static str>,
    boundary: &'static str,
    offset: usize,
    locations: &SourceLocations,
    diagnostics: &mut Vec<CompileDiagnostic>,
) {
    let Some(inferred) = inferred else {
        return;
    };
    if let Some(actual) = declared {
        if !actual.eq_ignore_ascii_case(inferred) {
            diagnostics.push(locations.diagnostic(
                offset,
                DiagnosticKind::DataContractMismatch {
                    from: format!("{node_id} {boundary}"),
                    to: node_id.to_owned(),
                    expected: inferred.into(),
                    actual: actual.clone(),
                },
            ));
        }
    } else {
        *declared = Some(inferred.into());
    }
}

fn value_type_name(value_type: i32) -> Option<&'static str> {
    match ValueType::try_from(value_type).ok()? {
        ValueType::Boolean => Some("boolean"),
        ValueType::Integer => Some("integer"),
        ValueType::String => Some("string"),
        ValueType::Unspecified => None,
    }
}

fn validate_data_flow_paths(
    parsed: &ParsedProcess,
    locations: &SourceLocations,
    diagnostics: &mut Vec<CompileDiagnostic>,
) {
    let outgoing =
        parsed
            .flows
            .iter()
            .fold(BTreeMap::<&str, Vec<&str>>::new(), |mut graph, flow| {
                graph
                    .entry(flow.source.as_str())
                    .or_default()
                    .push(flow.target.as_str());
                graph
            });
    let mut received = BTreeMap::<String, BTreeMap<String, String>>::new();
    let mut pending = parsed
        .nodes
        .iter()
        .filter(|(_, node)| !node.is_compensation_handler)
        .map(|(id, _)| id.clone())
        .collect::<VecDeque<_>>();
    let mut mismatches = BTreeSet::new();
    while let Some(node_id) = pending.pop_front() {
        let Some(node) = parsed.nodes.get(&node_id) else {
            continue;
        };
        let inputs = received.get(&node_id).cloned().unwrap_or_default();
        if let Some(expected) = &node.input_type {
            for (actual, origin) in &inputs {
                if actual != expected
                    && mismatches.insert((origin.clone(), node_id.clone(), actual.clone()))
                {
                    diagnostics.push(locations.diagnostic(
                        node.offset,
                        DiagnosticKind::DataContractMismatch {
                            from: origin.clone(),
                            to: node_id.clone(),
                            expected: expected.clone(),
                            actual: actual.clone(),
                        },
                    ));
                }
            }
        }
        let outputs = node.output_type.as_ref().map_or_else(
            || {
                if node.kind == RawNodeKind::DecisionTask {
                    BTreeMap::new()
                } else {
                    inputs
                }
            },
            |output| BTreeMap::from([(output.clone(), node_id.clone())]),
        );
        for target in outgoing.get(node_id.as_str()).into_iter().flatten() {
            let target_inputs = received.entry((*target).to_owned()).or_default();
            let mut changed = false;
            for (value_type, origin) in &outputs {
                changed |= target_inputs
                    .insert(value_type.clone(), origin.clone())
                    .is_none();
            }
            if changed {
                pending.push_back((*target).to_owned());
            }
        }
    }
}

#[derive(Debug, Clone)]
enum CoverageProof {
    Boolean {
        variable: String,
    },
    Enum {
        variable: String,
        values: Vec<String>,
    },
    Integer {
        variable: String,
        intervals: Vec<CoverageInterval>,
    },
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
struct CoverageInterval {
    lower: Option<i64>,
    upper: Option<i64>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
enum CoverageError {
    Ambiguous(String),
    NonExhaustive(String),
}

fn analyze_gateway_coverage(
    outgoing_flows: &[&RawFlow],
    enum_values: &[String],
    allow_overlap: bool,
) -> Result<CoverageProof, CoverageError> {
    let mut guards = Vec::with_capacity(outgoing_flows.len());
    for flow in outgoing_flows {
        let condition = flow
            .condition
            .as_deref()
            .ok_or_else(|| CoverageError::NonExhaustive("branch has no guard".into()))?;
        guards.push(parse_guard(condition).map_err(CoverageError::NonExhaustive)?);
    }
    if guards.is_empty() {
        return Err(CoverageError::NonExhaustive(
            "gateway has no guarded branches".into(),
        ));
    }
    let variable = guards[0].variable.clone();
    if guards.iter().any(|guard| guard.variable != variable) {
        return Err(CoverageError::NonExhaustive(
            "static coverage currently requires all branches to test the same variable".into(),
        ));
    }
    match guards[0].literal.as_ref() {
        Some(guard_expression::Literal::BooleanValue(_)) => {
            analyze_boolean_coverage(variable, &guards, allow_overlap)
        }
        Some(guard_expression::Literal::StringValue(_)) => {
            analyze_enum_coverage(variable, &guards, enum_values, allow_overlap)
        }
        Some(guard_expression::Literal::IntegerValue(_)) => {
            analyze_integer_coverage(variable, &guards, allow_overlap)
        }
        None => Err(CoverageError::NonExhaustive(
            "guard literal is missing".into(),
        )),
    }
}

fn analyze_boolean_coverage(
    variable: String,
    guards: &[GuardExpression],
    allow_overlap: bool,
) -> Result<CoverageProof, CoverageError> {
    let mut covered = BTreeSet::new();
    for guard in guards {
        let Some(guard_expression::Literal::BooleanValue(value)) = guard.literal else {
            return Err(CoverageError::NonExhaustive(
                "boolean coverage cannot mix literal types".into(),
            ));
        };
        let values = match ComparisonOperator::try_from(guard.operator) {
            Ok(ComparisonOperator::Equal) => vec![value],
            Ok(ComparisonOperator::NotEqual) => vec![!value],
            Ok(_) | Err(_) => {
                return Err(CoverageError::NonExhaustive(
                    "boolean coverage supports only == and !=".into(),
                ));
            }
        };
        for value in values {
            if !covered.insert(value) && !allow_overlap {
                return Err(CoverageError::Ambiguous(format!(
                    "boolean value {value} is matched by more than one branch"
                )));
            }
        }
    }
    if covered.len() == 2 {
        Ok(CoverageProof::Boolean { variable })
    } else {
        Err(CoverageError::NonExhaustive(
            "boolean domain is missing true or false".into(),
        ))
    }
}

fn analyze_enum_coverage(
    variable: String,
    guards: &[GuardExpression],
    enum_values: &[String],
    allow_overlap: bool,
) -> Result<CoverageProof, CoverageError> {
    if enum_values.is_empty() {
        return Err(CoverageError::NonExhaustive(
            "string enum coverage requires enumValues on the gateway".into(),
        ));
    }
    let declared: BTreeSet<_> = enum_values.iter().cloned().collect();
    if declared.len() != enum_values.len() {
        return Err(CoverageError::Ambiguous(
            "gateway enumValues contains duplicate values".into(),
        ));
    }
    let mut covered = BTreeSet::new();
    for guard in guards {
        let Some(guard_expression::Literal::StringValue(value)) = guard.literal.as_ref() else {
            return Err(CoverageError::NonExhaustive(
                "enum coverage cannot mix literal types".into(),
            ));
        };
        if !matches!(
            ComparisonOperator::try_from(guard.operator),
            Ok(ComparisonOperator::Equal)
        ) {
            return Err(CoverageError::NonExhaustive(
                "enum coverage supports only equality guards".into(),
            ));
        }
        if !declared.contains(value) {
            return Err(CoverageError::NonExhaustive(format!(
                "guard value {value} is outside gateway enumValues"
            )));
        }
        if !covered.insert(value.clone()) && !allow_overlap {
            return Err(CoverageError::Ambiguous(format!(
                "enum value {value} is matched by more than one branch"
            )));
        }
    }
    let missing: Vec<_> = declared.difference(&covered).cloned().collect();
    if missing.is_empty() {
        Ok(CoverageProof::Enum {
            variable,
            values: enum_values.to_vec(),
        })
    } else {
        Err(CoverageError::NonExhaustive(format!(
            "enum domain is missing values {}",
            missing.join(",")
        )))
    }
}

fn analyze_integer_coverage(
    variable: String,
    guards: &[GuardExpression],
    allow_overlap: bool,
) -> Result<CoverageProof, CoverageError> {
    let mut intervals = Vec::new();
    for guard in guards {
        let Some(guard_expression::Literal::IntegerValue(value)) = guard.literal else {
            return Err(CoverageError::NonExhaustive(
                "integer coverage cannot mix literal types".into(),
            ));
        };
        intervals.extend(integer_guard_intervals(
            ComparisonOperator::try_from(guard.operator)
                .map_err(|_| CoverageError::NonExhaustive("invalid comparison operator".into()))?,
            value,
        )?);
    }
    if allow_overlap {
        merge_and_validate_integer_cover(&mut intervals)?;
    } else {
        validate_disjoint_integer_cover(&mut intervals)?;
    }
    Ok(CoverageProof::Integer {
        variable,
        intervals,
    })
}

fn merge_and_validate_integer_cover(
    intervals: &mut Vec<CoverageInterval>,
) -> Result<(), CoverageError> {
    if intervals.is_empty() {
        return Err(CoverageError::NonExhaustive(
            "integer coverage has no intervals".into(),
        ));
    }
    intervals.sort_unstable_by_key(|interval| (interval.lower.unwrap_or(i64::MIN), interval.upper));
    let mut merged = Vec::<CoverageInterval>::new();
    for interval in intervals.iter().copied() {
        if let Some(last) = merged.last_mut() {
            let overlaps_or_touches = match last.upper {
                None | Some(i64::MAX) => true,
                Some(upper) => interval
                    .lower
                    .is_none_or(|lower| lower <= upper.saturating_add(1)),
            };
            if overlaps_or_touches {
                last.upper = match (last.upper, interval.upper) {
                    (None, _) | (_, None) => None,
                    (Some(left), Some(right)) => Some(left.max(right)),
                };
                continue;
            }
        }
        merged.push(interval);
    }
    if merged.len() == 1 && merged[0].lower.is_none() && merged[0].upper.is_none() {
        *intervals = merged;
        Ok(())
    } else {
        Err(CoverageError::NonExhaustive(
            "integer intervals do not cover the full domain".into(),
        ))
    }
}

fn integer_guard_intervals(
    operator: ComparisonOperator,
    value: i64,
) -> Result<Vec<CoverageInterval>, CoverageError> {
    let intervals = match operator {
        ComparisonOperator::Equal => vec![CoverageInterval {
            lower: Some(value),
            upper: Some(value),
        }],
        ComparisonOperator::NotEqual => {
            let mut intervals = Vec::new();
            if let Some(upper) = value.checked_sub(1) {
                intervals.push(CoverageInterval {
                    lower: None,
                    upper: Some(upper),
                });
            }
            if let Some(lower) = value.checked_add(1) {
                intervals.push(CoverageInterval {
                    lower: Some(lower),
                    upper: None,
                });
            }
            intervals
        }
        ComparisonOperator::LessThan => value
            .checked_sub(1)
            .map(|upper| {
                vec![CoverageInterval {
                    lower: None,
                    upper: Some(upper),
                }]
            })
            .unwrap_or_default(),
        ComparisonOperator::LessThanOrEqual => vec![CoverageInterval {
            lower: None,
            upper: Some(value),
        }],
        ComparisonOperator::GreaterThan => value
            .checked_add(1)
            .map(|lower| {
                vec![CoverageInterval {
                    lower: Some(lower),
                    upper: None,
                }]
            })
            .unwrap_or_default(),
        ComparisonOperator::GreaterThanOrEqual => vec![CoverageInterval {
            lower: Some(value),
            upper: None,
        }],
        ComparisonOperator::Unspecified => {
            return Err(CoverageError::NonExhaustive(
                "invalid comparison operator".into(),
            ));
        }
    };
    Ok(intervals)
}

fn validate_disjoint_integer_cover(
    intervals: &mut [CoverageInterval],
) -> Result<(), CoverageError> {
    if intervals.is_empty() {
        return Err(CoverageError::NonExhaustive(
            "integer coverage has no intervals".into(),
        ));
    }
    intervals.sort_unstable_by_key(|interval| (interval.lower.unwrap_or(i64::MIN), interval.upper));
    let mut expected_lower = None;
    for interval in intervals.iter() {
        match compare_interval_lower(interval.lower, expected_lower) {
            std::cmp::Ordering::Less => {
                return Err(CoverageError::Ambiguous("integer intervals overlap".into()));
            }
            std::cmp::Ordering::Greater => {
                return Err(CoverageError::NonExhaustive(
                    "integer intervals leave an uncovered gap".into(),
                ));
            }
            std::cmp::Ordering::Equal => {}
        }
        if let (Some(lower), Some(upper)) = (interval.lower, interval.upper)
            && lower > upper
        {
            return Err(CoverageError::NonExhaustive(
                "integer interval lower bound is above upper bound".into(),
            ));
        }
        expected_lower = match interval.upper {
            Some(i64::MAX) | None => return Ok(()),
            Some(value) => value.checked_add(1),
        };
        if expected_lower.is_none() {
            return Err(CoverageError::NonExhaustive(
                "integer interval upper bound overflowed".into(),
            ));
        }
    }
    Err(CoverageError::NonExhaustive(
        "integer intervals do not cover the unbounded upper range".into(),
    ))
}

fn compare_interval_lower(actual: Option<i64>, expected: Option<i64>) -> std::cmp::Ordering {
    actual
        .unwrap_or(i64::MIN)
        .cmp(&expected.unwrap_or(i64::MIN))
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

#[allow(clippy::too_many_lines)]
fn lower(
    parsed: ParsedProcess,
    tenant_id: &str,
    workflow_version: &str,
) -> WorkflowIntermediateRepresentation {
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
        .filter(|(_, node)| !node.is_compensation_handler)
        .map(|(id, raw)| {
            let kind = match raw.kind {
                RawNodeKind::Start => node::Kind::Start(StartNode {
                    next_node_id: next_by_source[&id.as_str()].to_owned(),
                }),
                RawNodeKind::ServiceTask => node::Kind::ServiceTask(ServiceTaskNode {
                    task_type: raw.task_type.unwrap_or_else(|| id.clone()),
                    next_node_id: next_by_source[&id.as_str()].to_owned(),
                }),
                RawNodeKind::DecisionTask => node::Kind::DecisionTask(DecisionTaskNode {
                    decision_table_id: raw
                        .decision_ref
                        .expect("decisionRef is validated before lowering"),
                    next_node_id: next_by_source[&id.as_str()].to_owned(),
                }),
                RawNodeKind::ExclusiveGateway => {
                    let outgoing_flows: Vec<_> = parsed
                        .flows
                        .iter()
                        .filter(|flow| flow.source == id)
                        .collect();
                    let has_default = outgoing_flows.iter().any(|flow| {
                        flow.is_default || raw.default_flow_id.as_deref() == Some(flow.id.as_str())
                    });
                    let coverage = (!has_default).then(|| {
                        coverage_to_wire(
                            analyze_gateway_coverage(&outgoing_flows, &raw.enum_values, false)
                                .expect("gateway coverage is validated before lowering"),
                        )
                    });
                    let mut transitions: Vec<_> = parsed
                        .flows
                        .iter()
                        .filter(|flow| flow.source == id)
                        .map(|flow| ConditionalTransition {
                            target_node_id: flow.target.clone(),
                            condition: String::new(),
                            is_default: flow.is_default
                                || raw.default_flow_id.as_deref() == Some(flow.id.as_str()),
                            guard: flow.condition.as_deref().map(|condition| {
                                parse_guard(condition)
                                    .expect("guard expressions are validated before lowering")
                            }),
                        })
                        .collect();
                    transitions.sort_unstable_by(|left, right| {
                        left.target_node_id.cmp(&right.target_node_id)
                    });
                    node::Kind::ExclusiveGateway(ExclusiveGatewayNode {
                        transitions,
                        coverage,
                    })
                }
                RawNodeKind::ParallelGateway => {
                    let outgoing_flows = parsed
                        .flows
                        .iter()
                        .filter(|flow| flow.source == id)
                        .collect::<Vec<_>>();
                    let is_split = outgoing_flows.len() >= 2;
                    node::Kind::ParallelGateway(ParallelGatewayNode {
                        direction: if is_split {
                            GatewayDirection::Split.into()
                        } else {
                            GatewayDirection::Join.into()
                        },
                        target_node_ids: outgoing_flows
                            .iter()
                            .map(|flow| flow.target.clone())
                            .collect(),
                        paired_gateway_id: parsed.gateway_pairs[&id].clone(),
                    })
                }
                RawNodeKind::InclusiveGateway => {
                    let outgoing_flows = parsed
                        .flows
                        .iter()
                        .filter(|flow| flow.source == id)
                        .collect::<Vec<_>>();
                    let is_split = outgoing_flows.len() >= 2;
                    let has_default = outgoing_flows.iter().any(|flow| {
                        flow.is_default || raw.default_flow_id.as_deref() == Some(flow.id.as_str())
                    });
                    let coverage = (is_split && !has_default).then(|| {
                        coverage_to_wire(
                            analyze_gateway_coverage(&outgoing_flows, &raw.enum_values, true)
                                .expect("inclusive gateway coverage is validated before lowering"),
                        )
                    });
                    let mut transitions = if is_split {
                        outgoing_flows
                            .iter()
                            .map(|flow| ConditionalTransition {
                                target_node_id: flow.target.clone(),
                                condition: String::new(),
                                is_default: flow.is_default
                                    || raw.default_flow_id.as_deref() == Some(flow.id.as_str()),
                                guard: flow.condition.as_deref().map(|condition| {
                                    parse_guard(condition)
                                        .expect("guard expressions are validated before lowering")
                                }),
                            })
                            .collect::<Vec<_>>()
                    } else {
                        Vec::new()
                    };
                    transitions.sort_unstable_by(|left, right| {
                        left.target_node_id.cmp(&right.target_node_id)
                    });
                    node::Kind::InclusiveGateway(InclusiveGatewayNode {
                        direction: if is_split {
                            GatewayDirection::Split.into()
                        } else {
                            GatewayDirection::Join.into()
                        },
                        transitions,
                        next_node_id: if is_split {
                            String::new()
                        } else {
                            outgoing_flows[0].target.clone()
                        },
                        paired_gateway_id: parsed.gateway_pairs[&id].clone(),
                        coverage,
                    })
                }
                RawNodeKind::End => node::Kind::End(EndNode {}),
            };
            Node {
                id,
                kind: Some(kind),
                data_contract: (raw.input_type.is_some() || raw.output_type.is_some()).then(|| {
                    DataContract {
                        input_type: raw.input_type.unwrap_or_default(),
                        output_type: raw.output_type.unwrap_or_default(),
                    }
                }),
                sla_milliseconds: raw.sla_milliseconds.unwrap_or_default(),
                compensation_handler_id: raw.compensation_handler_id.unwrap_or_default(),
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
        decision_tables: Vec::new(),
        tenant_id: tenant_id.to_owned(),
        case_models: Vec::new(),
    }
}

fn coverage_to_wire(proof: CoverageProof) -> GatewayCoverage {
    match proof {
        CoverageProof::Boolean { variable } => GatewayCoverage {
            variable,
            value_type: ValueType::Boolean.into(),
            enum_values: Vec::new(),
            integer_intervals: Vec::new(),
        },
        CoverageProof::Enum { variable, values } => GatewayCoverage {
            variable,
            value_type: ValueType::String.into(),
            enum_values: values,
            integer_intervals: Vec::new(),
        },
        CoverageProof::Integer {
            variable,
            intervals,
        } => GatewayCoverage {
            variable,
            value_type: ValueType::Integer.into(),
            enum_values: Vec::new(),
            integer_intervals: intervals
                .into_iter()
                .map(|interval| IntegerInterval {
                    lower_bound: interval.lower.unwrap_or_default(),
                    upper_bound: interval.upper.unwrap_or_default(),
                    lower_unbounded: interval.lower.is_none(),
                    upper_unbounded: interval.upper.is_none(),
                })
                .collect(),
        },
    }
}

fn parse_guard(source: &str) -> Result<GuardExpression, String> {
    let operators = [
        (">=", ComparisonOperator::GreaterThanOrEqual),
        ("<=", ComparisonOperator::LessThanOrEqual),
        ("!=", ComparisonOperator::NotEqual),
        ("==", ComparisonOperator::Equal),
        (">", ComparisonOperator::GreaterThan),
        ("<", ComparisonOperator::LessThan),
    ];
    let Some((token, operator)) = operators
        .into_iter()
        .find(|(token, _)| source.contains(token))
    else {
        return Err("expected one comparison operator: ==, !=, <, <=, >, >=".into());
    };
    let Some((variable, literal)) = source.split_once(token) else {
        return Err("comparison is malformed".into());
    };
    let variable = variable.trim();
    if variable.is_empty()
        || !variable
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b'-'))
    {
        return Err("variable name is empty or contains unsupported characters".into());
    }
    let literal = parse_guard_literal(literal.trim())?;
    if matches!(literal, guard_expression::Literal::BooleanValue(_))
        && !matches!(
            operator,
            ComparisonOperator::Equal | ComparisonOperator::NotEqual
        )
    {
        return Err("boolean values only support == and !=".into());
    }
    Ok(GuardExpression {
        variable: variable.to_owned(),
        operator: operator.into(),
        literal: Some(literal),
    })
}

fn parse_guard_literal(source: &str) -> Result<guard_expression::Literal, String> {
    match source {
        "true" => Ok(guard_expression::Literal::BooleanValue(true)),
        "false" => Ok(guard_expression::Literal::BooleanValue(false)),
        _ if source.starts_with('"') && source.ends_with('"') && source.len() >= 2 => Ok(
            guard_expression::Literal::StringValue(source[1..source.len() - 1].to_owned()),
        ),
        _ => source
            .parse::<i64>()
            .map(guard_expression::Literal::IntegerValue)
            .map_err(|_| "literal must be a boolean, signed integer, or quoted string".into()),
    }
}

#[allow(clippy::too_many_lines)]
fn parse_cmmn(
    source: SourceDocument<'_>,
    limits: CompilerLimits,
    locations: &SourceLocations,
) -> Result<Vec<CaseModel>, Vec<CompileDiagnostic>> {
    let mut reader = NsReader::from_reader(source.bytes);
    reader.config_mut().trim_text(true);
    let decoder = reader.decoder();
    let mut buffer = Vec::new();
    let mut depth = 0_u32;
    let mut previous_position = 0_usize;
    let mut diagnostics = Vec::new();
    let mut model = None::<CaseModel>;
    let mut identifiers = BTreeSet::new();

    loop {
        let offset = next_tag_offset(source.bytes, previous_position);
        match reader.read_resolved_event_into(&mut buffer) {
            Ok((namespace, Event::Start(element))) => {
                depth = depth.saturating_add(1);
                if depth > limits.max_xml_depth {
                    diagnostics.push(locations.diagnostic(
                        offset,
                        DiagnosticKind::XmlDepthExceeded {
                            actual: depth,
                            configured_limit: limits.max_xml_depth,
                        },
                    ));
                }
                inspect_cmmn_element(
                    &namespace,
                    &element,
                    decoder,
                    offset,
                    &mut model,
                    &mut identifiers,
                    locations,
                    &mut diagnostics,
                );
            }
            Ok((namespace, Event::Empty(element))) => inspect_cmmn_element(
                &namespace,
                &element,
                decoder,
                offset,
                &mut model,
                &mut identifiers,
                locations,
                &mut diagnostics,
            ),
            Ok((_, Event::End(_))) => depth = depth.saturating_sub(1),
            Ok((_, Event::DocType(_))) => diagnostics
                .push(locations.diagnostic(offset, DiagnosticKind::ForbiddenDocumentType)),
            Ok((_, Event::Eof)) => break,
            Ok(_) => {}
            Err(error) => {
                diagnostics.push(locations.diagnostic(
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

    let Some(mut model) = model else {
        diagnostics.push(locations.diagnostic(
            0,
            DiagnosticKind::InvalidCaseModel {
                case_id: source.name.to_owned(),
                detail: "CMMN document contains no case element".into(),
            },
        ));
        return Err(diagnostics);
    };
    let sentry_ids = model
        .sentries
        .iter()
        .map(|sentry| sentry.id.as_str())
        .collect::<BTreeSet<_>>();
    for (owner, sentry_ref) in model
        .stages
        .iter()
        .flat_map(|stage| {
            stage
                .entry_sentry_ids
                .iter()
                .chain(&stage.exit_sentry_ids)
                .map(move |reference| (stage.id.as_str(), reference.as_str()))
        })
        .chain(model.milestones.iter().flat_map(|milestone| {
            milestone
                .entry_sentry_ids
                .iter()
                .map(move |reference| (milestone.id.as_str(), reference.as_str()))
        }))
    {
        if !sentry_ids.contains(sentry_ref) {
            diagnostics.push(locations.diagnostic(
                0,
                DiagnosticKind::InvalidCaseModel {
                    case_id: model.id.clone(),
                    detail: format!("{owner} references missing sentry {sentry_ref}"),
                },
            ));
        }
    }
    model
        .stages
        .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    model
        .milestones
        .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    model
        .sentries
        .sort_unstable_by(|left, right| left.id.cmp(&right.id));
    if diagnostics.is_empty() {
        Ok(vec![model])
    } else {
        Err(diagnostics)
    }
}

#[allow(clippy::too_many_arguments)]
fn inspect_cmmn_element(
    namespace: &ResolveResult<'_>,
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    model: &mut Option<CaseModel>,
    identifiers: &mut BTreeSet<String>,
    locations: &SourceLocations,
    diagnostics: &mut Vec<CompileDiagnostic>,
) {
    let is_cmmn = matches!(namespace, ResolveResult::Bound(namespace) if CMMN_MODEL_NAMESPACES.contains(&namespace.as_ref()));
    if !is_cmmn {
        return;
    }
    let local_name = String::from_utf8_lossy(element.local_name().as_ref()).into_owned();
    let id = || optional_non_empty_attribute(element, decoder, b"id");
    let name = || optional_non_empty_attribute(element, decoder, b"name").unwrap_or_default();
    match local_name.as_str() {
        "case" => {
            let Some(case_id) = id() else {
                diagnostics.push(locations.diagnostic(
                    offset,
                    DiagnosticKind::MissingAttribute {
                        element: "case".into(),
                        attribute: "id",
                    },
                ));
                return;
            };
            if model.is_some() {
                diagnostics.push(locations.diagnostic(
                    offset,
                    DiagnosticKind::InvalidCaseModel {
                        case_id,
                        detail: "one CMMN source may contain exactly one case".into(),
                    },
                ));
                return;
            }
            identifiers.insert(case_id.clone());
            *model = Some(CaseModel {
                id: case_id,
                name: name(),
                stages: Vec::new(),
                milestones: Vec::new(),
                sentries: Vec::new(),
            });
        }
        "stage" | "milestone" | "sentry" => {
            let Some(item_id) = id() else {
                diagnostics.push(locations.diagnostic(
                    offset,
                    DiagnosticKind::MissingAttribute {
                        element: local_name,
                        attribute: "id",
                    },
                ));
                return;
            };
            if !identifiers.insert(item_id.clone()) {
                diagnostics.push(
                    locations.diagnostic(offset, DiagnosticKind::DuplicateId { id: item_id }),
                );
                return;
            }
            let Some(model) = model.as_mut() else {
                diagnostics.push(locations.diagnostic(
                    offset,
                    DiagnosticKind::InvalidCaseModel {
                        case_id: "<unknown>".into(),
                        detail: format!("{local_name} appears before case"),
                    },
                ));
                return;
            };
            let refs = |attribute| {
                optional_non_empty_attribute(element, decoder, attribute)
                    .map(|value| split_references(&value))
                    .unwrap_or_default()
            };
            match local_name.as_str() {
                "stage" => model.stages.push(CaseStage {
                    id: item_id,
                    name: name(),
                    entry_sentry_ids: refs(b"entrySentryRefs"),
                    exit_sentry_ids: refs(b"exitSentryRefs"),
                }),
                "milestone" => model.milestones.push(CaseMilestone {
                    id: item_id,
                    name: name(),
                    entry_sentry_ids: refs(b"entrySentryRefs"),
                }),
                "sentry" => model.sentries.push(CaseSentry {
                    id: item_id,
                    condition: optional_non_empty_attribute(element, decoder, b"condition")
                        .unwrap_or_default(),
                }),
                _ => unreachable!(),
            }
        }
        _ => {}
    }
}

fn split_references(value: &str) -> Vec<String> {
    value
        .split([',', ' '])
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect()
}

#[allow(clippy::too_many_lines)]
fn parse_dmn(
    source: SourceDocument<'_>,
    limits: CompilerLimits,
    locations: &SourceLocations,
) -> Result<Vec<DecisionTable>, Vec<CompileDiagnostic>> {
    let mut reader = NsReader::from_reader(source.bytes);
    reader.config_mut().trim_text(true);
    let decoder = reader.decoder();
    let mut buffer = Vec::new();
    let mut depth = 0_u32;
    let mut previous_position = 0_usize;
    let mut diagnostics = Vec::new();
    let mut tables = Vec::new();
    let mut current_table: Option<DecisionTable> = None;
    let mut current_rule: Option<DecisionRule> = None;
    let mut capture: Option<DmnCapture> = None;

    loop {
        let offset = next_tag_offset(source.bytes, previous_position);
        let event = reader.read_resolved_event_into(&mut buffer);
        match event {
            Ok((namespace, Event::Start(element))) => {
                depth = depth.saturating_add(1);
                if depth > limits.max_xml_depth {
                    diagnostics.push(locations.diagnostic(
                        offset,
                        DiagnosticKind::XmlDepthExceeded {
                            actual: depth,
                            configured_limit: limits.max_xml_depth,
                        },
                    ));
                }
                inspect_dmn_start(
                    &namespace,
                    &element,
                    decoder,
                    offset,
                    &mut current_table,
                    &mut current_rule,
                    &mut capture,
                    &mut diagnostics,
                    locations,
                );
            }
            Ok((namespace, Event::Empty(element))) => inspect_dmn_empty(
                &namespace,
                &element,
                decoder,
                offset,
                &mut current_table,
                &mut current_rule,
                &mut diagnostics,
                locations,
            ),
            Ok((namespace, Event::End(element))) => {
                if is_dmn_namespace(&namespace) {
                    let local_name =
                        String::from_utf8_lossy(element.local_name().as_ref()).into_owned();
                    match local_name.as_str() {
                        "rule" => {
                            if let (Some(table), Some(rule)) =
                                (&mut current_table, current_rule.take())
                            {
                                table.rules.push(rule);
                            }
                        }
                        "decisionTable" => {
                            if let Some(table) = current_table.take()
                                && let Some(table) = validate_decision_table(
                                    table,
                                    offset,
                                    &mut diagnostics,
                                    locations,
                                )
                            {
                                tables.push(table);
                            }
                        }
                        "inputEntry" | "outputEntry" => capture = None,
                        _ => {}
                    }
                }
                depth = depth.saturating_sub(1);
            }
            Ok((_, Event::Text(text))) => {
                if let Some(capture) = capture {
                    let value = String::from_utf8_lossy(text.as_ref()).trim().to_owned();
                    push_dmn_entry(
                        capture,
                        &value,
                        &mut current_rule,
                        offset,
                        &mut diagnostics,
                        locations,
                    );
                }
            }
            Ok((_, Event::DocType(_))) => {
                diagnostics
                    .push(locations.diagnostic(offset, DiagnosticKind::ForbiddenDocumentType));
            }
            Ok((_, Event::Eof)) => break,
            Ok(_) => {}
            Err(error) => {
                diagnostics.push(locations.diagnostic(
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
    if diagnostics.is_empty() {
        Ok(tables)
    } else {
        Err(diagnostics)
    }
}

#[derive(Debug, Clone, Copy)]
enum DmnCapture {
    InputEntry,
    OutputEntry,
}

#[allow(clippy::too_many_arguments)]
fn inspect_dmn_start(
    namespace: &ResolveResult<'_>,
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    current_table: &mut Option<DecisionTable>,
    current_rule: &mut Option<DecisionRule>,
    capture: &mut Option<DmnCapture>,
    diagnostics: &mut Vec<CompileDiagnostic>,
    locations: &SourceLocations,
) {
    if !is_dmn_namespace(namespace) {
        return;
    }
    let local_name = String::from_utf8_lossy(element.local_name().as_ref()).into_owned();
    match local_name.as_str() {
        "decisionTable" => {
            let id = optional_non_empty_attribute(element, decoder, b"id")
                .unwrap_or_else(|| format!("decision-table-{offset}"));
            *current_table = Some(DecisionTable {
                id,
                hit_policy: parse_hit_policy(
                    optional_attribute(element, decoder, b"hitPolicy").as_deref(),
                )
                .into(),
                inputs: Vec::new(),
                outputs: Vec::new(),
                rules: Vec::new(),
            });
        }
        "input" => push_dmn_input(
            element,
            decoder,
            current_table,
            offset,
            diagnostics,
            locations,
        ),
        "output" => push_dmn_output(
            element,
            decoder,
            current_table,
            offset,
            diagnostics,
            locations,
        ),
        "rule" => {
            *current_rule = Some(DecisionRule {
                id: optional_non_empty_attribute(element, decoder, b"id")
                    .unwrap_or_else(|| format!("rule-{offset}")),
                input_tests: Vec::new(),
                output_values: Vec::new(),
            });
        }
        "inputEntry" => {
            if let Some(value) = optional_attribute(element, decoder, b"text") {
                push_dmn_entry(
                    DmnCapture::InputEntry,
                    value.trim(),
                    current_rule,
                    offset,
                    diagnostics,
                    locations,
                );
            } else {
                *capture = Some(DmnCapture::InputEntry);
            }
        }
        "outputEntry" => {
            if let Some(value) = optional_attribute(element, decoder, b"text") {
                push_dmn_entry(
                    DmnCapture::OutputEntry,
                    value.trim(),
                    current_rule,
                    offset,
                    diagnostics,
                    locations,
                );
            } else {
                *capture = Some(DmnCapture::OutputEntry);
            }
        }
        _ => {}
    }
}

#[allow(clippy::too_many_arguments)]
fn inspect_dmn_empty(
    namespace: &ResolveResult<'_>,
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    current_table: &mut Option<DecisionTable>,
    current_rule: &mut Option<DecisionRule>,
    diagnostics: &mut Vec<CompileDiagnostic>,
    locations: &SourceLocations,
) {
    if !is_dmn_namespace(namespace) {
        return;
    }
    let local_name = String::from_utf8_lossy(element.local_name().as_ref()).into_owned();
    match local_name.as_str() {
        "input" => push_dmn_input(
            element,
            decoder,
            current_table,
            offset,
            diagnostics,
            locations,
        ),
        "output" => push_dmn_output(
            element,
            decoder,
            current_table,
            offset,
            diagnostics,
            locations,
        ),
        "inputEntry" => {
            let value = optional_attribute(element, decoder, b"text").unwrap_or_else(|| "-".into());
            push_dmn_entry(
                DmnCapture::InputEntry,
                value.trim(),
                current_rule,
                offset,
                diagnostics,
                locations,
            );
        }
        "outputEntry" => {
            let value = optional_attribute(element, decoder, b"text").unwrap_or_default();
            push_dmn_entry(
                DmnCapture::OutputEntry,
                value.trim(),
                current_rule,
                offset,
                diagnostics,
                locations,
            );
        }
        _ => {}
    }
}

fn push_dmn_input(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    current_table: &mut Option<DecisionTable>,
    offset: usize,
    diagnostics: &mut Vec<CompileDiagnostic>,
    locations: &SourceLocations,
) {
    let Some(table) = current_table else {
        return;
    };
    match parse_value_type(optional_attribute(element, decoder, b"typeRef").as_deref()) {
        Ok(value_type) => table.inputs.push(DecisionInput {
            name: dmn_named_element(element, decoder, offset, "input"),
            value_type: value_type.into(),
        }),
        Err(detail) => diagnostics.push(dmn_diagnostic(&table.id, detail, offset, locations)),
    }
}

fn push_dmn_output(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    current_table: &mut Option<DecisionTable>,
    offset: usize,
    diagnostics: &mut Vec<CompileDiagnostic>,
    locations: &SourceLocations,
) {
    let Some(table) = current_table else {
        return;
    };
    match parse_value_type(optional_attribute(element, decoder, b"typeRef").as_deref()) {
        Ok(value_type) => table.outputs.push(DecisionOutput {
            name: dmn_named_element(element, decoder, offset, "output"),
            value_type: value_type.into(),
        }),
        Err(detail) => diagnostics.push(dmn_diagnostic(&table.id, detail, offset, locations)),
    }
}

fn push_dmn_entry(
    capture: DmnCapture,
    value: &str,
    current_rule: &mut Option<DecisionRule>,
    offset: usize,
    diagnostics: &mut Vec<CompileDiagnostic>,
    locations: &SourceLocations,
) {
    let Some(rule) = current_rule else {
        return;
    };
    match capture {
        DmnCapture::InputEntry => match parse_unary_test(value) {
            Ok(test) => rule.input_tests.push(test),
            Err(detail) => diagnostics.push(dmn_diagnostic(&rule.id, detail, offset, locations)),
        },
        DmnCapture::OutputEntry => match parse_workflow_literal(value) {
            Ok(value) => rule.output_values.push(value),
            Err(detail) => diagnostics.push(dmn_diagnostic(&rule.id, detail, offset, locations)),
        },
    }
}

fn validate_decision_table(
    table: DecisionTable,
    offset: usize,
    diagnostics: &mut Vec<CompileDiagnostic>,
    locations: &SourceLocations,
) -> Option<DecisionTable> {
    if table.inputs.is_empty() {
        diagnostics.push(dmn_diagnostic(
            &table.id,
            "decision table must declare at least one input".into(),
            offset,
            locations,
        ));
        return None;
    }
    if table.outputs.is_empty() {
        diagnostics.push(dmn_diagnostic(
            &table.id,
            "decision table must declare at least one output".into(),
            offset,
            locations,
        ));
        return None;
    }
    for rule in &table.rules {
        if rule.input_tests.len() != table.inputs.len() {
            diagnostics.push(dmn_diagnostic(
                &table.id,
                format!(
                    "rule {} has {} input tests, expected {}",
                    rule.id,
                    rule.input_tests.len(),
                    table.inputs.len()
                ),
                offset,
                locations,
            ));
            return None;
        }
        if rule.output_values.len() != table.outputs.len() {
            diagnostics.push(dmn_diagnostic(
                &table.id,
                format!(
                    "rule {} has {} output values, expected {}",
                    rule.id,
                    rule.output_values.len(),
                    table.outputs.len()
                ),
                offset,
                locations,
            ));
            return None;
        }
    }
    Some(table)
}

fn parse_unary_test(source: &str) -> Result<UnaryTest, String> {
    let source = source.trim();
    if source.is_empty() || source == "-" {
        return Ok(UnaryTest {
            test: Some(unary_test::Test::Any(true)),
        });
    }
    if let Some(interval) = parse_integer_interval(source)? {
        return Ok(UnaryTest {
            test: Some(unary_test::Test::IntegerInterval(interval)),
        });
    }
    Ok(UnaryTest {
        test: Some(unary_test::Test::Equal(parse_workflow_literal(source)?)),
    })
}

fn parse_integer_interval(source: &str) -> Result<Option<IntegerInterval>, String> {
    if let Some(value) = source.strip_prefix(">=") {
        return Ok(Some(IntegerInterval {
            lower_bound: value
                .trim()
                .parse()
                .map_err(|_| "invalid integer lower bound")?,
            upper_bound: 0,
            lower_unbounded: false,
            upper_unbounded: true,
        }));
    }
    if let Some(value) = source.strip_prefix(">") {
        let lower = value
            .trim()
            .parse::<i64>()
            .map_err(|_| "invalid integer lower bound")?
            .checked_add(1)
            .ok_or_else(|| "integer lower bound overflows".to_owned())?;
        return Ok(Some(IntegerInterval {
            lower_bound: lower,
            upper_bound: 0,
            lower_unbounded: false,
            upper_unbounded: true,
        }));
    }
    if let Some(value) = source.strip_prefix("<=") {
        return Ok(Some(IntegerInterval {
            lower_bound: 0,
            upper_bound: value
                .trim()
                .parse()
                .map_err(|_| "invalid integer upper bound")?,
            lower_unbounded: true,
            upper_unbounded: false,
        }));
    }
    if let Some(value) = source.strip_prefix("<") {
        let upper = value
            .trim()
            .parse::<i64>()
            .map_err(|_| "invalid integer upper bound")?
            .checked_sub(1)
            .ok_or_else(|| "integer upper bound overflows".to_owned())?;
        return Ok(Some(IntegerInterval {
            lower_bound: 0,
            upper_bound: upper,
            lower_unbounded: true,
            upper_unbounded: false,
        }));
    }
    let bracketed = source
        .strip_prefix('[')
        .and_then(|source| source.strip_suffix(']'));
    let Some(range) = bracketed else {
        return Ok(None);
    };
    let Some((lower, upper)) = range.split_once("..") else {
        return Err("integer interval must use [lower..upper]".into());
    };
    Ok(Some(IntegerInterval {
        lower_bound: lower
            .trim()
            .parse()
            .map_err(|_| "invalid integer lower bound")?,
        upper_bound: upper
            .trim()
            .parse()
            .map_err(|_| "invalid integer upper bound")?,
        lower_unbounded: false,
        upper_unbounded: false,
    }))
}

fn parse_workflow_literal(source: &str) -> Result<WorkflowLiteral, String> {
    let value = match parse_guard_literal(source)? {
        guard_expression::Literal::BooleanValue(value) => {
            workflow_literal::Value::BooleanValue(value)
        }
        guard_expression::Literal::IntegerValue(value) => {
            workflow_literal::Value::IntegerValue(value)
        }
        guard_expression::Literal::StringValue(value) => {
            workflow_literal::Value::StringValue(value)
        }
    };
    Ok(WorkflowLiteral { value: Some(value) })
}

fn parse_hit_policy(value: Option<&str>) -> HitPolicy {
    match value.unwrap_or("UNIQUE") {
        "UNIQUE" | "U" => HitPolicy::Unique,
        "FIRST" | "F" => HitPolicy::First,
        "COLLECT" | "C" => HitPolicy::Collect,
        _ => HitPolicy::Unspecified,
    }
}

fn parse_value_type(value: Option<&str>) -> Result<ValueType, String> {
    match value.unwrap_or_default() {
        "boolean" | "bool" => Ok(ValueType::Boolean),
        "integer" | "int" | "long" => Ok(ValueType::Integer),
        "string" => Ok(ValueType::String),
        _ => Err("typeRef must be boolean, integer, or string".into()),
    }
}

fn dmn_named_element(
    element: &BytesStart<'_>,
    decoder: quick_xml::encoding::Decoder,
    offset: usize,
    fallback: &str,
) -> String {
    optional_non_empty_attribute(element, decoder, b"name")
        .or_else(|| optional_non_empty_attribute(element, decoder, b"label"))
        .or_else(|| optional_non_empty_attribute(element, decoder, b"id"))
        .unwrap_or_else(|| format!("{fallback}-{offset}"))
}

fn dmn_diagnostic(
    table_id: &str,
    detail: String,
    offset: usize,
    locations: &SourceLocations,
) -> CompileDiagnostic {
    locations.diagnostic(
        offset,
        DiagnosticKind::InvalidDecisionTable {
            table_id: table_id.to_owned(),
            detail,
        },
    )
}

fn is_dmn_namespace(namespace: &ResolveResult<'_>) -> bool {
    matches!(
        namespace,
        ResolveResult::Bound(namespace)
            if DMN_MODEL_NAMESPACES
                .iter()
                .any(|candidate| namespace.as_ref() == *candidate)
    )
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

    const TENANT_ID: &str = "tenant-a";

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
                TENANT_ID,
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
                TENANT_ID,
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
                TENANT_ID,
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
                TENANT_ID,
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
    fn exclusive_gateway_with_default_flow_round_trips() {
        let gateway = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="routing">
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="route" default="fallback" />
    <bpmn:serviceTask id="approved" name="approve" />
    <bpmn:endEvent id="rejected" />
    <bpmn:endEvent id="done" />
    <bpmn:sequenceFlow id="to-route" sourceRef="start" targetRef="route" />
    <bpmn:sequenceFlow id="accepted" sourceRef="route" targetRef="approved" condition="approved == true" />
    <bpmn:sequenceFlow id="fallback" sourceRef="route" targetRef="rejected" />
    <bpmn:sequenceFlow id="finish" sourceRef="approved" targetRef="done" />
  </bpmn:process>
</bpmn:definitions>"#;
        let compiler = compiler();
        let first = compiler
            .compile(
                SourceDocument {
                    name: "gateway.bpmn",
                    bytes: gateway.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        let canonical = compiler.print(&first).unwrap();
        let second = compiler
            .compile(
                SourceDocument {
                    name: "canonical.bpmn",
                    bytes: canonical.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        assert_eq!(first, second);
    }

    #[test]
    fn exhaustive_boolean_gateway_without_default_round_trips() {
        let gateway = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="boolean-routing">
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="route" />
    <bpmn:endEvent id="approved" />
    <bpmn:endEvent id="rejected" />
    <bpmn:sequenceFlow id="to-route" sourceRef="start" targetRef="route" />
    <bpmn:sequenceFlow id="yes" sourceRef="route" targetRef="approved" condition="approved == true" />
    <bpmn:sequenceFlow id="no" sourceRef="route" targetRef="rejected" condition="approved == false" />
  </bpmn:process>
</bpmn:definitions>"#;
        let compiler = compiler();
        let first = compiler
            .compile(
                SourceDocument {
                    name: "boolean-gateway.bpmn",
                    bytes: gateway.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        let canonical = compiler.print(&first).unwrap();
        let second = compiler
            .compile(
                SourceDocument {
                    name: "canonical.bpmn",
                    bytes: canonical.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        assert_eq!(first, second);
    }

    #[test]
    fn exhaustive_enum_gateway_uses_declared_domain() {
        let gateway = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="enum-routing">
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="route" enumValues="approved,rejected" />
    <bpmn:endEvent id="approved" />
    <bpmn:endEvent id="rejected" />
    <bpmn:sequenceFlow id="to-route" sourceRef="start" targetRef="route" />
    <bpmn:sequenceFlow id="yes" sourceRef="route" targetRef="approved" condition="status == &quot;approved&quot;" />
    <bpmn:sequenceFlow id="no" sourceRef="route" targetRef="rejected" condition="status == &quot;rejected&quot;" />
  </bpmn:process>
</bpmn:definitions>"#;
        let compiler = compiler();
        let first = compiler
            .compile(
                SourceDocument {
                    name: "enum-gateway.bpmn",
                    bytes: gateway.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        let canonical = compiler.print(&first).unwrap();
        let second = compiler
            .compile(
                SourceDocument {
                    name: "canonical.bpmn",
                    bytes: canonical.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        assert_eq!(first, second);
    }

    #[test]
    fn exhaustive_numeric_interval_gateway_without_default_compiles() {
        let gateway = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="amount-routing">
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="route" />
    <bpmn:endEvent id="low" />
    <bpmn:endEvent id="high" />
    <bpmn:sequenceFlow id="to-route" sourceRef="start" targetRef="route" />
    <bpmn:sequenceFlow id="low-flow" sourceRef="route" targetRef="low" condition="amount &lt; 100" />
    <bpmn:sequenceFlow id="high-flow" sourceRef="route" targetRef="high" condition="amount &gt;= 100" />
  </bpmn:process>
</bpmn:definitions>"#;
        let wir = compiler()
            .compile(
                SourceDocument {
                    name: "numeric-gateway.bpmn",
                    bytes: gateway.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap();
        let gateway = wir
            .nodes
            .iter()
            .find(|node| node.id == "route")
            .and_then(|node| node.kind.as_ref())
            .unwrap();
        assert!(matches!(
            gateway,
            node::Kind::ExclusiveGateway(gateway) if gateway.coverage.is_some()
        ));
    }

    #[test]
    fn rejects_overlapping_static_gateway_coverage() {
        let invalid = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="amount-routing">
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="route" />
    <bpmn:endEvent id="low" />
    <bpmn:endEvent id="high" />
    <bpmn:sequenceFlow id="to-route" sourceRef="start" targetRef="route" />
    <bpmn:sequenceFlow id="low-flow" sourceRef="route" targetRef="low" condition="amount &lt;= 100" />
    <bpmn:sequenceFlow id="high-flow" sourceRef="route" targetRef="high" condition="amount &gt;= 100" />
  </bpmn:process>
</bpmn:definitions>"#;
        let diagnostics = compiler()
            .compile(
                SourceDocument {
                    name: "ambiguous-gateway.bpmn",
                    bytes: invalid.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap_err();
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            diagnostic.kind,
            DiagnosticKind::AmbiguousGatewayCoverage { .. }
        )));
    }

    #[test]
    fn compiles_dmn_decision_table_into_typed_wir() {
        let dmn = r#"<dmn:definitions xmlns:dmn="https://www.omg.org/spec/DMN/20191111/MODEL/">
  <dmn:decisionTable id="risk" hitPolicy="FIRST">
    <dmn:input id="amount" label="amount" typeRef="integer" />
    <dmn:output id="tier" name="tier" typeRef="string" />
    <dmn:rule id="low">
      <dmn:inputEntry text="&lt; 100" />
      <dmn:outputEntry text="&quot;low&quot;" />
    </dmn:rule>
  </dmn:decisionTable>
</dmn:definitions>"#;
        let wir = compiler()
            .compile_with_decisions(
                SourceDocument {
                    name: "order.bpmn",
                    bytes: VALID.as_bytes(),
                },
                &[SourceDocument {
                    name: "risk.dmn",
                    bytes: dmn.as_bytes(),
                }],
                TENANT_ID,
                "1",
            )
            .unwrap();
        assert_eq!(wir.decision_tables.len(), 1);
        assert_eq!(wir.decision_tables[0].id, "risk");
        assert_eq!(wir.decision_tables[0].rules.len(), 1);
    }

    #[test]
    fn business_rule_task_round_trips_with_decision_ref() {
        let bpmn = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="risk-routing">
    <bpmn:startEvent id="start" />
    <bpmn:businessRuleTask id="risk" decisionRef="risk-table" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="risk" />
    <bpmn:sequenceFlow id="f2" sourceRef="risk" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;
        let dmn = r#"<dmn:definitions xmlns:dmn="https://www.omg.org/spec/DMN/20191111/MODEL/">
  <dmn:decisionTable id="risk-table" hitPolicy="FIRST">
    <dmn:input id="amount" label="amount" typeRef="integer" />
    <dmn:output id="approved" name="approved" typeRef="boolean" />
    <dmn:rule id="high">
      <dmn:inputEntry text="&gt;= 100" />
      <dmn:outputEntry text="true" />
    </dmn:rule>
  </dmn:decisionTable>
</dmn:definitions>"#;
        let compiler = compiler();
        let first = compiler
            .compile_with_decisions(
                SourceDocument {
                    name: "risk.bpmn",
                    bytes: bpmn.as_bytes(),
                },
                &[SourceDocument {
                    name: "risk.dmn",
                    bytes: dmn.as_bytes(),
                }],
                TENANT_ID,
                "1",
            )
            .unwrap();
        let canonical = compiler.print(&first).unwrap();
        let second = compiler
            .compile_with_decisions(
                SourceDocument {
                    name: "canonical.bpmn",
                    bytes: canonical.as_bytes(),
                },
                &[SourceDocument {
                    name: "risk.dmn",
                    bytes: dmn.as_bytes(),
                }],
                TENANT_ID,
                "1",
            )
            .unwrap();
        assert_eq!(first, second);
    }

    #[test]
    fn reports_semantic_contract_violations_with_locations() {
        let invalid = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="invalid" slaMilliseconds="100">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="source" outputType="Invoice" slaMilliseconds="200" requiresCompensation="true" />
    <bpmn:serviceTask id="target" inputType="Payment" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="source" />
    <bpmn:sequenceFlow id="f2" sourceRef="source" targetRef="target" />
    <bpmn:sequenceFlow id="f3" sourceRef="target" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;
        let diagnostics = compiler()
            .compile(
                SourceDocument {
                    name: "semantic-errors.bpmn",
                    bytes: invalid.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap_err();
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            diagnostic.kind,
            DiagnosticKind::MissingCompensation { .. }
        )));
        assert!(
            diagnostics
                .iter()
                .any(|diagnostic| matches!(diagnostic.kind, DiagnosticKind::SlaConflict { .. }))
        );
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            diagnostic.kind,
            DiagnosticKind::DataContractMismatch { .. }
        )));
        assert!(
            diagnostics
                .iter()
                .all(|diagnostic| { diagnostic.span.line > 0 && diagnostic.span.column > 0 })
        );
    }

    #[test]
    fn rejects_non_exhaustive_gateway() {
        let invalid = VALID.replace(
            "<bpmn:serviceTask id=\"charge\" name=\"payment\" />",
            "<bpmn:exclusiveGateway id=\"charge\" />",
        );
        let diagnostics = compiler()
            .compile(
                SourceDocument {
                    name: "gateway.bpmn",
                    bytes: invalid.as_bytes(),
                },
                TENANT_ID,
                "1",
            )
            .unwrap_err();
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            diagnostic.kind,
            DiagnosticKind::NonExhaustiveGateway { .. }
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
                TENANT_ID,
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
                TENANT_ID,
                "property-v1",
            ).expect("generated BPMN must compile");
            let canonical = compiler.print(&first).expect("WIR must print");
            let second = compiler.compile(
                SourceDocument { name: "canonical.bpmn", bytes: canonical.as_bytes() },
                TENANT_ID,
                "property-v1",
            ).expect("canonical BPMN must compile");
            prop_assert_eq!(first, second);
        }
    }
}
