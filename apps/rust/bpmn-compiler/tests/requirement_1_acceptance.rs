use std::fs;
use std::process::Command;

use bpmn_compiler::{BpmnCompiler, CompilerLimits, DiagnosticKind, SourceDocument};
use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};
use bpmp_engine::WirLoader;
use proptest::prelude::*;

const TENANT: &str = "tenant-a";
const BPMN_NS: &str = "http://www.omg.org/spec/BPMN/20100524/MODEL";

fn compiler() -> BpmnCompiler {
    BpmnCompiler::new(CompilerLimits::new(128 * 1024, 64).unwrap())
}

fn linear(process_id: &str) -> String {
    format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI">
  <b:process id="{process_id}">
    <b:startEvent id="start" />
    <b:serviceTask id="task" name="execute" />
    <b:endEvent id="end" />
    <b:sequenceFlow id="f1" sourceRef="start" targetRef="task" />
    <b:sequenceFlow id="f2" sourceRef="task" targetRef="end" />
    <bpmndi:BPMNDiagram id="diagram" />
  </b:process>
</b:definitions>"#
    )
}

#[test]
fn ac1_namespace_aware_bpmn_compiles_to_wir() {
    let source = linear("ac1");
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "ac1.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    assert_eq!(wir.workflow_type, "ac1");
    assert_eq!(wir.nodes.len(), 3);
    assert_eq!(wir.tenant_id, TENANT);
}

#[test]
fn ac2_dmn_and_cmmn_are_integrated_in_one_wir() {
    let bpmn = r#"<b:definitions xmlns:b="http://www.omg.org/spec/BPMN/20100524/MODEL"><b:process id="ac2"><b:startEvent id="start"/><b:businessRuleTask id="risk" decisionRef="risk-table"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="risk"/><b:sequenceFlow id="f2" sourceRef="risk" targetRef="end"/></b:process></b:definitions>"#;
    let dmn = r#"<d:definitions xmlns:d="https://www.omg.org/spec/DMN/20191111/MODEL/"><d:decisionTable id="risk-table" hitPolicy="FIRST"><d:input id="amount" label="amount" typeRef="integer"/><d:output id="approved" name="approved" typeRef="boolean"/><d:rule id="r1"><d:inputEntry text="-"/><d:outputEntry text="true"/></d:rule></d:decisionTable></d:definitions>"#;
    let cmmn = r#"<c:definitions xmlns:c="https://www.omg.org/spec/CMMN/20151109/MODEL"><c:case id="case-1"><c:casePlanModel id="plan"><c:sentry id="ready" condition="ready == true"/><c:stage id="review" entrySentryRefs="ready"/><c:milestone id="done" entrySentryRefs="ready"/></c:casePlanModel></c:case></c:definitions>"#;
    let wir = compiler()
        .compile_with_models(
            SourceDocument {
                name: "ac2.bpmn",
                bytes: bpmn.as_bytes(),
            },
            &[SourceDocument {
                name: "ac2.dmn",
                bytes: dmn.as_bytes(),
            }],
            &[SourceDocument {
                name: "ac2.cmmn",
                bytes: cmmn.as_bytes(),
            }],
            TENANT,
            "1",
        )
        .unwrap();
    assert_eq!(wir.decision_tables.len(), 1);
    assert_eq!(wir.case_models.len(), 1);
}

#[test]
fn ac3_generated_rust_has_no_runtime_xml_dependency_and_compiles() {
    let source = linear("ac3");
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "ac3.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    let generated = compiler.generate_rust(&wir).unwrap();
    assert!(!generated.contains("quick_xml"));
    assert!(!generated.contains("<b:"));
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("generated.rs");
    fs::write(&path, generated).unwrap();
    assert!(
        Command::new("rustc")
            .args(["--edition=2024", "--crate-type=lib"])
            .arg(path)
            .arg("-o")
            .arg(directory.path().join("generated.rlib"))
            .status()
            .unwrap()
            .success()
    );
}

#[test]
fn ac4_non_exhaustive_gateway_reports_source_location() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="ac4"><b:startEvent id="start"/><b:exclusiveGateway id="route"/><b:endEvent id="yes"/><b:endEvent id="no"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="route"/><b:sequenceFlow id="f2" sourceRef="route" targetRef="yes" condition="approved == true"/><b:sequenceFlow id="f3" sourceRef="route" targetRef="no" condition="approved == true"/></b:process></b:definitions>"#
    );
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "ac4.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(
            diagnostic.kind,
            DiagnosticKind::NonExhaustiveGateway { .. }
                | DiagnosticKind::AmbiguousGatewayCoverage { .. }
        ) && diagnostic.span.line > 0
            && diagnostic.span.column > 0
    }));
}

