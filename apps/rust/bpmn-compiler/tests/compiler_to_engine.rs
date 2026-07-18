use bpmn_compiler::{BpmnCompiler, CompilerLimits, SourceDocument};
use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};
use bpmp_engine::WirLoader;

const BPMN: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="shipment">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="dispatch" name="dispatch-shipment" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="dispatch" />
    <bpmn:sequenceFlow id="f2" sourceRef="dispatch" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;

const GATEWAY_BPMN: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
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

const EXHAUSTIVE_GATEWAY_BPMN: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="exhaustive-routing">
    <bpmn:startEvent id="start" />
    <bpmn:exclusiveGateway id="route" />
    <bpmn:endEvent id="approved" />
    <bpmn:endEvent id="rejected" />
    <bpmn:sequenceFlow id="to-route" sourceRef="start" targetRef="route" />
    <bpmn:sequenceFlow id="yes" sourceRef="route" targetRef="approved" condition="approved == true" />
    <bpmn:sequenceFlow id="no" sourceRef="route" targetRef="rejected" condition="approved == false" />
  </bpmn:process>
</bpmn:definitions>"#;

#[test]
fn compiler_artifact_loads_into_the_authoritative_engine_domain() {
    let compiler = BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap());
    let wir = compiler
        .compile(
            SourceDocument {
                name: "shipment.bpmn",
                bytes: BPMN.as_bytes(),
            },
            "2026-07-18.1",
        )
        .unwrap();
    let signer = Ed25519Signer::from_bytes(&[11; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();

    let definition = WirLoader::load(&artifact, &verifier).unwrap();

    assert_eq!(definition.workflow_type.as_str(), "shipment");
    assert_eq!(definition.workflow_version.as_str(), "2026-07-18.1");
}

#[test]
fn typed_gateway_artifact_loads_into_the_authoritative_engine_domain() {
    let compiler = BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap());
    let wir = compiler
        .compile(
            SourceDocument {
                name: "routing.bpmn",
                bytes: GATEWAY_BPMN.as_bytes(),
            },
            "1",
        )
        .unwrap();
    let signer = Ed25519Signer::from_bytes(&[12; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();

    let definition = WirLoader::load(&artifact, &verifier).unwrap();

    assert_eq!(definition.workflow_type.as_str(), "routing");
}

#[test]
fn statically_exhaustive_gateway_artifact_loads_into_the_authoritative_engine_domain() {
    let compiler = BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap());
    let wir = compiler
        .compile(
            SourceDocument {
                name: "exhaustive-routing.bpmn",
                bytes: EXHAUSTIVE_GATEWAY_BPMN.as_bytes(),
            },
            "1",
        )
        .unwrap();
    let signer = Ed25519Signer::from_bytes(&[13; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();

    let definition = WirLoader::load(&artifact, &verifier).unwrap();

    assert_eq!(definition.workflow_type.as_str(), "exhaustive-routing");
}
