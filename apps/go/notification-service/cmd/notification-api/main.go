package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/bootstrap"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/grpcapi"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/httpapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	httpListen := flag.String("http-listen", "0.0.0.0:8089", "HTTPS BFF-facing listen address")
	grpcListen := flag.String("grpc-listen", "0.0.0.0:9089", "gRPC mTLS listen address")
	flag.Parse()

	if err := run(*httpListen, *grpcListen); err != nil {
		slog.Error("notification-api stopped", "error", err)
		os.Exit(1)
	}
}

func run(httpAddr, grpcAddr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap.InitRuntime(ctx, true)
	if err != nil {
		return err
	}
	defer rt.Close()

	tlsCfg, err := loadMTLSConfig()
	if err != nil {
		return err
	}

	httpHandler, err := httpapi.NewHandler(rt.Service, rt.ProofVerifier)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           httpHandler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         tlsCfg,
	}

	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	grpcSrv, err := grpcapi.NewServer(rt.Service, rt.ProofVerifier)
	if err != nil {
		return err
	}
	grpcSrv.Register(grpcServer)

	grpcListener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}
	defer grpcListener.Close()

	httpListener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return err
	}
	defer httpListener.Close()

	errs := make(chan error, 2)
	go func() { errs <- grpcServer.Serve(grpcListener) }()
	go func() { errs <- httpServer.ServeTLS(httpListener, "", "") }()

	slog.Info("notification-api started", "http", httpAddr, "grpc", grpcAddr)
	select {
	case <-ctx.Done():
	case serveErr := <-errs:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			err = serveErr
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grpcServer.GracefulStop()
	return errors.Join(err, httpServer.Shutdown(shutdownCtx))
}

func loadMTLSConfig() (*tls.Config, error) {
	certPath := os.Getenv("NOTIFICATION_SERVER_CERTIFICATE")
	keyPath := os.Getenv("NOTIFICATION_SERVER_PRIVATE_KEY")
	caPath := os.Getenv("NOTIFICATION_CLIENT_CA")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, errors.New("NOTIFICATION_SERVER_CERTIFICATE / NOTIFICATION_SERVER_PRIVATE_KEY invalid")
	}
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, errors.New("NOTIFICATION_CLIENT_CA is unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("NOTIFICATION_CLIENT_CA PEM is invalid")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	}, nil
}
