use std::fmt::{self, Display};

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct SourceSpan {
    pub file: String,
    pub byte_offset: usize,
    pub line: u32,
    pub column: u32,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct CompileDiagnostic {
    pub kind: DiagnosticKind,
    pub span: SourceSpan,
}

impl Display for CompileDiagnostic {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{}:{}:{}: error: {}",
            self.span.file, self.span.line, self.span.column, self.kind
        )
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub enum DiagnosticKind {
    InputTooLarge {
        actual: usize,
        configured_limit: usize,
    },
    Xml {
        detail: String,
    },
    ForbiddenDocumentType,
    XmlDepthExceeded {
        actual: u32,
        configured_limit: u32,
    },
    ElementOutsideBpmnNamespace {
        element: String,
    },
    MissingAttribute {
        element: String,
        attribute: &'static str,
    },
    DuplicateId {
        id: String,
    },
    UnsupportedElement {
        element: String,
    },
    MissingProcess,
    MultipleProcesses,
    MissingStartEvent,
    MultipleStartEvents,
    MissingEndEvent,
    UnresolvedReference {
        flow_id: String,
        missing_id: String,
    },
    InvalidOutgoingCount {
        node_id: String,
        actual: usize,
    },
    InvalidIncomingCount {
        node_id: String,
        actual: usize,
    },
    UnreachablePath {
        element_id: String,
    },
    DeadPath {
        element_id: String,
    },
}

impl Display for DiagnosticKind {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InputTooLarge {
                actual,
                configured_limit,
            } => write!(
                formatter,
                "input is {actual} bytes, above configured limit {configured_limit}"
            ),
            Self::Xml { detail } => write!(formatter, "invalid XML: {detail}"),
            Self::ForbiddenDocumentType => {
                formatter.write_str("DOCTYPE and entity declarations are forbidden")
            }
            Self::XmlDepthExceeded {
                actual,
                configured_limit,
            } => write!(
                formatter,
                "XML depth {actual} exceeds configured limit {configured_limit}"
            ),
            Self::ElementOutsideBpmnNamespace { element } => {
                write!(
                    formatter,
                    "semantic element {element} is outside the BPMN namespace"
                )
            }
            Self::MissingAttribute { element, attribute } => {
                write!(
                    formatter,
                    "{element} is missing required attribute {attribute}"
                )
            }
            Self::DuplicateId { id } => write!(formatter, "duplicate BPMN id {id}"),
            Self::UnsupportedElement { element } => write!(
                formatter,
                "BPMN element {element} is not supported by the current compiler phase"
            ),
            Self::MissingProcess => formatter.write_str("no executable BPMN process was found"),
            Self::MultipleProcesses => {
                formatter.write_str("one source document must contain exactly one BPMN process")
            }
            Self::MissingStartEvent => formatter.write_str("process has no startEvent"),
            Self::MultipleStartEvents => {
                formatter.write_str("linear MVP process must contain exactly one startEvent")
            }
            Self::MissingEndEvent => formatter.write_str("process has no endEvent"),
            Self::UnresolvedReference {
                flow_id,
                missing_id,
            } => write!(
                formatter,
                "sequenceFlow {flow_id} references missing node {missing_id}"
            ),
            Self::InvalidOutgoingCount { node_id, actual } => write!(
                formatter,
                "node {node_id} must have exactly one outgoing flow, found {actual}"
            ),
            Self::InvalidIncomingCount { node_id, actual } => write!(
                formatter,
                "node {node_id} must have exactly one incoming flow, found {actual}"
            ),
            Self::UnreachablePath { element_id } => {
                write!(
                    formatter,
                    "node {element_id} is unreachable from the start event"
                )
            }
            Self::DeadPath { element_id } => {
                write!(formatter, "node {element_id} cannot reach an end event")
            }
        }
    }
}
