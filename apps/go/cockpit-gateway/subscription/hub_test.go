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
		hub, err := New(Config{MaxSubscriptions: uint32(count + 1), BufferSize: 1})
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
		hub, err := New(Config{MaxSubscriptions: uint32(count), BufferSize: 1})
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
