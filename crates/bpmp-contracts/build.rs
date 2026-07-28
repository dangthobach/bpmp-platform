fn main() -> Result<(), Box<dyn std::error::Error>> {
    let out_dir = std::path::PathBuf::from(std::env::var("OUT_DIR")?);
    let root = "../../contracts/proto";
    let protoc_path = protoc_bin_vendored::protoc_bin_path()?;
    let proto_files = [
        format!("{root}/bpmp/configuration/v1/configuration.proto"),
        format!("{root}/bpmp/engine/v1/engine.proto"),
        format!("{root}/bpmp/governance/v1/governance.proto"),
        format!("{root}/bpmp/raft/v1/raft.proto"),
        format!("{root}/bpmp/storage/v1/storage.proto"),
        format!("{root}/bpmp/wir/v1/wir.proto"),
    ];
    let public_protos = [
        &proto_files[0],
        &proto_files[1],
        &proto_files[2],
        &proto_files[4],
        &proto_files[5],
    ];
    let mut descriptor_command = std::process::Command::new(&protoc_path);
    descriptor_command
        .arg("--include_imports")
        .arg(format!("--proto_path={root}"))
        .arg(format!(
            "--descriptor_set_out={}",
            out_dir.join("bpmp_public_descriptor.bin").display()
        ))
        .args(public_protos);
    if !descriptor_command.status()?.success() {
        return Err("generate public Protobuf descriptor set".into());
    }
    let mut config = prost_build::Config::new();
    config.protoc_executable(protoc_path);
    config.extern_path(
        ".bpmp.authorization.v1",
        "::bpmp_authz_contracts::authorization::v1",
    );
    tonic_prost_build::configure()
        .build_client(true)
        .build_server(true)
        .compile_with_config(config, &proto_files, &[root.to_owned()])?;
    for proto in proto_files {
        println!("cargo:rerun-if-changed={proto}");
    }
    Ok(())
}
