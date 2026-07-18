fn main() -> Result<(), Box<dyn std::error::Error>> {
    let root = "../../contracts/proto";
    let proto = format!("{root}/bpmp/authorization/v1/authorization.proto");
    let mut config = prost_build::Config::new();
    config.protoc_executable(protoc_bin_vendored::protoc_bin_path()?);
    config.compile_protos(&[&proto], &[root])?;
    println!("cargo:rerun-if-changed={proto}");
    Ok(())
}
