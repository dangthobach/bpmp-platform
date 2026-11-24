package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/engineevents"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/postgres"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/dangthobach/bpmp-platform/go/platform/runtimeconfig"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	PostgresDSN string               `json:"postgres_dsn"`
	Kafka       kafkaconfig.Consumer `json:"kafka"`
}

func main() {
	path := flag.String("config", os.Getenv("CASE_ENGINE_EVENTS_CONFIG"), "Case Engine event consumer JSON configuration")
	flag.Parse()
	if err := run(*path); err != nil {
		slog.Error("Case Engine event consumer stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string) error {
	if path == "" {
		return errors.New("Case Engine event consumer configuration path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode Case Engine event consumer config: %w", err)
	}
	if cfg.PostgresDSN == "" || cfg.Kafka.Validate() != nil || cfg.Kafka.MaxMessageBytes > engineevents.MaxEventBytes {
		return errors.New("Case Engine event consumer configuration is invalid")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect Case database: %w", err)
	}
	kafka, err := runtimeconfig.NewKafkaClient(cfg.Kafka)
	if err != nil {
		return err
	}
	defer kafka.Close()
	if err = kafka.Ping(ctx); err != nil {
		return fmt.Errorf("connect committed Engine event broker: %w", err)
	}
	store, err := postgres.New(pool)
	if err != nil {
		return err
	}
	service, err := application.NewEngineEventService(store)
	if err != nil {
		return err
	}
	handler, err := engineevents.New(service)
	if err != nil {
		return err
	}
	consumer, err := engineevents.NewConsumer(kafka, handler, cfg.Kafka)
	if err != nil {
		return err
	}
	return consumer.Run(ctx)
}
