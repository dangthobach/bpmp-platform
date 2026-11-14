use std::{env, error::Error, fs, io::Write, net::SocketAddr, path::PathBuf};

use bpmn_compiler::{BpmnCompiler, CompileDiagnostic, CompilerLimits, SourceDocument};
use bpmp_contracts::compiler::v1::{
    CompileBpmnRequest, CompileBpmnResponse, Diagnostic, GetWirArtifactRequest,
    GetWirArtifactResponse, ValidateBpmnRequest, ValidateBpmnResponse, ValidateDmnRequest,
    ValidateDmnResponse,
    bpmn_compiler_service_server::{BpmnCompilerService, BpmnCompilerServiceServer},
};
use bpmp_contracts::{Ed25519Signer, WirCodec};
use bpmp_transport_observability::{AdmissionLimiter, RequestMetadataLayer, WorkloadAuthorization};
use sha2::{Digest, Sha256};
use tempfile::NamedTempFile;
use tonic::{
    Request, Response, Status,
    transport::{Identity, Server, ServerTlsConfig},
};

struct CompilerService {
    compiler: BpmnCompiler,
    signer: Option<Ed25519Signer>,
    max_input_bytes: usize,
    artifact_directory: Option<PathBuf>,
}

#[tonic::async_trait]
impl BpmnCompilerService for CompilerService {
    async fn compile_bpmn(
        &self,
        request: Request<CompileBpmnRequest>,
    ) -> Result<Response<CompileBpmnResponse>, Status> {
        let request = request.into_inner();
        if request.tenant_id.trim().is_empty()
            || request.process_model_id.trim().is_empty()
            || request.source_revision < 1
            || request.workflow_version.trim().is_empty()
            || request.bpmn_source_name.trim().is_empty()
            || request.bpmn_source_name.len() > 256
        {
            return Err(Status::invalid_argument(
                "tenant, model, revision, workflow version and BPMN source are required",
            ));
        }
        let signer = self.signer.as_ref().ok_or_else(|| {
            Status::failed_precondition("WIR signing is not configured on this compiler")
        })?;
        let artifact_directory = self.artifact_directory.as_ref().ok_or_else(|| {
            Status::failed_precondition("durable WIR artifact storage is not configured")
        })?;
        let total_bytes = request
            .decision_models
            .iter()
            .chain(request.case_models.iter())
            .try_fold(request.bpmn_xml.len(), |total, source| {
                total.checked_add(source.content.len())
            })
            .ok_or_else(|| Status::resource_exhausted("compile source size overflow"))?;
        let source_count = request.decision_models.len() + request.case_models.len() + 1;
        if source_count > 33
            || total_bytes > (4 << 20)
            || request.bpmn_xml.len() > self.max_input_bytes
            || request
                .decision_models
                .iter()
                .chain(request.case_models.iter())
                .any(|source| {
                    source.source_name.trim().is_empty()
                        || source.source_name.len() > 256
                        || source.content.is_empty()
                        || source.content.len() > self.max_input_bytes
                })
        {
            return Err(Status::resource_exhausted(
                "compile inputs exceed count or byte limits",
            ));
        }
        let decisions = request
            .decision_models
            .iter()
            .map(|source| SourceDocument {
                name: &source.source_name,
                bytes: &source.content,
            })
            .collect::<Vec<_>>();
        let cases = request
            .case_models
            .iter()
            .map(|source| SourceDocument {
                name: &source.source_name,
                bytes: &source.content,
            })
            .collect::<Vec<_>>();
        let compiled = self.compiler.compile_with_models(
            SourceDocument {
                name: &request.bpmn_source_name,
                bytes: &request.bpmn_xml,
            },
            &decisions,
            &cases,
            &request.tenant_id,
            &request.workflow_version,
        );
        let wir = match compiled {
            Ok(wir) => wir,
            Err(diagnostics) => {
                return Ok(Response::new(CompileBpmnResponse {
                    valid: false,
                    signed_wir_artifact: Vec::new(),
                    artifact_sha256: Vec::new(),
                    artifact_id: String::new(),
                    diagnostics: to_diagnostics(diagnostics),
                }));
            }
        };
        if !request.expected_workflow_type.trim().is_empty()
            && wir.workflow_type != request.expected_workflow_type
        {
            return Ok(Response::new(CompileBpmnResponse {
                valid: false,
                signed_wir_artifact: Vec::new(),
                artifact_sha256: Vec::new(),
                artifact_id: String::new(),
                diagnostics: vec![Diagnostic {
                    code: "WORKFLOW_TYPE_MISMATCH".into(),
                    severity: "ERROR".into(),
                    message: format!(
                        "compiled BPMN workflow type {:?} does not match requested type",
                        wir.workflow_type
                    ),
                    element_id: String::new(),
                    line: 0,
                    column: 0,
                }],
            }));
        }
        let workflow_type = wir.workflow_type.clone();
        let artifact = WirCodec::seal(wir, signer)
            .map_err(|error| Status::internal(format!("WIR sealing failed: {error}")))?;
        let artifact_sha256 = Sha256::digest(&artifact).to_vec();
        let artifact_id = store_artifact(artifact_directory, &artifact_sha256, &artifact)?;
        Ok(Response::new(CompileBpmnResponse {
            valid: true,
            signed_wir_artifact: artifact,
            artifact_sha256,
            diagnostics: Vec::new(),
            artifact_id,
            workflow_type,
        }))
    }

