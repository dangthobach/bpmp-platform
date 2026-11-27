module github.com/dangthobach/bpmp-platform/apps/go/notification-service

go 1.27.3

require (
	github.com/dangthobach/bpmp-platform/go/contracts v0.0.0
	github.com/dangthobach/bpmp-platform/go/platform v0.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/redis/go-redis/v9 v9.17.2
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
	github.com/twmb/franz-go v1.21.0
	golang.org/x/net v0.56.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

replace github.com/dangthobach/bpmp-platform/go/platform => ../../../go/platform

replace github.com/dangthobach/bpmp-platform/go/contracts => ../../../go/contracts
