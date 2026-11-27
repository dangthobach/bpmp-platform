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

	"github.com/dangthobach/bpmp-platform/apps/go/integration-hub/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/integration-hub/internal/httpapi"
	"github.com/dangthobach/bpmp-platform/apps/go/integration-hub/internal/postgres"
	"github.com/dangthobach/bpmp-platform/go/platform/odsruntime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	hosts := splitNonEmpty(envOr("INTEGRATION_ALLOWED_EGRESS_HOSTS", "esb.bank.example,aml.bank.example"))
	svc := application.NewService(application.Config{
		AllowedEgressHosts: hosts,
	})

	var pgStore *postgres.Store
	if dsn := strings.TrimSpace(os.Getenv("INTEGRATION_POSTGRES_DSN")); dsn != "" {
		var err error
		pgStore, err = postgres.Open(ctx, dsn)
		if err != nil {
			logger.Error("integration-hub postgres init failed", "error", err)
			os.Exit(1)
		}
		defer pgStore.Close()
		svc.SetOutboxSink(pgStore.AppendOutbox)
		logger.Info("integration-hub connected to PostgreSQL")

		if brokers := splitNonEmpty(os.Getenv("INTEGRATION_KAFKA_BROKERS")); len(brokers) > 0 {
			pub, pubErr := odsruntime.NewKafkaPublisher(brokers, "integration-hub-outbox", 5*time.Second)
			if pubErr != nil {
				logger.Error("integration-hub kafka publisher init failed", "error", pubErr)
				os.Exit(1)
			}
			defer pub.Close()
			topic := envOr("INTEGRATION_KAFKA_TOPIC", "bpmp.integration.events.v1.e2e")
			go odsruntime.RunOutboxRelay(ctx, logger, pgStore, pub, odsruntime.RelayConfig{
				Topic:        topic,
				BatchSize:    50,
				PollInterval: 500 * time.Millisecond,
			})
			logger.Info("integration-hub Kafka outbox relay started", "topic", topic)
		}
	}

	baseHandler := httpapi.NewHandler(svc)
	verifier, err := odsruntime.LoadActorVerifier(
		os.Getenv("INTEGRATION_ACTOR_PROOF_KEY_ID"),
		envOr("INTEGRATION_ACTOR_WORKLOAD_ID", "integration-hub"),
		os.Getenv("INTEGRATION_ACTOR_PROOF_PUBLIC_KEY"),
	)
	if err != nil {
		logger.Error("integration-hub actor proof verifier init failed", "error", err)
		os.Exit(1)
	}
	metrics := odsruntime.NewMetricsCollector("integration-hub", nil)
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
		ListenAddress:  envOr("INTEGRATION_HUB_ADDR", "0.0.0.0:8088"),
		ServerCertFile: os.Getenv("INTEGRATION_SERVER_CERTIFICATE"),
		ServerKeyFile:  os.Getenv("INTEGRATION_SERVER_PRIVATE_KEY"),
		ClientCAFile:   os.Getenv("INTEGRATION_CLIENT_CA"),
	}, handler)
	if err != nil {
		logger.Error("integration-hub stopped with error", "error", err)
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
