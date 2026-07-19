use bpmn_compiler::{BpmnCompiler, CompilerLimits, DiagnosticKind, SourceDocument};

const BPMN_NS: &str = "http://www.omg.org/spec/BPMN/20100524/MODEL";

fn compiler() -> BpmnCompiler {
    BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap())
}

fn compile_with(candidate: &str) -> Vec<bpmn_compiler::CompileDiagnostic> {
    let source = format!(
        "<b:definitions xmlns:b=\"{BPMN_NS}\">\n  <b:process id=\"unsupported\">\n    <b:startEvent id=\"start\"/>\n    {candidate}\n    <b:endEvent id=\"end\"/>\n    <b:sequenceFlow id=\"f1\" sourceRef=\"start\" targetRef=\"end\"/>\n  </b:process>\n</b:definitions>"
    );
    compiler()
        .compile(
            SourceDocument {
                name: "unsupported.bpmn",
                bytes: source.as_bytes(),
            },
            "tenant-a",
            "1",
        )
        .unwrap_err()
}

#[test]
fn every_known_unsupported_executable_element_fails_closed_with_a_span() {
    let cases = [
        ("task", "<b:task id=\"candidate\"/>"),
        ("sendTask", "<b:sendTask id=\"candidate\"/>"),
        ("receiveTask", "<b:receiveTask id=\"candidate\"/>"),
        ("manualTask", "<b:manualTask id=\"candidate\"/>"),
        ("complexGateway", "<b:complexGateway id=\"candidate\"/>"),
        (
            "eventBasedGateway",
            "<b:eventBasedGateway id=\"candidate\"/>",
        ),
        (
            "intermediateCatchEvent",
            "<b:intermediateCatchEvent id=\"candidate\"/>",
        ),
        (
            "intermediateThrowEvent",
            "<b:intermediateThrowEvent id=\"candidate\"/>",
        ),
        ("transaction", "<b:transaction id=\"candidate\"/>"),
        ("adHocSubProcess", "<b:adHocSubProcess id=\"candidate\"/>"),
        (
            "standardLoopCharacteristics",
            "<b:standardLoopCharacteristics/>",
        ),
        ("signalEventDefinition", "<b:signalEventDefinition/>"),
        (
            "escalationEventDefinition",
            "<b:escalationEventDefinition/>",
        ),
        ("cancelEventDefinition", "<b:cancelEventDefinition/>"),
        (
            "conditionalEventDefinition",
            "<b:conditionalEventDefinition/>",
        ),
        ("linkEventDefinition", "<b:linkEventDefinition/>"),
        ("terminateEventDefinition", "<b:terminateEventDefinition/>"),
        ("choreographyTask", "<b:choreographyTask id=\"candidate\"/>"),
        ("callChoreography", "<b:callChoreography id=\"candidate\"/>"),
        ("subChoreography", "<b:subChoreography id=\"candidate\"/>"),
        ("conversation", "<b:conversation id=\"candidate\"/>"),
        ("conversationNode", "<b:conversationNode id=\"candidate\"/>"),
    ];

    for (expected, candidate) in cases {
        let diagnostics = compile_with(candidate);
        let diagnostic = diagnostics
            .iter()
            .find(|diagnostic| {
                matches!(
                    &diagnostic.kind,
                    DiagnosticKind::UnsupportedElement { element } if element == expected
                )
            })
            .unwrap_or_else(|| panic!("missing unsupported diagnostic for {expected}"));
        assert_eq!(diagnostic.span.file, "unsupported.bpmn");
        assert_eq!(diagnostic.span.line, 4);
        assert!(diagnostic.span.column > 0);
    }
}

#[test]
fn event_subprocess_is_not_lowered_as_a_normal_retained_scope() {
    let diagnostics = compile_with(
        "<b:subProcess id=\"candidate\" triggeredByEvent=\"true\"><b:startEvent id=\"event-start\"/><b:endEvent id=\"event-end\"/></b:subProcess>",
    );
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(
            &diagnostic.kind,
            DiagnosticKind::UnsupportedElement { element } if element == "eventSubProcess"
        )
    }));
}

#[test]
fn invalid_event_subprocess_boolean_fails_closed() {
    let diagnostics = compile_with(
        "<b:subProcess id=\"candidate\" triggeredByEvent=\"yes\"><b:startEvent id=\"event-start\"/><b:endEvent id=\"event-end\"/></b:subProcess>",
    );
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(
            &diagnostic.kind,
            DiagnosticKind::InvalidSubProcess { subprocess_id, detail }
                if subprocess_id == "candidate" && detail.contains("XML boolean")
        )
    }));
}

#[test]
fn unknown_bpmn_namespace_element_fails_closed() {
    let diagnostics = compile_with("<b:futureEnterpriseTask id=\"candidate\"/>");
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(
            &diagnostic.kind,
            DiagnosticKind::UnsupportedElement { element }
                if element == "futureEnterpriseTask"
        )
    }));
}

#[test]
fn unsupported_semantic_element_outside_bpmn_namespace_is_rejected() {
    let source = format!(
        "<b:definitions xmlns:b=\"{BPMN_NS}\" xmlns:x=\"urn:not-bpmn\"><b:process id=\"wrong-ns\"><b:startEvent id=\"start\"/><x:eventBasedGateway id=\"candidate\"/><b:endEvent id=\"end\"/><b:sequenceFlow id=\"f1\" sourceRef=\"start\" targetRef=\"end\"/></b:process></b:definitions>"
    );
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "wrong-namespace.bpmn",
                bytes: source.as_bytes(),
            },
            "tenant-a",
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| matches!(
        &diagnostic.kind,
        DiagnosticKind::ElementOutsideBpmnNamespace { element }
            if element == "eventBasedGateway"
    )));
}
