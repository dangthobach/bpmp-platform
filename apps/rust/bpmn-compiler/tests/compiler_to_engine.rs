use bpmn_compiler::{BpmnCompiler, CompilerLimits, SourceDocument};
use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};
use bpmp_engine::WirLoader;
use proptest::prelude::*;

const TENANT_ID: &str = "tenant-a";

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
            TENANT_ID,
            "2026-07-18.1",
        )
        .unwrap();
    let signer = Ed25519Signer::from_bytes(&[11; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();

    let definition = WirLoader::load(&artifact, &verifier).unwrap();

    assert_eq!(definition.workflow_type.as_str(), "shipment");
    assert_eq!(definition.workflow_version.as_str(), "2026-07-18.1");
    assert_eq!(definition.tenant_id.as_str(), TENANT_ID);
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
            TENANT_ID,
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
            TENANT_ID,
            "1",
        )
        .unwrap();
    let signer = Ed25519Signer::from_bytes(&[13; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let artifact = WirCodec::seal(wir, &signer).unwrap();

    let definition = WirLoader::load(&artifact, &verifier).unwrap();

    assert_eq!(definition.workflow_type.as_str(), "exhaustive-routing");
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(100))]

    #[test]
    fn compile_print_compile_round_trip_is_equivalent_at_public_api_boundary(
        process_suffix in "[a-z][a-z0-9]{0,10}",
        task_suffix in "[a-z][a-z0-9]{0,10}",
    ) {
        let source = BPMN
            .replace("id=\"shipment\"", &format!("id=\"shipment-{process_suffix}\""))
            .replace("name=\"dispatch-shipment\"", &format!("name=\"dispatch-{task_suffix}\""));
        let compiler = BpmnCompiler::new(CompilerLimits::new(64 * 1024, 32).unwrap());
        let first = compiler
            .compile(
                SourceDocument {
                    name: "generated.bpmn",
                    bytes: source.as_bytes(),
                },
                TENANT_ID,
                "property-v1",
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
                "property-v1",
            )
            .unwrap();
        prop_assert_eq!(first, second);
    }
}