    async fn get_wir_artifact(
        &self,
        request: Request<GetWirArtifactRequest>,
    ) -> Result<Response<GetWirArtifactResponse>, Status> {
        let artifact_id = request.into_inner().artifact_id;
        let digest = parse_artifact_id(&artifact_id)?;
        let directory = self.artifact_directory.as_ref().ok_or_else(|| {
            Status::failed_precondition("durable WIR artifact storage is not configured")
        })?;
        let path = directory.join(format!("{artifact_id}.wir"));
        let artifact = fs::read(path).map_err(|error| match error.kind() {
            std::io::ErrorKind::NotFound => Status::not_found("WIR artifact not found"),
            _ => Status::unavailable("WIR artifact storage read failed"),
        })?;
        if artifact.is_empty()
            || artifact.len() > (8 << 20)
            || Sha256::digest(&artifact).as_slice() != digest
        {
            return Err(Status::data_loss(
                "stored WIR artifact failed its content-address check",
            ));
        }
        Ok(Response::new(GetWirArtifactResponse {
            signed_wir_artifact: artifact,
            artifact_sha256: digest.to_vec(),
        }))
    }

    async fn validate_bpmn(
        &self,
        request: Request<ValidateBpmnRequest>,
    ) -> Result<Response<ValidateBpmnResponse>, Status> {
        let request = request.into_inner();
        if request.tenant_id.trim().is_empty()
            || request.process_model_id.trim().is_empty()
            || request.draft_revision < 1
            || request.source_name.trim().is_empty()
            || request.source_name.len() > 256
        {
            return Err(Status::invalid_argument(
                "tenant, model, revision and source name are required",
            ));
        }
        let source = SourceDocument {
            name: &request.source_name,
            bytes: &request.bpmn_xml,
        };
        let result = self.compiler.compile(
            source,
            &request.tenant_id,
            &format!("{}-r{}", request.process_model_id, request.draft_revision),
        );
        let response = match result {
            Ok(_) => ValidateBpmnResponse {
                valid: true,
                diagnostics: Vec::new(),
            },
            Err(diagnostics) => ValidateBpmnResponse {
                valid: false,
                diagnostics: diagnostics
                    .into_iter()
                    .map(|diagnostic| Diagnostic {
                        code: "BPMN_VALIDATION_ERROR".into(),
                        severity: "ERROR".into(),
                        message: diagnostic.kind.to_string(),
                        element_id: String::new(),
                        line: u64::from(diagnostic.span.line),
                        column: u64::from(diagnostic.span.column),
                    })
                    .collect(),
            },
        };
        Ok(Response::new(response))
    }

