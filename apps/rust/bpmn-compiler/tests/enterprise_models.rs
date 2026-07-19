use std::fs;
use std::process::Command;

use bpmn_compiler::{BpmnCompiler, CompilerLimits, DiagnosticKind, SourceDocument};
use bpmp_contracts::wir::v1::{MultiInstanceMode, TimerKind, event_trigger, node, property_value};
use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};
use bpmp_domain_core::{
    ExtensionPropertyValue, MultiInstanceMode as DomainMultiInstanceMode, NodeId,
};
use bpmp_engine::WirLoader;

const TENANT: &str = "tenant-enterprise";
const BPMN_NS: &str = "http://www.omg.org/spec/BPMN/20100524/MODEL";

fn compiler() -> BpmnCompiler {
    BpmnCompiler::new(CompilerLimits::new(256 * 1024, 96).unwrap())
}

#[test]
fn nested_subprocess_is_inlined_and_round_trips_as_canonical_graph() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="subprocess-order"><b:startEvent id="start"/><b:subProcess id="review"><b:startEvent id="review-start"/><b:serviceTask id="review-task" name="review"/><b:endEvent id="review-end"/><b:sequenceFlow id="inner-1" sourceRef="review-start" targetRef="review-task"/><b:sequenceFlow id="inner-2" sourceRef="review-task" targetRef="review-end"/></b:subProcess><b:endEvent id="end"/><b:sequenceFlow id="outer-1" sourceRef="start" targetRef="review"/><b:sequenceFlow id="outer-2" sourceRef="review" targetRef="end"/></b:process></b:definitions>"#
    );
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "subprocess.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    assert_eq!(wir.nodes.len(), 3);
    assert!(wir.nodes.iter().any(|node| node.id == "review-task"));
    assert!(!wir.nodes.iter().any(|node| node.id == "review"));

    let canonical = compiler.print(&wir).unwrap();
    let recompiled = compiler
        .compile(
            SourceDocument {
                name: "canonical-subprocess.bpmn",
                bytes: canonical.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    assert_eq!(wir, recompiled);
}

#[test]
fn call_multi_instance_timer_and_typed_extensions_survive_artifact_round_trip() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}" xmlns:ext="urn:example:worker"><b:process id="enterprise-call"><b:startEvent id="start"/><b:callActivity id="call" calledElement="child-workflow" calledVersion="7"><b:extensionElements><ext:property name="timeout" type="durationMilliseconds" value="5000"/><ext:worker retries="5"/></b:extensionElements><b:multiInstanceLoopCharacteristics isSequential="false" collection="orders" elementVariable="order" maxParallelism="8"/></b:callActivity><b:boundaryEvent id="timeout" attachedToRef="call" cancelActivity="false"><b:timerEventDefinition><b:timeDuration>PT5M</b:timeDuration></b:timerEventDefinition></b:boundaryEvent><b:endEvent id="done"/><b:endEvent id="timed-out"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="call"/><b:sequenceFlow id="f2" sourceRef="call" targetRef="done"/><b:sequenceFlow id="timeout-flow" sourceRef="timeout" targetRef="timed-out"/></b:process></b:definitions>"#
    );
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "enterprise-call.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "7",
        )
        .unwrap();
    let call = wir.nodes.iter().find(|node| node.id == "call").unwrap();
    let Some(node::Kind::CallActivity(call_kind)) = &call.kind else {
        panic!("call activity must lower to its own WIR node")
    };
    assert_eq!(call_kind.called_element, "child-workflow");
    assert_eq!(call_kind.called_version, "7");
    let multi = call.multi_instance.as_ref().unwrap();
    assert_eq!(
        MultiInstanceMode::try_from(multi.mode).unwrap(),
        MultiInstanceMode::Parallel
    );
    assert_eq!(multi.max_parallelism, 8);
    assert_eq!(call.properties.len(), 2);
    assert!(call.properties.iter().any(|property| {
        property.name == "timeout"
            && matches!(
                property
                    .value
                    .as_ref()
                    .and_then(|value| value.value.as_ref()),
                Some(property_value::Value::DurationMilliseconds(5000))
            )
    }));
    let boundary = &call.boundary_events[0];
    assert!(!boundary.cancel_activity);
    assert!(matches!(
        boundary
            .trigger
            .as_ref()
            .and_then(|trigger| trigger.trigger.as_ref()),
        Some(event_trigger::Trigger::Timer(timer))
            if TimerKind::try_from(timer.kind) == Ok(TimerKind::Duration)
                && timer.expression == "PT5M"
    ));

    let canonical = compiler.print(&wir).unwrap();
    let recompiled = compiler
        .compile(
            SourceDocument {
                name: "canonical-enterprise-call.bpmn",
                bytes: canonical.as_bytes(),
            },
            TENANT,
            "7",
        )
        .unwrap();
    assert_eq!(wir, recompiled);

    let signer = Ed25519Signer::from_bytes(&[42; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();
    let definition = WirLoader::load(&artifact, &verifier).unwrap();
    assert_eq!(definition.workflow_type.as_str(), "enterprise-call");
    let metadata = definition
        .node_execution_metadata(&NodeId::new("call").unwrap())
        .unwrap();
    assert_eq!(
        metadata.multi_instance.as_ref().unwrap().mode,
        DomainMultiInstanceMode::Parallel
    );
    assert!(metadata.properties.iter().any(|property| {
        property.name == "timeout"
            && property.value == ExtensionPropertyValue::DurationMilliseconds(5000)
    }));
}

#[test]
fn error_and_message_boundaries_are_typed() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="boundary-types"><b:startEvent id="start"/><b:serviceTask id="work"/><b:boundaryEvent id="error" attachedToRef="work"><b:errorEventDefinition errorRef="business-error"/></b:boundaryEvent><b:boundaryEvent id="message" attachedToRef="work" cancelActivity="false"><b:messageEventDefinition messageRef="cancel-message"/></b:boundaryEvent><b:endEvent id="done"/><b:endEvent id="failed"/><b:endEvent id="cancelled"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="work"/><b:sequenceFlow id="f2" sourceRef="work" targetRef="done"/><b:sequenceFlow id="f3" sourceRef="error" targetRef="failed"/><b:sequenceFlow id="f4" sourceRef="message" targetRef="cancelled"/></b:process></b:definitions>"#
    );
    let wir = compiler()
        .compile(
            SourceDocument {
                name: "boundaries.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    let work = wir.nodes.iter().find(|node| node.id == "work").unwrap();
    assert!(work.boundary_events.iter().any(|boundary| matches!(
        boundary.trigger.as_ref().and_then(|trigger| trigger.trigger.as_ref()),
        Some(event_trigger::Trigger::Error(error)) if error.error_ref == "business-error"
    )));
    assert!(work.boundary_events.iter().any(|boundary| matches!(
        boundary.trigger.as_ref().and_then(|trigger| trigger.trigger.as_ref()),
        Some(event_trigger::Trigger::Message(message)) if message.message_ref == "cancel-message"
    )));
}

#[test]
fn ambiguous_subprocess_shape_fails_closed() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="invalid-sub"><b:startEvent id="start"/><b:subProcess id="sub"><b:startEvent id="s1"/><b:startEvent id="s2"/><b:endEvent id="inner-end"/></b:subProcess><b:endEvent id="end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="sub"/><b:sequenceFlow id="f2" sourceRef="sub" targetRef="end"/></b:process></b:definitions>"#
    );
    let diagnostics = compiler()
        .compile(
            SourceDocument {
                name: "invalid-subprocess.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(
        diagnostics
            .iter()
            .any(|diagnostic| matches!(diagnostic.kind, DiagnosticKind::InvalidSubProcess { .. }))
    );
}

