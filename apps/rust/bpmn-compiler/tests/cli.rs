use std::fs;
use std::process::Command;

use bpmp_contracts::{Ed25519Signer, Ed25519Verifier, WirCodec};

const VALID: &str = r#"<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="order">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="charge" name="payment" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="charge" />
    <bpmn:sequenceFlow id="f2" sourceRef="charge" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>"#;

fn command() -> Command {
    Command::new(env!("CARGO_BIN_EXE_bpmn-compiler"))
}

#[test]
fn cli_emits_a_verifiable_artifact() {
    let directory = tempfile::tempdir().unwrap();
    let input = directory.path().join("order.bpmn");
    let output = directory.path().join("order.wir");
    let key = directory.path().join("signing.key");
    fs::write(&input, VALID).unwrap();
    fs::write(&key, [17; 32]).unwrap();

    let status = command()
        .args([
            "--input",
            input.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
            "--workflow-version",
            "1",
            "--signing-key",
            key.to_str().unwrap(),
            "--max-input-bytes",
            "65536",
            "--max-xml-depth",
            "32",
        ])
        .status()
        .unwrap();

    assert!(status.success());
    let signer = Ed25519Signer::from_bytes(&[17; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    assert!(WirCodec::open(&fs::read(output).unwrap(), &verifier).is_ok());
}

#[test]
fn cli_returns_ci_failure_with_source_location() {
    let directory = tempfile::tempdir().unwrap();
    let input = directory.path().join("invalid.bpmn");
    let output = directory.path().join("invalid.wir");
    let key = directory.path().join("signing.key");
    fs::write(
        &input,
        VALID.replace("targetRef=\"charge\"", "targetRef=\"missing\""),
    )
    .unwrap();
    fs::write(&key, [17; 32]).unwrap();

    let result = command()
        .args([
            "--input",
            input.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
            "--workflow-version",
            "1",
            "--signing-key",
            key.to_str().unwrap(),
            "--max-input-bytes",
            "65536",
            "--max-xml-depth",
            "32",
        ])
        .output()
        .unwrap();

    assert_eq!(result.status.code(), Some(1));
    let stderr = String::from_utf8(result.stderr).unwrap();
    assert!(stderr.contains("invalid.bpmn:"));
    assert!(stderr.contains("error:"));
    assert!(!output.exists());
}