    async fn validate_dmn(
        &self,
        request: Request<ValidateDmnRequest>,
    ) -> Result<Response<ValidateDmnResponse>, Status> {
        let request = request.into_inner();
        if request.tenant_id.trim().is_empty()
            || request.decision_model_id.trim().is_empty()
            || request.draft_revision < 1
            || request.source_name.trim().is_empty()
            || request.source_name.len() > 256
        {
            return Err(Status::invalid_argument(
                "tenant, decision model, revision and source name are required",
            ));
        }
        let source = SourceDocument {
            name: &request.source_name,
            bytes: &request.dmn_xml,
        };
        let result = self.compiler.validate_dmn(source);
        let response = match result {
            Ok(()) => ValidateDmnResponse {
                valid: true,
                diagnostics: Vec::new(),
            },
            Err(diagnostics) => ValidateDmnResponse {
                valid: false,
                diagnostics: diagnostics
                    .into_iter()
                    .map(|diagnostic| Diagnostic {
                        code: "DMN_VALIDATION_ERROR".into(),
                        severity: "ERROR".into(),
                        message: diagnostic.kind.to_string(),
                        element_id: String::new(),
                        line: u64::from(diagnostic.span.line),
                        column: u64::from(diagnostic.span.column),
                    })
                    .collect(),
            },
        };
        Ok(Response::new(response))
    }
}

fn to_diagnostics(values: Vec<CompileDiagnostic>) -> Vec<Diagnostic> {
    values
        .into_iter()
        .map(|diagnostic| Diagnostic {
            code: "COMPILE_ERROR".into(),
            severity: "ERROR".into(),
            message: diagnostic.kind.to_string(),
            element_id: String::new(),
            line: u64::from(diagnostic.span.line),
            column: u64::from(diagnostic.span.column),
        })
        .collect()
}

fn parse_artifact_id(value: &str) -> Result<[u8; 32], Status> {
    if value.len() != 64
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    {
        return Err(Status::invalid_argument(
            "artifact_id must be a lowercase SHA-256 hex digest",
        ));
    }
    let mut digest = [0_u8; 32];
    for (index, pair) in value.as_bytes().chunks_exact(2).enumerate() {
        let high = hex_nibble(pair[0])
            .ok_or_else(|| Status::invalid_argument("artifact_id is invalid"))?;
        let low = hex_nibble(pair[1])
            .ok_or_else(|| Status::invalid_argument("artifact_id is invalid"))?;
        digest[index] = (high << 4) | low;
    }
    Ok(digest)
}

fn hex_nibble(value: u8) -> Option<u8> {
    match value {
        b'0'..=b'9' => Some(value - b'0'),
        b'a'..=b'f' => Some(value - b'a' + 10),
        _ => None,
    }
}

fn store_artifact(
    directory: &std::path::Path,
    digest: &[u8],
    artifact: &[u8],
) -> Result<String, Status> {
    if digest.len() != 32 || artifact.is_empty() || artifact.len() > (8 << 20) {
        return Err(Status::resource_exhausted(
            "signed WIR artifact exceeds storage limits",
        ));
    }
    let artifact_id = digest
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    fs::create_dir_all(directory)
        .map_err(|_| Status::unavailable("WIR artifact directory is unavailable"))?;
    let path = directory.join(format!("{artifact_id}.wir"));
    let mut temporary = NamedTempFile::new_in(directory)
        .map_err(|_| Status::unavailable("WIR artifact temporary file could not be created"))?;
    temporary
        .write_all(artifact)
        .map_err(|_| Status::unavailable("WIR artifact write failed"))?;
    temporary
        .as_file()
        .sync_all()
        .map_err(|_| Status::unavailable("WIR artifact sync failed"))?;
    match temporary.persist_noclobber(&path) {
        Ok(_) => {}
        Err(error) if error.error.kind() == std::io::ErrorKind::AlreadyExists => {
            let existing = fs::read(&path)
                .map_err(|_| Status::unavailable("existing WIR artifact could not be read"))?;
            if existing != artifact {
                return Err(Status::data_loss(
                    "artifact hash path contains different bytes",
                ));
            }
        }
        Err(_) => return Err(Status::unavailable("WIR artifact could not be published")),
    }
    Ok(artifact_id)
}

