module github.com/dangthobach/bpmp-platform/apps/go/authoring-service

go 1.25.12

replace github.com/dangthobach/bpmp-platform/go/contracts => ../../../go/contracts

require (
	github.com/dangthobach/bpmp-platform/go/contracts v0.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/twmb/franz-go v1.21.0
	google.golang.org/grpc v1.82.1
)
