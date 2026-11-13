use std::{env, error::Error, fs, net::SocketAddr};

use bpmp_contracts::compiler::v1::{
    Diagnostic, ValidateBpmnRequest, ValidateBpmnResponse, ValidateDmnRequest, ValidateDmnResponse,
    bpmn_compiler_service_server::{BpmnCompilerService, BpmnCompilerServiceServer},
};
use bpmn_compiler::{BpmnCompiler, CompilerLimits, SourceDocument};
use tonic::{Request, Response, Status, transport::{Identity, Server, ServerTlsConfig}};

struct CompilerService {
    compiler: BpmnCompiler,
}

#[tonic::async_trait]
impl BpmnCompilerService for CompilerService {
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
            return Err(Status::invalid_argument("tenant, model, revision and source name are required"));
        }
        let source = SourceDocument { name: &request.source_name, bytes: &request.bpmn_xml };
        let result = self.compiler.compile(
            source,
            &request.tenant_id,
            &format!("{}-r{}", request.process_model_id, request.draft_revision),
        );
        let response = match result {
            Ok(_) => ValidateBpmnResponse { valid: true, diagnostics: Vec::new() },
            Err(diagnostics) => ValidateBpmnResponse {
                valid: false,
                diagnostics: diagnostics.into_iter().map(|diagnostic| Diagnostic {
                    code: "BPMN_VALIDATION_ERROR".into(),
                    severity: "ERROR".into(),
                    message: diagnostic.kind.to_string(),
                    element_id: String::new(),
                    line: u64::from(diagnostic.span.line),
                    column: u64::from(diagnostic.span.column),
                }).collect(),
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
            return Err(Status::invalid_argument("tenant, decision model, revision and source name are required"));
        }
        let source = SourceDocument { name: &request.source_name, bytes: &request.dmn_xml };
        let result = self.compiler.validate_dmn(source);
        let response = match result {
            Ok(()) => ValidateDmnResponse { valid: true, diagnostics: Vec::new() },
            Err(diagnostics) => ValidateDmnResponse {
                valid: false,
                diagnostics: diagnostics.into_iter().map(|diagnostic| Diagnostic {
                    code: "DMN_VALIDATION_ERROR".into(),
                    severity: "ERROR".into(),
                    message: diagnostic.kind.to_string(),
                    element_id: String::new(),
                    line: u64::from(diagnostic.span.line),
                    column: u64::from(diagnostic.span.column),
                }).collect(),
            },
        };
        Ok(Response::new(response))
    }
}

fn required_path(name: &str) -> Result<String, Box<dyn Error>> {
    env::var(name).map_err(|_| format!("{name} must be configured").into())
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let address: SocketAddr = env::var("BPMP_COMPILER_BIND")
        .unwrap_or_else(|_| "0.0.0.0:7443".into()).parse()?;
    let certificate = fs::read(required_path("BPMP_COMPILER_TLS_CERT")?)?;
    let private_key = fs::read(required_path("BPMP_COMPILER_TLS_KEY")?)?;
    let client_ca = fs::read(required_path("BPMP_COMPILER_CLIENT_CA")?)?;
    let max_input_bytes = env::var("BPMP_COMPILER_MAX_INPUT_BYTES")
        .unwrap_or_else(|_| "1048576".into()).parse::<usize>()?;
    if max_input_bytes > (1 << 20) {
        return Err("BPMP_COMPILER_MAX_INPUT_BYTES cannot exceed 1 MiB".into());
    }
    let max_xml_depth = env::var("BPMP_COMPILER_MAX_XML_DEPTH")
        .unwrap_or_else(|_| "128".into()).parse::<u32>()?;
    let limits = CompilerLimits::new(max_input_bytes, max_xml_depth)?;
    let tls = ServerTlsConfig::new()
        .identity(Identity::from_pem(certificate, private_key))
        .client_ca_root(tonic::transport::Certificate::from_pem(client_ca));
    Server::builder().tls_config(tls)?
        .add_service(BpmnCompilerServiceServer::new(CompilerService { compiler: BpmnCompiler::new(limits) })
            .max_decoding_message_size((1 << 20) + (64 << 10)))
        .serve(address).await?;
    Ok(())
}