#[test]
fn ac5_dead_and_unreachable_paths_report_locations() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="ac5"><b:startEvent id="start"/><b:endEvent id="end"/><b:serviceTask id="orphan"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="end"/><b:sequenceFlow id="f2" sourceRef="orphan" targetRef="orphan"/></b:process></b:definitions>"#
    );
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "ac5.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(diagnostic.kind, DiagnosticKind::UnreachablePath { .. })
            && diagnostic.span.line > 0
    }));
    assert!(
        diagnostics
            .iter()
            .any(|diagnostic| matches!(diagnostic.kind, DiagnosticKind::DeadPath { .. }))
    );
}

#[test]
fn ac6_standard_compensation_boundary_resolves_handler_and_missing_handler_fails() {
    let valid = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="ac6"><b:startEvent id="start"/><b:serviceTask id="charge"/><b:boundaryEvent id="undo-trigger" attachedToRef="charge"><b:compensateEventDefinition/></b:boundaryEvent><b:serviceTask id="undo-charge" isForCompensation="true"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="charge"/><b:sequenceFlow id="f2" sourceRef="charge" targetRef="end"/><b:association id="a1" sourceRef="undo-trigger" targetRef="undo-charge"/></b:process></b:definitions>"#
    );
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "compensation.bpmn",
                bytes: valid.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    let charge = wir.nodes.iter().find(|node| node.id == "charge").unwrap();
    assert_eq!(charge.compensation_handler_id, "undo-charge");
    assert!(!wir.nodes.iter().any(|node| node.id == "undo-charge"));
    let canonical = compiler.print(&wir).unwrap();
    let round_tripped = compiler
        .compile(
            SourceDocument {
                name: "canonical-compensation.bpmn",
                bytes: canonical.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    assert_eq!(wir, round_tripped);

    let invalid = valid.replace(
        "<b:association id=\"a1\" sourceRef=\"undo-trigger\" targetRef=\"undo-charge\"/>",
        "",
    );
    let diagnostics = compiler
        .compile(
            SourceDocument {
                name: "missing-compensation.bpmn",
                bytes: invalid.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(diagnostic.kind, DiagnosticKind::MissingCompensation { .. })
            && diagnostic.span.line > 0
    }));
}

#[test]
fn ac7_cumulative_path_sla_conflict_reports_path() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="ac7" slaMilliseconds="100"><b:startEvent id="start"/><b:serviceTask id="a" slaMilliseconds="60"/><b:serviceTask id="b" slaMilliseconds="60"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="a"/><b:sequenceFlow id="f2" sourceRef="a" targetRef="b"/><b:sequenceFlow id="f3" sourceRef="b" targetRef="end"/></b:process></b:definitions>"#
    );
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "sla.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(&diagnostic.kind, DiagnosticKind::SlaConflict { detail } if detail.contains("a -> b") && detail.contains("120ms"))
    }));
}

#[test]
fn ac8_data_contract_types_propagate_through_gateway() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="ac8"><b:startEvent id="start"/><b:serviceTask id="producer" outputType="Invoice"/><b:exclusiveGateway id="route" default="fallback"/><b:serviceTask id="consumer" inputType="Payment"/><b:endEvent id="fallback-end"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="producer"/><b:sequenceFlow id="f2" sourceRef="producer" targetRef="route"/><b:sequenceFlow id="typed" sourceRef="route" targetRef="consumer" condition="approved == true"/><b:sequenceFlow id="fallback" sourceRef="route" targetRef="fallback-end"/><b:sequenceFlow id="f3" sourceRef="consumer" targetRef="end"/></b:process></b:definitions>"#
    );
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "data-flow.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(&diagnostic.kind, DiagnosticKind::DataContractMismatch { from, to, expected, actual }
            if from == "producer" && to == "consumer" && expected == "Payment" && actual == "Invoice")
    }));
}

