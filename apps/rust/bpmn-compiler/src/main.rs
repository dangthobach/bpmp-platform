use std::fs::{self, File};
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use bpmn_compiler::{BpmnCompiler, CompilerLimits, SourceDocument};
use bpmp_contracts::{Ed25519Signer, WirCodec};
use clap::{ArgAction, Parser};
use tempfile::NamedTempFile;

#[derive(Debug, Parser)]
#[command(
    name = "bpmn-compiler",
    version,
    about = "Compile BPMN into signed BPMP WIR"
)]
struct Arguments {
    #[arg(long)]
    input: PathBuf,
    #[arg(long, action = ArgAction::Append)]
    dmn: Vec<PathBuf>,
    #[arg(long, action = ArgAction::Append)]
    cmmn: Vec<PathBuf>,
    #[arg(long)]
    tenant_id: String,
    #[arg(long)]
    output: PathBuf,
    #[arg(long)]
    rust_output: Option<PathBuf>,
    #[arg(long)]
    workflow_version: String,
    #[arg(long)]
    signing_key: PathBuf,
    #[arg(long)]
    max_input_bytes: usize,
    #[arg(long)]
    max_xml_depth: u32,
}

fn main() -> ExitCode {
    let arguments = Arguments::parse();
    match run(&arguments) {
        Ok(()) => ExitCode::SUCCESS,
        Err(CliError::Compile(diagnostics)) => {
            for diagnostic in diagnostics {
                eprintln!("{diagnostic}");
            }
            ExitCode::from(1)
        }
        Err(error) => {
            eprintln!("error: {error}");
            ExitCode::from(2)
        }
    }
}

fn run(arguments: &Arguments) -> Result<(), CliError> {
    let limits = CompilerLimits::new(arguments.max_input_bytes, arguments.max_xml_depth)
        .map_err(|error| CliError::Configuration(error.to_string()))?;
    let input = read_bounded(&arguments.input, arguments.max_input_bytes)?;
    let dmn_inputs = arguments
        .dmn
        .iter()
        .map(|path| {
            read_bounded(path, arguments.max_input_bytes)
                .map(|bytes| (path.to_string_lossy().into_owned(), bytes))
        })
        .collect::<Result<Vec<_>, _>>()?;
    let cmmn_inputs = arguments
        .cmmn
        .iter()
        .map(|path| {
            read_bounded(path, arguments.max_input_bytes)
                .map(|bytes| (path.to_string_lossy().into_owned(), bytes))
        })
        .collect::<Result<Vec<_>, _>>()?;
    let signing_key = read_exact_key(&arguments.signing_key)?;
    let dmn_sources = dmn_inputs
        .iter()
        .map(|(name, bytes)| SourceDocument { name, bytes })
        .collect::<Vec<_>>();
    let cmmn_sources = cmmn_inputs
        .iter()
        .map(|(name, bytes)| SourceDocument { name, bytes })
        .collect::<Vec<_>>();
    let compiler = BpmnCompiler::new(limits);
    let wir = compiler
        .compile_with_models(
            SourceDocument {
                name: &arguments.input.to_string_lossy(),
                bytes: &input,
            },
            &dmn_sources,
            &cmmn_sources,
            &arguments.tenant_id,
            &arguments.workflow_version,
        )
        .map_err(CliError::Compile)?;
    if let Some(rust_output) = &arguments.rust_output {
        let generated = compiler
            .generate_rust(&wir)
            .map_err(|error| CliError::Codegen(error.to_string()))?;
        write_atomically(rust_output, generated.as_bytes())?;
    }
    let artifact = WirCodec::seal(wir, &Ed25519Signer::from_bytes(&signing_key))
        .map_err(|error| CliError::Artifact(error.to_string()))?;
    write_atomically(&arguments.output, &artifact)
}

fn read_bounded(path: &Path, configured_limit: usize) -> Result<Vec<u8>, CliError> {
    let file = File::open(path).map_err(|source| CliError::Io {
        operation: "open input",
        path: path.to_owned(),
        source,
    })?;
    let read_limit = u64::try_from(configured_limit)
        .map_err(|_| CliError::Configuration("max_input_bytes does not fit u64".into()))?
        .checked_add(1)
        .ok_or_else(|| CliError::Configuration("max_input_bytes is too large".into()))?;
    let mut bytes = Vec::new();
    file.take(read_limit)
        .read_to_end(&mut bytes)
        .map_err(|source| CliError::Io {
            operation: "read input",
            path: path.to_owned(),
            source,
        })?;
    Ok(bytes)
}

fn read_exact_key(path: &Path) -> Result<[u8; 32], CliError> {
    let mut bytes = Vec::new();
    File::open(path)
        .and_then(|file| file.take(33).read_to_end(&mut bytes))
        .map_err(|source| CliError::Io {
            operation: "read signing key",
            path: path.to_owned(),
            source,
        })?;
    bytes.try_into().map_err(|_| CliError::SigningKeyLength)
}

fn write_atomically(path: &Path, artifact: &[u8]) -> Result<(), CliError> {
    let parent = path
        .parent()
        .filter(|parent| !parent.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    fs::create_dir_all(parent).map_err(|source| CliError::Io {
        operation: "create output directory",
        path: parent.to_owned(),
        source,
    })?;
    let mut temporary = NamedTempFile::new_in(parent).map_err(|source| CliError::Io {
        operation: "create temporary artifact",
        path: parent.to_owned(),
        source,
    })?;
    temporary
        .write_all(artifact)
        .map_err(|source| CliError::Io {
            operation: "write artifact",
            path: path.to_owned(),
            source,
        })?;
    temporary
        .as_file()
        .sync_all()
        .map_err(|source| CliError::Io {
            operation: "sync artifact",
            path: path.to_owned(),
            source,
        })?;
    temporary.persist(path).map_err(|error| CliError::Io {
        operation: "publish artifact",
        path: path.to_owned(),
        source: error.error,
    })?;
    Ok(())
}

#[derive(Debug, thiserror::Error)]
enum CliError {
    #[error("invalid compiler configuration: {0}")]
    Configuration(String),
    #[error("{operation} failed for {}: {source}", path.display())]
    Io {
        operation: &'static str,
        path: PathBuf,
        source: io::Error,
    },
    #[error("Ed25519 signing key must contain exactly 32 raw bytes")]
    SigningKeyLength,
    #[error("WIR artifact sealing failed: {0}")]
    Artifact(String),
    #[error("Rust state-machine generation failed: {0}")]
    Codegen(String),
    #[error("BPMN compilation failed")]
    Compile(Vec<bpmn_compiler::CompileDiagnostic>),
}
