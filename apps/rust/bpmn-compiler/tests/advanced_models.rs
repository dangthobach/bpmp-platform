use std::fs;
use std::process::Command;

use bpmn_compiler::{BpmnCompiler, CompilerLimits, DiagnosticKind, SourceDocument};
use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};
use bpmp_engine::WirLoader;

const PARALLEL_BPMN: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="parallel-order">
    <bpmn:startEvent id="start" />
    <bpmn:parallelGateway id="fork" />
    <bpmn:serviceTask id="charge" name="charge" />
    <bpmn:serviceTask id="reserve" name="reserve" />
    <bpmn:parallelGateway id="join" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="fork" />
    <bpmn:sequenceFlow id="f2" sourceRef="fork" targetRef="charge" />
    <bpmn:sequenceFlow id="f3" sourceRef="fork" targetRef="reserve" />
    <bpmn:sequenceFlow id="f4" sourceRef="charge" targetRef="join" />
    <bpmn:sequenceFlow id="f5" sourceRef="reserve" targetRef="join" />
    <bpmn:sequenceFlow id="f6" sourceRef="join" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;

const INCLUSIVE_BPMN: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="inclusive-order">
    <bpmn:startEvent id="start" />
    <bpmn:inclusiveGateway id="fork" default="fallback-flow" />
    <bpmn:serviceTask id="charge" name="charge" />
    <bpmn:serviceTask id="reserve" name="reserve" />
    <bpmn:serviceTask id="fallback" name="fallback" />
    <bpmn:inclusiveGateway id="join" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="fork" />
    <bpmn:sequenceFlow id="f2" sourceRef="fork" targetRef="charge" condition="charge == true" />
    <bpmn:sequenceFlow id="f3" sourceRef="fork" targetRef="reserve" condition="reserve == true" />
    <bpmn:sequenceFlow id="fallback-flow" sourceRef="fork" targetRef="fallback" />
    <bpmn:sequenceFlow id="f4" sourceRef="charge" targetRef="join" />
    <bpmn:sequenceFlow id="f5" sourceRef="reserve" targetRef="join" />
    <bpmn:sequenceFlow id="f6" sourceRef="fallback" targetRef="join" />
    <bpmn:sequenceFlow id="f7" sourceRef="join" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;

const CMMN: &str = r#"<cmmn:definitions xmlns:cmmn="https://www.omg.org/spec/CMMN/20151109/MODEL">
  <cmmn:case id="claim-case" name="Claim review">
    <cmmn:casePlanModel id="claim-plan">
      <cmmn:sentry id="documents-ready" condition="documents == true" />
      <cmmn:stage id="review" name="Review" entrySentryRefs="documents-ready" />
      <cmmn:milestone id="approved" name="Approved" entrySentryRefs="documents-ready" />
    </cmmn:casePlanModel>
  </cmmn:case>
</cmmn:definitions>"#;

const DECISION_BPMN: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="decision-order">
    <bpmn:startEvent id="start" />
    <bpmn:businessRuleTask id="risk" decisionRef="risk-table" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="risk" />
    <bpmn:sequenceFlow id="f2" sourceRef="risk" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;

const DMN: &str = r#"<dmn:definitions xmlns:dmn="https://www.omg.org/spec/DMN/20191111/MODEL/">
  <dmn:decisionTable id="risk-table" hitPolicy="FIRST">
    <dmn:input id="amount" label="amount" typeRef="integer" />
    <dmn:output id="approved" name="approved" typeRef="boolean" />
    <dmn:rule id="high">
      <dmn:inputEntry text="&gt;= 100" />
      <dmn:outputEntry text="true" />
    </dmn:rule>
  </dmn:decisionTable>
</dmn:definitions>"#;

fn compiler() -> BpmnCompiler {
    BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap())
}

#[test]
fn structurally_balanced_parallel_gateway_round_trips_and_loads() {
    let compiler = compiler();
    let first = compiler
        .compile(
            SourceDocument {
                name: "parallel.bpmn",
                bytes: PARALLEL_BPMN.as_bytes(),
            },
            "tenant-a",
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
            "tenant-a",
            "1",
        )
        .unwrap();
    assert_eq!(first, second);

    let signer = Ed25519Signer::from_bytes(&[21; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(first, &signer).unwrap();
    WirLoader::load(&artifact, &verifier).unwrap();
}

#[test]
fn structurally_balanced_inclusive_gateway_round_trips_and_loads() {
    let compiler = compiler();
    let first = compiler
        .compile(
            SourceDocument {
                name: "inclusive.bpmn",
                bytes: INCLUSIVE_BPMN.as_bytes(),
            },
            "tenant-a",
            "1",
        )
        .unwrap();
    let canonical = compiler.print(&first).unwrap();
    let second = compiler
        .compile(
            SourceDocument {
                name: "canonical-inclusive.bpmn",
                bytes: canonical.as_bytes(),
            },
            "tenant-a",
            "1",
        )
        .unwrap();
    assert_eq!(first, second);
    let signer = Ed25519Signer::from_bytes(&[22; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(first, &signer).unwrap();
    WirLoader::load(&artifact, &verifier).unwrap();
}

#[test]
fn unbalanced_parallel_split_is_rejected() {
    let invalid = PARALLEL_BPMN
        .replace("targetRef=\"join\"", "targetRef=\"end\"")
        .replace("<bpmn:parallelGateway id=\"join\" />", "");
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "unbalanced.bpmn",
                bytes: invalid.as_bytes(),
            },
            "tenant-a",
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        diagnostic
            .kind
            .to_string()
            .contains("structurally unbalanced")
            || diagnostic.kind.to_string().contains("missing node join")
    }));
}

