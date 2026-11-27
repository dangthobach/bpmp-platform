package odsruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// ServerConfig configures a bounded, production-hardened HTTP/mTLS server.
type ServerConfig struct {
	ListenAddress   string
	ServerCertFile  string
	ServerKeyFile   string
	ClientCAFile    string
	ReadHeaderTime  time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxHeaderBytes  int
}

func (c *ServerConfig) applyDefaults() {
	if c.ReadHeaderTime <= 0 {
		c.ReadHeaderTime = 5 * time.Second
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = 15 * time.Second
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 15 * time.Second
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 60 * time.Second
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = 10 * time.Second
	}
	if c.MaxHeaderBytes <= 0 {
		c.MaxHeaderBytes = 32 * 1024
	}
}

// BuildMTLSConfig constructs a strict TLS 1.3 mutual-TLS server config when cert/key/CA are provided.
// Returns (nil, nil) if all three paths are empty (explicit local plaintext mode).
func BuildMTLSConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" && clientCAFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" || clientCAFile == "" {
		return nil, errors.New("mTLS requires all three of server cert, server key, and client CA")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server keypair: %w", err)
	}
	caPEM, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("client CA file contains no valid PEM certificates")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

// Serve runs the HTTP/mTLS server with bounded timeouts and graceful shutdown on ctx cancellation.
func Serve(ctx context.Context, logger *slog.Logger, cfg ServerConfig, handler http.Handler) error {
	cfg.applyDefaults()
	if cfg.ListenAddress == "" {
		return errors.New("listen address is required")
	}
	tlsCfg, err := BuildMTLSConfig(cfg.ServerCertFile, cfg.ServerKeyFile, cfg.ClientCAFile)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: cfg.ReadHeaderTime,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}

	ln, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddress, err)
	}

	errCh := make(chan error, 1)
	go func() {
		if tlsCfg != nil {
			logger.Info("service listening with TLS 1.3 mTLS", "addr", cfg.ListenAddress)
			errCh <- srv.Serve(tls.NewListener(ln, tlsCfg))
		} else {
			logger.Info("service listening (plaintext local mode)", "addr", cfg.ListenAddress)
			errCh <- srv.Serve(ln)
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
