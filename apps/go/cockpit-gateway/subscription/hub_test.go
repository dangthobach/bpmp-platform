package subscription

import (
	"fmt"
	"testing"
	"testing/quick"
)

// Feature: rust-bpm-platform, Property 19: Signal fanout honors every subscription filter
func TestFanoutOnlyMatchesFilters(t *testing.T) {
	property := func(rawCount uint8) bool {
		count := int(rawCount%40) + 1
		hub, err := New(Config{MaxSubscriptions: uint32(count + 1), BufferSize: 1, ReplaySize: 8, MaxReplayStreams: uint32(count + 1)})
		if err != nil {
			return false
		}
		matchesExpected := 0
		subscriptions := make([]*Subscription, 0, count)
		for index := 0; index < count; index++ {
			region := "eu"
			if index%3 == 0 {
				region = "us"
				matchesExpected++
			}
			subscription, subscribeErr := hub.Subscribe(Filter{
				TenantID: "tenant-a",
				Name:     "order.changed",
				Labels:   map[string]string{"region": region},
			})
			if subscribeErr != nil {
				return false
			}
			subscriptions = append(subscriptions, subscription)
		}
		delivered := hub.Publish(Signal{
			TenantID: "tenant-a",
			Name:     "order.changed",
			Labels:   map[string]string{"region": "us"},
			Payload:  []byte(fmt.Sprint(rawCount)),
		})
		for _, subscription := range subscriptions {
			subscription.Close()
		}
		return delivered == matchesExpected
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: rust-bpm-platform, Property 20: Disconnect removes subscription state
func TestDisconnectAlwaysRemovesSubscription(t *testing.T) {
	property := func(rawCount uint8) bool {
		count := int(rawCount%50) + 1
		hub, err := New(Config{MaxSubscriptions: uint32(count), BufferSize: 1, ReplaySize: 8, MaxReplayStreams: uint32(count)})
		if err != nil {
			return false
		}
		subscriptions := make([]*Subscription, 0, count)
		for index := 0; index < count; index++ {
			subscription, subscribeErr := hub.Subscribe(Filter{
				TenantID: "tenant-a",
				Name:     fmt.Sprintf("signal-%d", index),
			})
			if subscribeErr != nil {
				return false
			}
			subscriptions = append(subscriptions, subscription)
		}
		for _, subscription := range subscriptions {
			subscription.Close()
			subscription.Close()
		}
		return hub.Count() == 0
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectReplaysSuffixOrRequestsResync(t *testing.T) {
	hub, err := New(Config{MaxSubscriptions: 4, BufferSize: 4, ReplaySize: 3, MaxReplayStreams: 4})
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 4; index++ {
		hub.Publish(Signal{
			Cursor: fmt.Sprintf("cursor-%d", index), TenantID: "tenant-a",
			Name: "workflow.changed", Payload: []byte(fmt.Sprint(index)),
		})
	}
	current, err := hub.SubscribeFrom(Filter{
		TenantID: "tenant-a", Name: "workflow.changed",
	}, "cursor-2")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if current.ResumeReset {
		t.Fatal("retained cursor unexpectedly requires reset")
	}
	for _, expected := range []string{"3", "4"} {
		if got := string((<-current.Signals).Payload); got != expected {
			t.Fatalf("expected replay %s, got %s", expected, got)
		}
	}
	expired, err := hub.SubscribeFrom(Filter{
		TenantID: "tenant-a", Name: "workflow.changed",
	}, "cursor-1")
	if err != nil {
		t.Fatal(err)
	}
	defer expired.Close()
	if !expired.ResumeReset {
		t.Fatal("expired cursor must require reset")
	}
}

func TestSlowSubscriberIsDisconnected(t *testing.T) {
	hub, err := New(Config{MaxSubscriptions: 1, BufferSize: 1, ReplaySize: 1, MaxReplayStreams: 1})
	if err != nil {
		t.Fatal(err)
	}
	value, err := hub.Subscribe(Filter{TenantID: "tenant-a", Name: "audit.changed"})
	if err != nil {
		t.Fatal(err)
	}
	hub.Publish(Signal{TenantID: "tenant-a", Name: "audit.changed"})
	stats := hub.PublishToIndexedSubscribers(
		Signal{TenantID: "tenant-a", Name: "audit.changed"},
	)
	if stats.Disconnected != 1 || hub.Count() != 0 {
		t.Fatalf("expected one disconnected subscriber, got %+v", stats)
	}
	value.Close()
}
