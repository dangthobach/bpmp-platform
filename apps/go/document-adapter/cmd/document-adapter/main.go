package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/document-adapter/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/document-adapter/internal/httpapi"
	"github.com/dangthobach/bpmp-platform/apps/go/document-adapter/internal/postgres"
	"github.com/dangthobach/bpmp-platform/go/platform/odsruntime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc, err := application.NewService(application.Config{
		CSPEndpoint:      envOr("DOCUMENT_CSP_ENDPOINT", "https://csp.internal.local"),
		URLSigningSecret: []byte(envOr("DOCUMENT_URL_SIGNING_SECRET", "local-dev-document-signing-key-32bytes")),
		CallbackSecrets: map[string][][]byte{
			"clamav": {[]byte(envOr("DOCUMENT_SCAN_CALLBACK_SECRET", "local-dev-av-callback-secret-32bytes"))},
		},
	})
	if err != nil {
		logger.Error("document-adapter init failed", "error", err)
		os.Exit(1)
	}

	var pgStore *postgres.Store
	if dsn := strings.TrimSpace(os.Getenv("DOCUMENT_POSTGRES_DSN")); dsn != "" {
		pgStore, err = postgres.Open(ctx, dsn)
		if err != nil {
			logger.Error("document-adapter postgres init failed", "error", err)
			os.Exit(1)
		}
		defer pgStore.Close()
		svc.SetOutboxSink(pgStore.AppendOutbox)
		logger.Info("document-adapter connected to PostgreSQL")

		if brokers := splitNonEmpty(os.Getenv("DOCUMENT_KAFKA_BROKERS")); len(brokers) > 0 {
			pub, pubErr := odsruntime.NewKafkaPublisher(brokers, "document-adapter-outbox", 5*time.Second)
			if pubErr != nil {
				logger.Error("document-adapter kafka publisher init failed", "error", pubErr)
				os.Exit(1)
			}
			defer pub.Close()
			topic := envOr("DOCUMENT_KAFKA_TOPIC", "bpmp.document.events.v1.e2e")
			go odsruntime.RunOutboxRelay(ctx, logger, pgStore, pub, odsruntime.RelayConfig{
				Topic:        topic,
				BatchSize:    50,
				PollInterval: 500 * time.Millisecond,
			})
			logger.Info("document-adapter Kafka outbox relay started", "topic", topic)
		}
	}

	baseHandler := httpapi.NewHandler(svc)
	verifier, err := odsruntime.LoadActorVerifier(
		os.Getenv("DOCUMENT_ACTOR_PROOF_KEY_ID"),
		envOr("DOCUMENT_ACTOR_WORKLOAD_ID", "document-adapter"),
		os.Getenv("DOCUMENT_ACTOR_PROOF_PUBLIC_KEY"),
	)
	if err != nil {
		logger.Error("document-adapter actor proof verifier init failed", "error", err)
		os.Exit(1)
	}
	metrics := odsruntime.NewMetricsCollector("document-adapter", nil)
	handler := metrics.Middleware(odsruntime.ActorProofMiddleware(verifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && pgStore != nil {
			if err := pgStore.Ping(r.Context()); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"degraded","dependency":"postgres"}`))
				return
			}
		}
		baseHandler.ServeHTTP(w, r)
	})))

	err = odsruntime.Serve(ctx, logger, odsruntime.ServerConfig{
		ListenAddress:  envOr("DOCUMENT_ADAPTER_ADDR", "0.0.0.0:8084"),
		ServerCertFile: os.Getenv("DOCUMENT_SERVER_CERTIFICATE"),
		ServerKeyFile:  os.Getenv("DOCUMENT_SERVER_PRIVATE_KEY"),
		ClientCAFile:   os.Getenv("DOCUMENT_CLIENT_CA"),
	}, handler)
	if err != nil {
		logger.Error("document-adapter stopped with error", "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitNonEmpty(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}
