module github.com/dangthobach/bpmp-platform/apps/go/api-gateway

go 1.25.0

replace github.com/dangthobach/bpmp-platform/go/contracts => ../../../go/contracts

require (
	github.com/dangthobach/bpmp-platform/go/contracts v0.0.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	google.golang.org/grpc v1.76.0
	google.golang.org/protobuf v1.36.10
)

require (
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250804133106-a7a43d27e69b // indirect
)
