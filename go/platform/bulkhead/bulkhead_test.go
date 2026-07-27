package bulkhead

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"
)

// Feature: rust-bpm-platform, Property 26: Bulkhead concurrency is bounded and groups are isolated
func TestConcurrencyNeverExceedsPerGroupLimit(t *testing.T) {
	property := func(rawA, rawB uint8) bool {
		limitA := uint32(rawA%5 + 1)
		limitB := uint32(rawB%5 + 1)
		gate, err := New(Config{GroupLimits: map[string]uint32{"a": limitA, "b": limitB}})
		if err != nil {
			return false
		}
		var activeA, activeB, maxA, maxB atomic.Uint32
		var wg sync.WaitGroup
		run := func(group string, active, maximum *atomic.Uint32) {
			defer wg.Done()
			_ = gate.Do(context.Background(), group, func(context.Context) error {
				current := active.Add(1)
				for {
					observed := maximum.Load()
					if current <= observed || maximum.CompareAndSwap(observed, current) {
						break
					}
				}
				time.Sleep(time.Microsecond)
				active.Add(^uint32(0))
				return nil
			})
		}
		for range 12 {
			wg.Add(2)
			go run("a", &activeA, &maxA)
			go run("b", &activeB, &maxB)
		}
		wg.Wait()
		return maxA.Load() <= limitA &&
			maxB.Load() <= limitB &&
			activeA.Load() == 0 &&
			activeB.Load() == 0
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