#[test]
fn complex_boolean_guard_is_symbolically_proved_round_tripped_and_executed() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="complex-routing"><b:startEvent id="start"/><b:exclusiveGateway id="route"/><b:serviceTask id="priority"/><b:serviceTask id="standard"/><b:endEvent id="priority-end"/><b:endEvent id="standard-end"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="route"/><b:sequenceFlow id="priority-flow" sourceRef="route" targetRef="priority"><b:conditionExpression>(approved == true &amp;&amp; amount >= 100) || (approved == false &amp;&amp; amount &lt; 0)</b:conditionExpression></b:sequenceFlow><b:sequenceFlow id="standard-flow" sourceRef="route" targetRef="standard" condition="!((approved == true &amp;&amp; amount >= 100) || (approved == false &amp;&amp; amount &lt; 0))"/><b:sequenceFlow id="f2" sourceRef="priority" targetRef="priority-end"/><b:sequenceFlow id="f3" sourceRef="standard" targetRef="standard-end"/></b:process></b:definitions>"#
    );
    let compiler = compiler();
    let wir = compiler
        .compile(
            SourceDocument {
                name: "complex-routing.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    let route = wir.nodes.iter().find(|node| node.id == "route").unwrap();
    let Some(node::Kind::ExclusiveGateway(gateway)) = &route.kind else {
        panic!("route must be an exclusive gateway")
    };
    assert!(gateway.coverage.as_ref().unwrap().solver_verified);
    assert!(
        gateway
            .transitions
            .iter()
            .all(|transition| transition.expression.is_some())
    );

    let canonical = compiler.print(&wir).unwrap();
    let recompiled = compiler
        .compile(
            SourceDocument {
                name: "canonical-complex-routing.bpmn",
                bytes: canonical.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap();
    assert_eq!(wir, recompiled);

    let mut generated = compiler.generate_rust(&wir).unwrap();
    generated.push_str(
        r#"
fn main() {
    let variables = std::collections::BTreeMap::from([
        ("approved", RuntimeValue::Boolean(true)),
        ("amount", RuntimeValue::Integer(150)),
    ]);
    assert_eq!(enabled_targets("route", &variables).unwrap(), vec!["priority"]);
}
"#,
    );
    let directory = tempfile::tempdir().unwrap();
    let generated_path = directory.path().join("complex_generated.rs");
    let executable = directory.path().join("complex_generated.exe");
    fs::write(&generated_path, generated).unwrap();
    let compilation = Command::new("rustc")
        .args(["--edition=2024"])
        .arg(&generated_path)
        .arg("-o")
        .arg(&executable)
        .output()
        .unwrap();
    assert!(
        compilation.status.success(),
        "generated complex evaluator failed to compile: {}",
        String::from_utf8_lossy(&compilation.stderr)
    );
    assert!(Command::new(executable).status().unwrap().success());
}

#[test]
fn symbolic_solver_budget_is_configurable_and_fails_closed() {
    let source = format!(
        r#"<b:definitions xmlns:b="{BPMN_NS}"><b:process id="bounded-solver"><b:startEvent id="start"/><b:exclusiveGateway id="route"/><b:endEvent id="yes"/><b:endEvent id="no"/><b:sequenceFlow id="f1" sourceRef="start" targetRef="route"/><b:sequenceFlow id="f2" sourceRef="route" targetRef="yes" condition="a == true &amp;&amp; b == true"/><b:sequenceFlow id="f3" sourceRef="route" targetRef="no" condition="!(a == true &amp;&amp; b == true)"/></b:process></b:definitions>"#
    );
    let limits = CompilerLimits::new(256 * 1024, 96)
        .unwrap()
        .with_max_symbolic_assignments(2)
        .unwrap();
    let diagnostics = BpmnCompiler::new(limits)
        .compile(
            SourceDocument {
                name: "bounded-solver.bpmn",
                bytes: source.as_bytes(),
            },
            TENANT,
            "1",
        )
        .unwrap_err();
    assert!(diagnostics.iter().any(|diagnostic| matches!(
        &diagnostic.kind,
        DiagnosticKind::NonExhaustiveGateway { detail, .. }
            if detail.contains("above configured limit 2")
    )));
}
