fn main() -> Result<(), Box<dyn std::error::Error>> {
    let root = "../../contracts/proto";
    let protos = [
        format!("{root}/bpmp/configuration/v1/configuration.proto"),
        format!("{root}/bpmp/engine/v1/engine.proto"),
        format!("{root}/bpmp/storage/v1/storage.proto"),
        format!("{root}/bpmp/wir/v1/wir.proto"),
    ];
    let mut config = prost_build::Config::new();
    config.protoc_executable(protoc_bin_vendored::protoc_bin_path()?);
    config.extern_path(
        ".bpmp.authorization.v1",
        "::bpmp_authz_contracts::authorization::v1",
    );
    config.compile_protos(&protos, &[root])?;
    for proto in protos {
        println!("cargo:rerun-if-changed={proto}");
    }
    Ok(())
}
