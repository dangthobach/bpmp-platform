package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/audit-projection/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/audit-projection/internal/httpapi"
	"github.com/dangthobach/bpmp-platform/apps/go/audit-projection/internal/postgres"
	"github.com/dangthobach/bpmp-platform/go/platform/odsruntime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc := application.NewService(nil)

	var pgStore *postgres.Store
	if dsn := strings.TrimSpace(os.Getenv("AUDIT_POSTGRES_DSN")); dsn != "" {
		var err error
		pgStore, err = postgres.Open(ctx, dsn)
		if err != nil {
			logger.Error("audit-projection postgres init failed", "error", err)
			os.Exit(1)
		}
		defer pgStore.Close()
		logger.Info("audit-projection connected to PostgreSQL")
	}

	if brokers := splitNonEmpty(os.Getenv("AUDIT_KAFKA_BROKERS")); len(brokers) > 0 {
		topics := splitNonEmpty(envOr(
			"AUDIT_KAFKA_TOPICS",
			"bpmp.case.events.v1.e2e,bpmp.document.events.v1.e2e,bpmp.integration.events.v1.e2e,bpmp.mdm.events.v1.e2e",
		))
		groupID := envOr("AUDIT_KAFKA_GROUP", "bpmp.audit-projection.v1.e2e")
		go func() {
			err := odsruntime.RunConsumer(ctx, logger, odsruntime.ConsumerConfig{
				Brokers:  brokers,
				GroupID:  groupID,
				Topics:   topics,
				ClientID: "audit-projection-consumer",
			}, func(hCtx context.Context, topic string, value []byte) error {
				var env odsruntime.OutboxEnvelope
				if err := json.Unmarshal(value, &env); err != nil {
					logger.Warn("skipping malformed audit envelope", "topic", topic, "error", err)
					return nil
				}
				if env.TenantID == "" || env.EventID == "" {
					return nil
				}
				src := env.Producer
				if src == "" {
					src = topic
				}
				evt := application.CommittedOwnerEvent{
					EventID:       env.EventID,
					TenantID:      env.TenantID,
					SourceService: src,
					AggregateType: env.AggregateType,
					AggregateID:   env.AggregateID,
					Action:        env.EventType,
					ActorID:       "system:" + src,
					CorrelationID: env.CorrelationID,
					OccurredAt:    env.OccurredAt,
				}
				if evt.OccurredAt.IsZero() {
					evt.OccurredAt = time.Now().UTC()
				}
				inserted, err := svc.Ingest(hCtx, evt)
				if err != nil {
					return err
				}
				if inserted && pgStore != nil {
					return pgStore.UpsertEntry(hCtx, application.ProjectedEntry{
						EventID:              evt.EventID,
						TenantID:             evt.TenantID,
						SourceService:        evt.SourceService,
						AggregateType:        evt.AggregateType,
						AggregateID:          evt.AggregateID,
						Action:               evt.Action,
						ActorID:              evt.ActorID,
						CorrelationID:        evt.CorrelationID,
						OccurredAt:           evt.OccurredAt,
						RedactedMetadata:     map[string]string{},
						ProjectionGeneration: 1,
					})
				}
				return nil
			})
			if err != nil && ctx.Err() == nil {
				logger.Error("audit-projection consumer stopped", "error", err)
			}
		}()
		logger.Info("audit-projection Kafka consumer started", "topics", topics)
	}

	baseHandler := httpapi.NewHandler(svc)
	verifier, err := odsruntime.LoadActorVerifier(
		os.Getenv("AUDIT_ACTOR_PROOF_KEY_ID"),
		envOr("AUDIT_ACTOR_WORKLOAD_ID", "audit-projection"),
		os.Getenv("AUDIT_ACTOR_PROOF_PUBLIC_KEY"),
	)
	if err != nil {
		logger.Error("audit-projection actor proof verifier init failed", "error", err)
		os.Exit(1)
	}
	metrics := odsruntime.NewMetricsCollector("audit-projection", nil)
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
		ListenAddress:  envOr("AUDIT_PROJECTION_ADDR", "0.0.0.0:8087"),
		ServerCertFile: os.Getenv("AUDIT_SERVER_CERTIFICATE"),
		ServerKeyFile:  os.Getenv("AUDIT_SERVER_PRIVATE_KEY"),
		ClientCAFile:   os.Getenv("AUDIT_CLIENT_CA"),
	}, handler)
	if err != nil {
		logger.Error("audit-projection stopped with error", "error", err)
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
