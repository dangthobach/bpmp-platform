package servermiddleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UnaryAuthorizer func(context.Context, string, any) error
type UnaryRateLimiter func(context.Context, string, any) error
type UnaryValidator func(context.Context, string, any) error
type StreamAuthorizer func(context.Context, string) error
type StreamRateLimiter func(context.Context, string) error

type GRPCConfig struct {
	Service         string
	UnaryTimeout    time.Duration
	StreamTimeout   time.Duration
	AuthorizeUnary  UnaryAuthorizer
	RateLimitUnary  UnaryRateLimiter
	ValidateUnary   UnaryValidator
	AuthorizeStream StreamAuthorizer
	RateLimitStream StreamRateLimiter
}

func (c GRPCConfig) Validate() error {
	if c.Service == "" {
		return errors.New("gRPC middleware service name is required")
	}
	if c.UnaryTimeout < 0 || c.StreamTimeout < 0 {
		return errors.New("gRPC middleware timeouts cannot be negative")
	}
	return nil
}

func UnaryServerInterceptor(config GRPCConfig) (grpc.UnaryServerInterceptor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	metadata := requestmeta.UnaryServerInterceptor(config.Service)
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return metadata(ctx, request, info, func(ctx context.Context, request any) (response any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.ErrorContext(ctx, "gRPC handler panic recovered",
						append(requestmeta.SlogAttrs(ctx), slog.String("rpc_method", info.FullMethod),
							slog.String("panic_type", fmt.Sprintf("%T", recovered)))...)
					response = nil
					err = status.Error(codes.Internal, "internal server error")
				}
			}()
			if config.UnaryTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = withBoundedTimeout(ctx, config.UnaryTimeout)
				defer cancel()
			}
			if config.AuthorizeUnary != nil {
				if err = config.AuthorizeUnary(ctx, info.FullMethod, request); err != nil {
					return nil, canonicalGRPCError(err, codes.PermissionDenied, "request is not authorized")
				}
			}
			if config.RateLimitUnary != nil {
				if err = config.RateLimitUnary(ctx, info.FullMethod, request); err != nil {
					return nil, canonicalGRPCError(err, codes.ResourceExhausted, "rate limit exceeded")
				}
			}
			if config.ValidateUnary != nil {
				if err = config.ValidateUnary(ctx, info.FullMethod, request); err != nil {
					return nil, canonicalGRPCError(err, codes.InvalidArgument, "request validation failed")
				}
			}
			return handler(ctx, request)
		})
	}, nil
}

func StreamServerInterceptor(config GRPCConfig) (grpc.StreamServerInterceptor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	metadata := requestmeta.StreamServerInterceptor(config.Service)
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		return metadata(server, stream, info, func(server any, stream grpc.ServerStream) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.ErrorContext(stream.Context(), "gRPC stream panic recovered",
						append(requestmeta.SlogAttrs(stream.Context()), slog.String("rpc_method", info.FullMethod),
							slog.String("panic_type", fmt.Sprintf("%T", recovered)))...)
					err = status.Error(codes.Internal, "internal server error")
				}
			}()
			ctx := stream.Context()
			if config.StreamTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = withBoundedTimeout(ctx, config.StreamTimeout)
				defer cancel()
				stream = &serverStreamContext{ServerStream: stream, ctx: ctx}
			}
			if config.AuthorizeStream != nil {
				if err = config.AuthorizeStream(ctx, info.FullMethod); err != nil {
					return canonicalGRPCError(err, codes.PermissionDenied, "request is not authorized")
				}
			}
			if config.RateLimitStream != nil {
				if err = config.RateLimitStream(ctx, info.FullMethod); err != nil {
					return canonicalGRPCError(err, codes.ResourceExhausted, "rate limit exceeded")
				}
			}
			return handler(server, stream)
		})
	}, nil
}

func withBoundedTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func canonicalGRPCError(err error, fallback codes.Code, message string) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(fallback, message)
}

type serverStreamContext struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *serverStreamContext) Context() context.Context { return s.ctx }
