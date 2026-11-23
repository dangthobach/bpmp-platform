module github.com/dangthobach/bpmp-platform/apps/go/case-service

go 1.25.12

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/twmb/franz-go/pkg/kmsg v1.13.1
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
)

require (
	github.com/dangthobach/bpmp-platform/go/contracts v0.0.0
	github.com/dangthobach/bpmp-platform/go/platform v0.0.0
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/twmb/franz-go v1.21.0
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.39.0 // indirect
)

replace github.com/dangthobach/bpmp-platform/go/platform => ../../../go/platform

replace github.com/dangthobach/bpmp-platform/go/contracts => ../../../go/contracts
