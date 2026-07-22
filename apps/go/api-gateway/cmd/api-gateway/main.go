package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/config"
	"github.com/dangthobach/bpmp-platform/apps/go/api-gateway/internal/gateway"
	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
	humanv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/human/v1"
)

func main() {
	path := flag.String("config", os.Getenv("API_GATEWAY_CONFIG"), "path to API gateway JSON configuration")
	flag.Parse()
	if *path == "" {
		slog.Error("configuration path is required")
		os.Exit(2)
	}
	if err := run(*path); err != nil {
		slog.Error("api-gateway stopped", "error", err)
		os.Exit(1)
	}
}
func run(path string) error {
	value, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	clientCertificate, err := tls.LoadX509KeyPair(value.UpstreamTLS.Certificate, value.UpstreamTLS.PrivateKey)
	if err != nil {
		return err
	}
	roots, err := certPool(value.UpstreamTLS.CA)
	if err != nil {
		return err
	}
	engineConn, err := grpc.NewClient(value.EngineAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.EngineServerName, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}})), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes)))
	if err != nil {
		return err
	}
	defer engineConn.Close()
	humanConn, err := grpc.NewClient(value.HumanAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: value.UpstreamTLS.HumanServerName, RootCAs: roots, Certificates: []tls.Certificate{clientCertificate}})), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(value.GRPC.MaxReceiveBytes), grpc.MaxCallSendMsgSize(value.GRPC.MaxSendBytes)))
	if err != nil {
		return err
	}
	defer humanConn.Close()
	handler, err := gateway.New(enginev1.NewEngineCommandServiceClient(engineConn), humanv1.NewHumanRuntimeServiceClient(humanConn), value)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: value.ListenAddress, Handler: handler.Routes(), ReadHeaderTimeout: value.HTTP.ReadHeaderTimeout(), ReadTimeout: value.HTTP.ReadTimeout(), WriteTimeout: value.HTTP.WriteTimeout(), IdleTimeout: value.HTTP.IdleTimeout()}
	errorsChannel := make(chan error, 1)
	go func() {
		errorsChannel <- server.ListenAndServeTLS(value.PublicTLS.Certificate, value.PublicTLS.PrivateKey)
	}()
	slog.Info("api-gateway started", "listen_address", value.ListenAddress)
	select {
	case <-ctx.Done():
		return server.Shutdown(context.Background())
	case runErr := <-errorsChannel:
		if errors.Is(runErr, http.ErrServerClosed) {
			return nil
		}
		return runErr
	}
}
func certPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("upstream CA contains no certificates")
	}
	return pool, nil
}
