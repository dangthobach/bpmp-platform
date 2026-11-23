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
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/kafkapublisher"
	"github.com/dangthobach/bpmp-platform/apps/go/case-service/internal/postgres"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	PostgresDSN    string               `json:"postgres_dsn"`
	Kafka          kafkaconfig.Producer `json:"kafka"`
	PollIntervalMS int                  `json:"poll_interval_ms"`
}

func main() {
	path := flag.String("config", os.Getenv("CASE_OUTBOX_CONFIG"), "Case outbox worker JSON configuration")
	once := flag.Bool("once", false, "process at most one available event and exit")
	flag.Parse()
	if err := run(*path, *once); err != nil {
		slog.Error("Case outbox worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string, once bool) error {
	if path == "" {
		return errors.New("Case outbox configuration path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode Case outbox config: %w", err)
	}
	if cfg.PostgresDSN == "" || cfg.PollIntervalMS < 50 || cfg.PollIntervalMS > 5000 ||
		cfg.Kafka.Validate() != nil || cfg.Kafka.MessageTimeout() > 20*time.Second {
		return errors.New("Case outbox configuration is invalid")
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
	client, err := kafkaconfig.NewProducerClient(cfg.Kafka)
	if err != nil {
		return err
	}
	defer client.Close()
	if err = client.Ping(ctx); err != nil {
		return fmt.Errorf("connect Case event broker: %w", err)
	}
	publisher, err := kafkapublisher.New(client, cfg.Kafka)
	if err != nil {
		return err
	}
	store, err := postgres.New(pool)
	if err != nil {
		return err
	}
	runner, err := application.NewOutboxRunner(store, publisher, time.Now, uuid.New)
	if err != nil {
		return err
	}
	for {
		found, runErr := runner.RunOnce(ctx)
		if once {
			return runErr
		}
		if ctx.Err() != nil {
			return nil
		}
		if runErr != nil {
			slog.Error("Case outbox delivery failed", "error", runErr)
		}
		if found && runErr == nil {
			continue
		}
		delay := time.Duration(cfg.PollIntervalMS) * time.Millisecond
		if runErr != nil {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
