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
