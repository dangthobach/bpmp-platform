package kafkaconfig

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// CheckFetchErrors lets the client rejoin/reconnect without discarding successful
// partitions in the same fetch. Record handlers and offset commits remain separate.
func CheckFetchErrors(ctx context.Context, faults []kgo.FetchError) error {
	for _, fault := range faults {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fault.Err
		if retryableConsumerError(err) {
			slog.WarnContext(ctx, "Kafka fetch temporarily unavailable; client recovery retained", "topic", fault.Topic, "partition", fault.Partition, "error", err)
			continue
		}
		return err
	}
	return nil
}

func retryableConsumerError(err error) bool {
	return kgo.IsRetryableBrokerErr(err) || kerr.IsRetriable(err) ||
		errors.Is(err, kerr.UnknownMemberID) || errors.Is(err, kerr.IllegalGeneration) ||
		errors.Is(err, kerr.RebalanceInProgress) || errors.Is(err, kerr.MemberIDRequired)
}

type OffsetCommitter interface {
	CommitRecords(context.Context, ...*kgo.Record) error
}

// RetryOffsetCommit retains the already-durable batch while group membership
// recovers. It never polls newer records or advances past a failed local effect.
func RetryOffsetCommit(ctx context.Context, client OffsetCommitter, attempts uint32, backoff, timeout time.Duration, records ...*kgo.Record) error {
	if client == nil || attempts == 0 || attempts > 20 || backoff <= 0 || backoff > 5*time.Minute || timeout <= 0 || timeout > 5*time.Minute {
		return ErrInvalidConfig
	}
	var err error
	for attempt := uint32(0); attempt < attempts; attempt++ {
		command, cancel := context.WithTimeout(ctx, timeout)
		err = client.CommitRecords(command, records...)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryableConsumerError(err) && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if attempt+1 == attempts {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