fn load_wir_signer() -> Result<Option<Ed25519Signer>, Box<dyn Error>> {
    let Ok(path) = env::var("BPMP_COMPILER_WIR_SIGNING_KEY") else {
        return Ok(None);
    };
    let bytes = fs::read(path)?;
    let key: [u8; 32] = bytes
        .try_into()
        .map_err(|_| "WIR signing key must contain exactly 32 bytes")?;
    Ok(Some(Ed25519Signer::from_bytes(&key)))
}

fn required_path(name: &str) -> Result<String, Box<dyn Error>> {
    env::var(name).map_err(|_| format!("{name} must be configured").into())
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let address: SocketAddr = env::var("BPMP_COMPILER_BIND")
        .unwrap_or_else(|_| "0.0.0.0:7443".into())
        .parse()?;
    let certificate = fs::read(required_path("BPMP_COMPILER_TLS_CERT")?)?;
    let private_key = fs::read(required_path("BPMP_COMPILER_TLS_KEY")?)?;
    let client_ca = fs::read(required_path("BPMP_COMPILER_CLIENT_CA")?)?;
    let authorized_clients = required_path("BPMP_COMPILER_AUTHORIZED_CLIENT_CERTIFICATE_SHA256")?;
    let authorized_clients = authorized_clients
        .split(',')
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect::<Vec<_>>();
    let workload_authorization = WorkloadAuthorization::try_new(
        &authorized_clients,
        &[
            "/bpmp.compiler.v1.BpmnCompilerService/ValidateBpmn".into(),
            "/bpmp.compiler.v1.BpmnCompilerService/ValidateDmn".into(),
            "/bpmp.compiler.v1.BpmnCompilerService/CompileBpmn".into(),
            "/bpmp.compiler.v1.BpmnCompilerService/GetWIRArtifact".into(),
        ],
    )?;
    let admission_limiter = AdmissionLimiter::try_new(
        env::var("BPMP_COMPILER_ADMISSION_RATE_RPS")
            .unwrap_or_else(|_| "100".into())
            .parse()?,
        env::var("BPMP_COMPILER_ADMISSION_BURST")
            .unwrap_or_else(|_| "200".into())
            .parse()?,
    )?;
    let max_input_bytes = env::var("BPMP_COMPILER_MAX_INPUT_BYTES")
        .unwrap_or_else(|_| "1048576".into())
        .parse::<usize>()?;
    if max_input_bytes > (1 << 20) {
        return Err("BPMP_COMPILER_MAX_INPUT_BYTES cannot exceed 1 MiB".into());
    }
    let max_xml_depth = env::var("BPMP_COMPILER_MAX_XML_DEPTH")
        .unwrap_or_else(|_| "128".into())
        .parse::<u32>()?;
    let limits = CompilerLimits::new(max_input_bytes, max_xml_depth)?;
    let signer = load_wir_signer()?;
    let artifact_directory = env::var_os("BPMP_COMPILER_ARTIFACT_DIRECTORY").map(PathBuf::from);
    let tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(certificate, private_key))
        .client_ca_root(tonic::transport::Certificate::from_pem(client_ca));
    Server::builder()
        .layer(
            RequestMetadataLayer::new("bpmn-compiler")
                .with_request_timeout(std::time::Duration::from_secs(30))
                .with_workload_authorization(workload_authorization)
                .with_admission_limiter(admission_limiter),
        )
        .tls_config(tls)?
        .add_service(
            BpmnCompilerServiceServer::new(CompilerService {
                compiler: BpmnCompiler::new(limits),
                signer,
                max_input_bytes,
                artifact_directory,
            })
            .max_decoding_message_size((4 << 20) + (64 << 10))
            .max_encoding_message_size((8 << 20) + (64 << 10)),
        )
        .serve(address)
        .await?;
    Ok(())
}
