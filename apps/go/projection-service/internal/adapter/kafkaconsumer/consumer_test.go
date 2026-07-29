package kafkaconsumer

import (
	"context"
	"sync"
	"testing"
	"time"

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
	policyCalls := 0
	consumer, err := New(client, handler, func() (RuntimePolicy, error) {
		policyCalls++
		batchSize := 2
		if policyCalls > 1 {
			batchSize = 1
		}
		return RuntimePolicy{
			BatchSize:       batchSize,
			CheckpointFlush: time.Second,
			MaxLag:          time.Hour,
		}, nil
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
		Timestamp: time.Now(),
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
