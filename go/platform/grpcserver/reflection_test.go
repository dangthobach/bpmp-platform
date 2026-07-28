package grpcserver

import (
	"testing"

	"google.golang.org/grpc"
)

func TestRegisterReflectionHonorsConfiguration(t *testing.T) {
	t.Parallel()
	for name, enabled := range map[string]bool{"enabled": true, "disabled": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := grpc.NewServer()
			RegisterReflection(server, enabled)
			_, registeredV1 := server.GetServiceInfo()["grpc.reflection.v1.ServerReflection"]
			_, registeredV1Alpha := server.GetServiceInfo()["grpc.reflection.v1alpha.ServerReflection"]
			if registeredV1 != enabled || registeredV1Alpha != enabled {
				t.Fatalf("reflection registration mismatch: v1=%v v1alpha=%v", registeredV1, registeredV1Alpha)
			}
		})
	}
}
