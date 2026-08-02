package servermiddleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUnaryPipelineRecoversAndPreservesCanonicalError(t *testing.T) {
	interceptor, err := UnaryServerInterceptor(GRPCConfig{Service: "test", UnaryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = interceptor(context.Background(), struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Panic"},
		func(context.Context, any) (any, error) { panic("sensitive panic") })
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "internal server error" {
		t.Fatalf("error = %v", err)
	}
}

func TestUnaryPipelineOrderDeadlineAndValidation(t *testing.T) {
	order := make([]string, 0, 4)
	interceptor, err := UnaryServerInterceptor(GRPCConfig{
		Service: "test", UnaryTimeout: time.Second,
		AuthorizeUnary: func(ctx context.Context, _ string, _ any) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("authorization has no deadline")
			}
			order = append(order, "security")
			return nil
		},
		RateLimitUnary: func(context.Context, string, any) error {
			order = append(order, "rate_limit")
			return nil
		},
		ValidateUnary: func(context.Context, string, any) error {
			order = append(order, "validation")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = interceptor(context.Background(), struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Call"},
		func(context.Context, any) (any, error) {
			order = append(order, "handler")
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"security", "rate_limit", "validation", "handler"}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("order = %v", order)
		}
	}
}

func TestUnaryPipelineFailsClosedAtGuard(t *testing.T) {
	interceptor, err := UnaryServerInterceptor(GRPCConfig{
		Service: "test",
		AuthorizeUnary: func(context.Context, string, any) error {
			return errors.New("sensitive verifier detail")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = interceptor(context.Background(), struct{}{},
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Call"},
		func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		})
	if called || status.Code(err) != codes.PermissionDenied ||
		status.Convert(err).Message() != "request is not authorized" {
		t.Fatalf("called = %v, error = %v", called, err)
	}
}
