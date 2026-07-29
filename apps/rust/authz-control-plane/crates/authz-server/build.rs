fn main() -> Result<(), Box<dyn std::error::Error>> {
    if let Ok(protoc_path) = protoc_bin_vendored::protoc_bin_path() {
        std::env::set_var("PROTOC", protoc_path);
    }
    tonic_build::compile_protos("proto/authz.proto")?;
    tonic_build::configure()
        .build_client(false)
        .build_server(false)
        .compile(
            &[
                "../../../../../contracts/proto/bpmp/configuration/v1/configuration.proto",
                "../../../../../contracts/proto/bpmp/tenancy/v1/tenancy.proto",
            ],
            &["../../../../../contracts/proto"],
        )?;
    Ok(())
}
