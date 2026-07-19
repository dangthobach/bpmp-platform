use std::fs;
use std::process::Command;

use bpmn_compiler::{BpmnCompiler, CompilerLimits, DiagnosticKind, SourceDocument};
use bpmp_contracts::wir::v1::node;
use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};
use bpmp_engine::WirLoader;

const BPMN_NS: &str = "http://www.omg.org/spec/BPMN/20100524/MODEL";
const TENANT: &str = "tenant-enterprise";

fn compiler() -> BpmnCompiler {
    BpmnCompiler::new(CompilerLimits::new(128 * 1024, 64).unwrap())
}

fn compile(
    source: &str,
) -> Result<
    bpmp_contracts::wir::v1::WorkflowIntermediateRepresentation,
    Vec<bpmn_compiler::CompileDiagnostic>,
> {
    compiler().compile(
        SourceDocument {
            name: "human-script.bpmn",
            bytes: source.as_bytes(),
        },
        TENANT,
        "2026.07.19",
    )
}

#[test]
fn user_and_versioned_script_tasks_survive_all_compiler_boundaries() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="approval"><b:startEvent id="start"/><b:userTask id="review" name="review-request" assignmentPolicyRef="approval-reviewers" formKey="approval-form-v2" resultVariable="review_result"/><b:scriptTask id="calculate" name="calculate-risk" implementationRef="wasm://risk/calculate" implementationVersion="sha256:abc123"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="review"/><b:sequenceFlow id="f2" sourceRef="review" targetRef="calculate"/><b:sequenceFlow id="f3" sourceRef="calculate" targetRef="end"/></b:process></b:definitions>"#
    );
    let compiler = compiler();
    let wir = compile(&source).unwrap();

    let review = wir.nodes.iter().find(|node| node.id == "review").unwrap();
    let Some(node::Kind::UserTask(review)) = review.kind.as_ref() else {
        panic!("review must lower to a typed user task")
    };
    assert_eq!(review.task_type, "review-request");
    assert_eq!(review.assignment_policy_ref, "approval-reviewers");
    assert_eq!(review.form_key, "approval-form-v2");
    assert_eq!(review.result_variable, "review_result");

    let calculate = wir
        .nodes
        .iter()
        .find(|node| node.id == "calculate")
        .unwrap();
    let Some(node::Kind::ScriptTask(calculate)) = calculate.kind.as_ref() else {
        panic!("calculate must lower to a typed script task")
    };
    assert_eq!(calculate.implementation_ref, "wasm://risk/calculate");
    assert_eq!(calculate.implementation_version, "sha256:abc123");

    let canonical = compiler.print(&wir).unwrap();
    let recompiled = compile(&canonical).unwrap();
    assert_eq!(wir, recompiled);

    let generated = compiler.generate_rust(&wir).unwrap();
    assert!(generated.contains("NodeKind::UserTask"));
    assert!(generated.contains("NodeKind::ScriptTask"));
    assert!(generated.contains("approval-reviewers"));
    assert!(generated.contains("wasm://risk/calculate"));
    let directory = tempfile::tempdir().unwrap();
    let generated_source = directory.path().join("human_script.rs");
    fs::write(&generated_source, generated).unwrap();
    let result = Command::new("rustc")
        .args(["--edition=2024", "--crate-type=lib"])
        .arg(&generated_source)
        .arg("-o")
        .arg(directory.path().join("human_script.rlib"))
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "generated user/script state machine failed to compile: {}",
        String::from_utf8_lossy(&result.stderr)
    );

    let signer = Ed25519Signer::from_bytes(&[73; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();
    let definition = WirLoader::load(&artifact, &verifier).unwrap();
    assert_eq!(definition.tenant_id.as_str(), TENANT);
    assert_eq!(definition.workflow_type.as_str(), "approval");
}

#[test]
fn user_task_uses_node_id_as_dynamic_assignment_policy_lookup_key_when_omitted() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="approval"><b:startEvent id="start"/><b:userTask id="review"/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="review"/><b:sequenceFlow id="f2" sourceRef="review" targetRef="end"/></b:process></b:definitions>"#
    );
    let wir = compile(&source).unwrap();
    let review = wir.nodes.iter().find(|node| node.id == "review").unwrap();
    let Some(node::Kind::UserTask(review)) = review.kind.as_ref() else {
        panic!("review must lower to a typed user task")
    };
    assert_eq!(review.assignment_policy_ref, "review");
}

