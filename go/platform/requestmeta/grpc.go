package requestmeta

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, reply any, connection *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
		ctx = outgoingFromCurrent(ctx)
		return invoke(ctx, method, request, reply, connection, options...)
	}
}

func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, description *grpc.StreamDesc, connection *grpc.ClientConn, method string, streamer grpc.Streamer, options ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(outgoingFromCurrent(ctx), description, connection, method, options...)
	}
}

func UnaryServerInterceptor(service string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()
		ctx = WithValues(ctx, ValuesFromIncomingContext(ctx))
		values, _ := FromContext(ctx)
		_ = grpc.SetHeader(ctx, responseMetadata(values))
		var err error
		defer func() { logGRPC(ctx, service, info.FullMethod, time.Since(started), err) }()
		var response any
		response, err = handler(ctx, request)
		return response, err
	}
}

func StreamServerInterceptor(service string) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		started := time.Now()
		ctx := WithValues(stream.Context(), ValuesFromIncomingContext(stream.Context()))
		values, _ := FromContext(ctx)
		_ = stream.SetHeader(responseMetadata(values))
		var err error
		defer func() { logGRPC(ctx, service, info.FullMethod, time.Since(started), err) }()
		err = handler(server, &contextServerStream{ServerStream: stream, ctx: ctx})
		return err
	}
}

func outgoingFromCurrent(ctx context.Context) context.Context {
	ctx = Ensure(ctx)
	values, _ := FromContext(ctx)
	return OutgoingContext(ctx, values)
}

func responseMetadata(values Values) metadata.MD {
	pairs := []string{RequestID, values.RequestID, CorrelationID, values.CorrelationID}
	if values.TraceID != "" {
		pairs = append(pairs, TraceID, values.TraceID)
	}
	return metadata.Pairs(pairs...)
}

func logGRPC(ctx context.Context, service, method string, elapsed time.Duration, err error) {
	attrs := SlogAttrs(ctx)
	attrs = append(attrs,
		slog.String("service", service),
		slog.String("transport", "grpc"),
		slog.String("rpc_method", method),
		slog.String("grpc_code", grpcCode(err).String()),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	)
	if err != nil {
		slog.WarnContext(ctx, "gRPC request completed", attrs...)
		return
	}
	slog.InfoContext(ctx, "gRPC request completed", attrs...)
}

func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return status.Code(err)
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextServerStream) Context() context.Context { return s.ctx }
