package kafkaconsumer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

type recordingClient struct {
	mu       sync.Mutex
	fetches  []kgo.Fetches
	maxPolls []int
	commits  [][]*kgo.Record
	cancel   context.CancelFunc
}

func (c *recordingClient) PollRecords(ctx context.Context, maxRecords int) kgo.Fetches {
	c.mu.Lock()
	c.maxPolls = append(c.maxPolls, maxRecords)
	if len(c.fetches) > 0 {
		fetches := c.fetches[0]
		c.fetches = c.fetches[1:]
		c.mu.Unlock()
		return fetches
	}
	c.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (c *recordingClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	c.mu.Lock()
	c.commits = append(c.commits, append([]*kgo.Record(nil), records...))
	complete := len(c.commits) == 2
	c.mu.Unlock()
	if complete {
		c.cancel()
	}
	return nil
}

type recordingHandler struct {
	mu      sync.Mutex
	offsets []int64
}

func (h *recordingHandler) Handle(_ context.Context, record *kgo.Record) error {
	h.mu.Lock()
	h.offsets = append(h.offsets, record.Offset)
	h.mu.Unlock()
	return nil
}

func TestPolicyChangesOnlyAfterCheckpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &recordingClient{
		fetches: []kgo.Fetches{
			fetchWithRecords(record(1), record(2)),
			fetchWithRecords(record(3)),
		},
		cancel: cancel,
	}
	handler := &recordingHandler{}
	// A recoverable fault on another partition must not discard fetched records.
	client.fetches[0][0].Topics[0].Partitions = append(client.fetches[0][0].Topics[0].Partitions, kgo.FetchPartition{Partition: 1, Err: kerr.UnknownMemberID})
	policyCalls := 0
	consumer, err := New(client, handler, func() (RuntimePolicy, error) {
		policyCalls++
		policy := RuntimePolicy{
			ConsumeBatchSize:         4,
			RebuildBatchSize:         3,
			RealtimePublishBatchSize: 2,
			CheckpointFlush:          time.Second,
			MaxLag:                   time.Hour,
			CommitRetryAttempts:      3,
			CommitBackoff:            time.Millisecond,
			CommitTimeout:            time.Second,
		}
		if policyCalls > 1 {
			policy.ConsumeBatchSize = 5
			policy.RebuildBatchSize = 1
			policy.RealtimePublishBatchSize = 4
		}
		return policy, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err = consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if policyCalls != 2 {
		t.Fatalf("expected one policy snapshot per checkpoint batch, got %d", policyCalls)
	}
	if len(client.commits) != 2 ||
		len(client.commits[0]) != 2 ||
		len(client.commits[1]) != 1 {
		t.Fatalf("unexpected checkpoint batches: %#v", client.commits)
	}
	if len(client.maxPolls) != 2 ||
		client.maxPolls[0] != 2 ||
		client.maxPolls[1] != 1 {
		t.Fatalf("policy was not applied at batch boundaries: %#v", client.maxPolls)
	}
}

func record(offset int64) *kgo.Record {
	return &kgo.Record{
		Topic:     "bpmp.engine.events.v1",
		Partition: 0,
		Offset:    offset,
		Timestamp: time.Now().Add(-2 * time.Hour),
	}
}

type idleFlushClient struct{ *recordingClient }

func (c *idleFlushClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	c.commits = append(c.commits, append([]*kgo.Record(nil), records...))
	c.cancel()
	return nil
}

func TestPartialBatchFlushesWithoutAnotherEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &idleFlushClient{&recordingClient{fetches: []kgo.Fetches{fetchWithRecords(record(1))}, cancel: cancel}}
	consumer, err := New(client, &recordingHandler{}, func() (RuntimePolicy, error) {
		return RuntimePolicy{ConsumeBatchSize: 10, RebuildBatchSize: 10, RealtimePublishBatchSize: 10, CheckpointFlush: 10 * time.Millisecond, MaxLag: time.Hour, CommitRetryAttempts: 3, CommitBackoff: time.Millisecond, CommitTimeout: time.Second}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.commits) != 1 || len(client.commits[0]) != 1 {
		t.Fatal("partial batch was never checkpointed while input was idle")
	}
}

func fetchWithRecords(records ...*kgo.Record) kgo.Fetches {
	return kgo.Fetches{{
		Topics: []kgo.FetchTopic{{
			Topic: "bpmp.engine.events.v1",
			Partitions: []kgo.FetchPartition{{
				Partition: 0,
				Records:   records,
			}},
		}},
	}}
}