#[test]
fn script_task_requires_a_pinned_external_implementation() {
    for (attributes, missing) in [
        ("implementationVersion=\"1\"", "implementationRef"),
        (
            "implementationRef=\"wasm://risk/calculate\"",
            "implementationVersion",
        ),
    ] {
        let source = format!(
            r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="script"><b:startEvent id="start"/><b:scriptTask id="calculate" {attributes}/><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="calculate"/><b:sequenceFlow id="f2" sourceRef="calculate" targetRef="end"/></b:process></b:definitions>"#
        );
        let diagnostics = compile(&source).unwrap_err();
        assert!(diagnostics.iter().any(|diagnostic| matches!(
            &diagnostic.kind,
            DiagnosticKind::MissingAttribute { element, attribute }
                if element == "scriptTask" && *attribute == missing
        )));
    }
}

#[test]
fn inline_script_body_fails_closed() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="script"><b:startEvent id="start"/><b:scriptTask id="calculate" implementationRef="wasm://risk/calculate" implementationVersion="1"><b:script>doSomething()</b:script></b:scriptTask><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="calculate"/><b:sequenceFlow id="f2" sourceRef="calculate" targetRef="end"/></b:process></b:definitions>"#
    );
    let diagnostics = compile(&source).unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| matches!(
        &diagnostic.kind,
        DiagnosticKind::UnsupportedElement { element } if element == "script"
    )));
}

#[test]
fn user_and_script_tasks_accept_durable_multi_instance_and_boundary_contracts() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="batch-approval"><b:startEvent id="start"/><b:userTask id="review" assignmentPolicyRef="reviewers"><b:multiInstanceLoopCharacteristics isSequential="false" collection="reviewers" elementVariable="reviewer" maxParallelism="4"><b:completionCondition>nrOfCompletedInstances &gt;= 2</b:completionCondition></b:multiInstanceLoopCharacteristics></b:userTask><b:boundaryEvent id="review-timeout" attachedToRef="review" cancelActivity="true"><b:timerEventDefinition><b:timeDuration>PT1H</b:timeDuration></b:timerEventDefinition></b:boundaryEvent><b:scriptTask id="calculate" implementationRef="wasm://risk/calculate" implementationVersion="7"><b:multiInstanceLoopCharacteristics isSequential="true"><b:loopCardinality>3</b:loopCardinality></b:multiInstanceLoopCharacteristics></b:scriptTask><b:endEvent id="done"/><b:endEvent id="expired"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="review"/><b:sequenceFlow id="f2" sourceRef="review" targetRef="calculate"/><b:sequenceFlow id="f3" sourceRef="calculate" targetRef="done"/><b:sequenceFlow id="f4" sourceRef="review-timeout" targetRef="expired"/></b:process></b:definitions>"#
    );
    let wir = compile(&source).unwrap();
    let review = wir.nodes.iter().find(|node| node.id == "review").unwrap();
    assert!(review.multi_instance.is_some());
    assert_eq!(review.boundary_events.len(), 1);
    let calculate = wir
        .nodes
        .iter()
        .find(|node| node.id == "calculate")
        .unwrap();
    assert!(calculate.multi_instance.is_some());

    let signer = Ed25519Signer::from_bytes(&[74; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();
    WirLoader::load(&artifact, &verifier).unwrap();
}