#[test]
fn ac8_dmn_output_type_is_inferred_and_checked_downstream() {
    let bpmn = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="ac8-dmn"><b:startEvent id="start"/><b:serviceTask id="producer" outputType="integer"/><b:businessRuleTask id="decision" decisionRef="risk"/><b:serviceTask id="consumer" inputType="string"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="producer"/><b:sequenceFlow id="f2" sourceRef="producer" targetRef="decision"/><b:sequenceFlow id="f3" sourceRef="decision" targetRef="consumer"/><b:sequenceFlow id="f4" sourceRef="consumer" targetRef="end"/></b:process></b:definitions>"#
    );
    let dmn = r#"<d:definitions xmlns:d="https://www.omg.org/spec/DMN/20191111/MODEL/"><d:decisionTable id="risk" hitPolicy="FIRST"><d:input id="amount" label="amount" typeRef="integer"/><d:output id="approved" name="approved" typeRef="boolean"/><d:rule id="r1"><d:inputEntry text="-"/><d:outputEntry text="true"/></d:rule></d:decisionTable></d:definitions>"#;
    let diagnostics = compiler()
        .compile_with_decisions(
            SourceDocument {
                name: "data-flow-dmn.bpmn",
                bytes: bpmn.as_bytes(),
            },
            &[SourceDocument {
                name: "risk.dmn",
                bytes: dmn.as_bytes(),
            }],
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(&diagnostic.kind, DiagnosticKind::DataContractMismatch { from, to, expected, actual }
            if from == "decision" && to == "consumer" && expected == "string" && actual == "boolean")
    }), "{diagnostics:#?}");
}

#[test]
fn ac9_cli_returns_nonzero_and_does_not_publish_invalid_artifact() {
    let directory = tempfile::tempdir().unwrap();
    let input = directory.path().join("invalid.bpmn");
    let output = directory.path().join("invalid.wir");
    let key = directory.path().join("key.bin");
    fs::write(
        &input,
        linear("ac9").replace("targetRef=\"task\"", "targetRef=\"missing\""),
    )
    .unwrap();
    fs::write(&key, [9; 32]).unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_bpmn-compiler"))
        .args([
            "--input",
            input.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
            "--tenant-id",
            TENANT,
            "--workflow-version",
            "1",
            "--signing-key",
            key.to_str().unwrap(),
            "--max-input-bytes",
            "131072",
            "--max-xml-depth",
            "64",
        ])
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(1));
    assert!(!output.exists());
    assert!(String::from_utf8_lossy(&result.stderr).contains("invalid.bpmn:"));
}

#[test]
fn ac10_serialized_artifact_loads_directly_in_engine() {
    let source = linear("ac10");
    let wir = compiler()
        .compile(
            SourceDocument {
                name: "ac10.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    let signer = Ed25519Signer::from_bytes(&[10; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();
    let definition = WirLoader::load(&artifact, &verifier).unwrap();
    assert_eq!(definition.workflow_type.as_str(), "ac10");
}

proptest! {
    #![proptest_config(ProptestConfig {
        cases: 100,
        failure_persistence: None,
        ..ProptestConfig::default()
    })]

    // Feature: rust-bpm-platform, Property 1: BPMN compiler canonical round trip
    #[test]
    fn ac11_compile_print_compile_is_equivalent_for_valid_models(
        suffix in "[a-z][a-z0-9]{0,12}"
    ) {
        let source = linear(&format!("ac11-{suffix}"));
        let compiler = compiler();
        let first = compiler.compile(
            SourceDocument { name: "generated.bpmn", bytes: source.as_bytes() }, TENANT, "1"
        ).unwrap();
        let canonical = compiler.print(&first).unwrap();
        let second = compiler.compile(
            SourceDocument { name: "canonical.bpmn", bytes: canonical.as_bytes() }, TENANT, "1"
        ).unwrap();
        prop_assert_eq!(first, second);
    }

    #[test]
    fn parser_never_panics_for_bounded_untrusted_input(bytes in proptest::collection::vec(any::<u8>(), 0..4096)) {
        let _ = compiler().compile(
            SourceDocument { name: "untrusted.bpmn", bytes: &bytes }, TENANT, "1"
        );
    }
}

#[test]
fn ac12_versioned_signed_contract_rejects_tampering() {
    let source = linear("ac12");
    let wir = compiler()
        .compile(
            SourceDocument {
                name: "ac12.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    assert!(wir.schema_version > 0);
    let signer = Ed25519Signer::from_bytes(&[12; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let mut artifact = WirCodec::seal(wir, &signer).unwrap();
    let last = artifact.len() - 1;
    artifact[last] ^= 1;
    assert!(WirCodec::open(&artifact, &verifier).is_err());
}
