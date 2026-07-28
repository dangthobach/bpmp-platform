package grpcserver

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func RegisterReflection(server *grpc.Server, enabled bool) {
	if enabled {
		reflection.Register(server)
	}
}
