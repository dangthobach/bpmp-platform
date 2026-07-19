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
            "--tenant-id",
            "tenant-a",
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
    let wir = WirCodec::open(&fs::read(output).unwrap(), &verifier).unwrap();
    assert_eq!(wir.tenant_id, "tenant-a");
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
            "--tenant-id",
            "tenant-a",
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

#[test]
fn cli_compiles_combined_bpmn_dmn_cmmn_and_rust_outputs() {
    let directory = tempfile::tempdir().unwrap();
    let input = directory.path().join("decision.bpmn");
    let dmn = directory.path().join("risk.dmn");
    let cmmn = directory.path().join("case.cmmn");
    let output = directory.path().join("decision.wir");
    let rust_output = directory.path().join("decision.rs");
    let key = directory.path().join("signing.key");
    fs::write(
        &input,
        VALID.replace(
            "<bpmn:serviceTask id=\"charge\" name=\"payment\" />",
            "<bpmn:businessRuleTask id=\"charge\" decisionRef=\"risk\" />",
        ),
    )
    .unwrap();
    fs::write(
        &dmn,
        r#"<d:definitions xmlns:d="https://www.omg.org/spec/DMN/20191111/MODEL/"><d:decisionTable id="risk" hitPolicy="FIRST"><d:input id="amount" label="amount" typeRef="integer"/><d:output id="approved" name="approved" typeRef="boolean"/><d:rule id="r1"><d:inputEntry text="-"/><d:outputEntry text="true"/></d:rule></d:decisionTable></d:definitions>"#,
    )
    .unwrap();
    fs::write(
        &cmmn,
        r#"<c:definitions xmlns:c="https://www.omg.org/spec/CMMN/20151109/MODEL"><c:case id="case-1"><c:casePlanModel id="plan"><c:sentry id="ready"/><c:stage id="review" entrySentryRefs="ready"/><c:milestone id="done" entrySentryRefs="ready"/></c:casePlanModel></c:case></c:definitions>"#,
    )
    .unwrap();
    fs::write(&key, [23; 32]).unwrap();

    let status = command()
        .args([
            "--input",
            input.to_str().unwrap(),
            "--dmn",
            dmn.to_str().unwrap(),
            "--cmmn",
            cmmn.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
            "--rust-output",
            rust_output.to_str().unwrap(),
            "--workflow-version",
            "1",
            "--tenant-id",
            "tenant-a",
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
    let signer = Ed25519Signer::from_bytes(&[23; 32]);
    let verifier = Ed25519Verifier::from_bytes(&signer.verifying_key_bytes()).unwrap();
    let wir = WirCodec::open(&fs::read(output).unwrap(), &verifier).unwrap();
    assert_eq!(wir.decision_tables.len(), 1);
    assert_eq!(wir.case_models.len(), 1);
    let generated = fs::read_to_string(rust_output).unwrap();
    assert!(generated.contains("fn decision_0"));
}

#[test]
fn cli_rejects_signing_keys_outside_the_exact_32_byte_contract() {
    for (name, bytes) in [("short", vec![7; 31]), ("long", vec![7; 33])] {
        let directory = tempfile::tempdir().unwrap();
        let input = directory.path().join("order.bpmn");
        let output = directory.path().join("order.wir");
        let key = directory.path().join(format!("{name}.key"));
        fs::write(&input, VALID).unwrap();
        fs::write(&key, bytes).unwrap();

        let result = command()
            .args([
                "--input",
                input.to_str().unwrap(),
                "--output",
                output.to_str().unwrap(),
                "--workflow-version",
                "1",
                "--tenant-id",
                "tenant-a",
                "--signing-key",
                key.to_str().unwrap(),
                "--max-input-bytes",
                "65536",
                "--max-xml-depth",
                "32",
            ])
            .output()
            .unwrap();

        assert_eq!(result.status.code(), Some(2), "key case {name}");
        assert!(
            String::from_utf8(result.stderr)
                .unwrap()
                .contains("exactly 32 raw bytes")
        );
        assert!(!output.exists());
    }
}
