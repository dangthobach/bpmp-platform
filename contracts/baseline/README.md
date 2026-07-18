# Protobuf compatibility baseline

`v1.binpb` is the reviewed Buf image for the current durable v1 contracts. CI
compares the working schema against this image with:

```powershell
buf breaking --against contracts/baseline/v1.binpb
```

Replace the baseline only as part of a reviewed compatible contract release.
Wire-breaking changes require a new Protobuf package version.
