package kafkaconsumer

import (
	"context"
	"errors"
	"github.com/twmb/franz-go/pkg/kgo"
	"testing"
)

type fakeClient struct{ commits int }

func (*fakeClient) PollRecords(context.Context, int) kgo.Fetches          { return kgo.Fetches{} }
func (f *fakeClient) CommitRecords(context.Context, ...*kgo.Record) error { f.commits++; return nil }

type fakeHandler struct{ err error }

func (f fakeHandler) Handle(context.Context, []byte) error { return f.err }
func TestRecordCommitsOnlyAfterDurableHandlerSuccess(t *testing.T) {
	record := &kgo.Record{Value: []byte("event")}
	client := &fakeClient{}
	consumer, _ := New(client, fakeHandler{err: errors.New("db failed")}, 1)
	if err := consumer.HandleRecord(context.Background(), record); err == nil {
		t.Fatal("expected handler error")
	}
	if client.commits != 0 {
		t.Fatal("record committed before durable handler")
	}
	consumer.handler = fakeHandler{}
	if err := consumer.HandleRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if client.commits != 1 {
		t.Fatalf("expected one commit, got %d", client.commits)
	}
}
