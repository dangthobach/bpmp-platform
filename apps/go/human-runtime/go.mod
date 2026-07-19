module github.com/dangthobach/bpmp-platform/apps/go/human-runtime

go 1.25.0

replace github.com/dangthobach/bpmp-platform/go/contracts => ../../../go/contracts

require github.com/jackc/pgx/v5 v5.7.6

require (
	github.com/dangthobach/bpmp-platform/go/contracts v0.0.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/twmb/franz-go v1.21.0
	google.golang.org/grpc v1.76.0
	google.golang.org/protobuf v1.36.10
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.13.1 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250804133106-a7a43d27e69b // indirect
)
