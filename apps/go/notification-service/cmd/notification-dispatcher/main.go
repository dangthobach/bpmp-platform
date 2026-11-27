package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/bootstrap"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
)

func main() {
	lanesFlag := flag.String("lanes", defaultEnv("NOTIFICATION_DISPATCH_LANES", "transactional,normal,bulk"), "comma-separated dispatch lanes")
	batchSize := flag.Int("batch-size", 25, "claim batch size per poll")
	leaseMS := flag.Int64("lease-ms", 30000, "job lease duration in milliseconds")
	pollMS := flag.Int64("poll-ms", 250, "idle poll interval in milliseconds")
	flag.Parse()

	if err := run(*lanesFlag, *batchSize, time.Duration(*leaseMS)*time.Millisecond, time.Duration(*pollMS)*time.Millisecond); err != nil {
		slog.Error("notification-dispatcher stopped", "error", err)
		os.Exit(1)
	}
}

func run(lanesRaw string, batchSize int, leaseDuration, pollInterval time.Duration) error {
	var lanes []string
	for _, part := range strings.Split(lanesRaw, ",") {
		l := strings.TrimSpace(part)
		if l == "" {
			continue
		}
		if !queue.ValidLane(l) {
			return queue.ErrInvalidLane
		}
		lanes = append(lanes, l)
	}
	if len(lanes) == 0 {
		lanes = []string{queue.LaneTransactional, queue.LaneNormal, queue.LaneBulk}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap.InitRuntime(ctx, false)
	if err != nil {
		return err
	}
	defer rt.Close()

	slog.Info("notification-dispatcher started", "lanes", lanes, "batch_size", batchSize)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		totalProcessed := 0
		for _, lane := range lanes {
			n, err := rt.Service.ProcessQueueBatch(ctx, lane, batchSize, leaseDuration)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				slog.Error("dispatch lane batch failed", "lane", lane, "error", err)
				continue
			}
			totalProcessed += n
		}
		if totalProcessed >= batchSize {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func defaultEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