#[test]
fn cmmn_subset_is_embedded_in_canonical_wir() {
    let wir = compiler()
        .compile_with_models(
            SourceDocument {
                name: "parallel.bpmn",
                bytes: PARALLEL_BPMN.as_bytes(),
            },
            &[],
            &[SourceDocument {
                name: "claim.cmmn",
                bytes: CMMN.as_bytes(),
            }],
            "tenant-a",
            "1",
        )
        .unwrap();
    assert_eq!(wir.case_models.len(), 1);
    assert_eq!(wir.case_models[0].id, "claim-case");
    assert_eq!(
        wir.case_models[0].stages[0].entry_sentry_ids,
        ["documents-ready"]
    );
}

#[test]
fn cmmn_missing_sentry_reference_is_rejected() {
    let invalid = CMMN.replace(
        "entrySentryRefs=\"documents-ready\"",
        "entrySentryRefs=\"missing\"",
    );
    let diagnostics = compiler()
        .compile_with_models(
            SourceDocument {
                name: "parallel.bpmn",
                bytes: PARALLEL_BPMN.as_bytes(),
            },
            &[],
            &[SourceDocument {
                name: "invalid.cmmn",
                bytes: invalid.as_bytes(),
            }],
            "tenant-a",
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(&diagnostic.kind, DiagnosticKind::InvalidCaseModel { detail, .. }
            if detail.contains("references missing sentry missing"))
    }));
}

#[test]
fn dmn_rule_cardinality_mismatch_is_rejected() {
    let invalid = DMN.replace("<dmn:inputEntry text=\"&gt;= 100\" />", "");
    let diagnostics = compiler()
        .compile_with_decisions(
            SourceDocument {
                name: "decision.bpmn",
                bytes: DECISION_BPMN.as_bytes(),
            },
            &[SourceDocument {
                name: "invalid.dmn",
                bytes: invalid.as_bytes(),
            }],
            "tenant-a",
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(&diagnostic.kind, DiagnosticKind::InvalidDecisionTable { detail, .. }
            if detail.contains("0 input tests, expected 1"))
    }));
}

#[test]
fn dmn_unsupported_type_is_rejected() {
    let invalid = DMN.replace("typeRef=\"integer\"", "typeRef=\"decimal\"");
    let diagnostics = compiler()
        .compile_with_decisions(
            SourceDocument {
                name: "decision.bpmn",
                bytes: DECISION_BPMN.as_bytes(),
            },
            &[SourceDocument {
                name: "invalid-type.dmn",
                bytes: invalid.as_bytes(),
            }],
            "tenant-a",
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| {
        matches!(&diagnostic.kind, DiagnosticKind::InvalidDecisionTable { detail, .. }
            if detail.contains("typeRef must be boolean, integer, or string"))
    }));
}

#[test]
fn generated_rust_state_machine_is_standalone_and_compilable() {
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "parallel.bpmn",
                bytes: PARALLEL_BPMN.as_bytes(),
            },
            "tenant-a",
            "1",
        )
        .unwrap();
    let generated = compiler.generate_rust(&wir).unwrap();
    let directory = tempfile::tempdir().unwrap();
    let source = directory.path().join("generated.rs");
    let library = directory.path().join("generated.rlib");
    fs::write(&source, generated).unwrap();
    let result = Command::new("rustc")
        .args(["--edition=2024", "--crate-type=lib"])
        .arg(&source)
        .arg("-o")
        .arg(&library)
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "generated Rust failed to compile: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(library.exists());
}

#[test]
fn generated_rust_contains_compilable_typed_dmn_evaluator() {
    let compiler = compiler();
    let wir = compiler
        .compile_with_decisions(
            SourceDocument {
                name: "decision.bpmn",
                bytes: DECISION_BPMN.as_bytes(),
            },
            &[SourceDocument {
                name: "risk.dmn",
                bytes: DMN.as_bytes(),
            }],
            "tenant-a",
            "1",
        )
        .unwrap();
    let generated = compiler.generate_rust(&wir).unwrap();
    assert!(generated.contains("fn decision_0"));
    let directory = tempfile::tempdir().unwrap();
    let source = directory.path().join("generated_dmn.rs");
    fs::write(&source, generated).unwrap();
    let result = Command::new("rustc")
        .args(["--edition=2024", "--crate-type=lib"])
        .arg(&source)
        .arg("-o")
        .arg(directory.path().join("generated_dmn.rlib"))
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "generated DMN evaluator failed to compile: {}",
        String::from_utf8_lossy(&result.stderr)
    );
}
