package alerting

import (
	"testing"
	"testing/quick"
)

// Feature: rust-bpm-platform, Property 32: SLA breach emits one alert per threshold crossing
func TestOneAlertPerThresholdCrossing(t *testing.T) {
	property := func(rawThreshold, rawSamples uint8) bool {
		threshold := float64(rawThreshold) + 1
		evaluator, err := New(Policy{Threshold: threshold})
		if err != nil {
			return false
		}
		emitted := 0
		for sample := 0; sample < int(rawSamples%17)+2; sample++ {
			alert, observeErr := evaluator.Observe("workflow-latency", threshold+float64(sample))
			if observeErr != nil {
				return false
			}
			if alert {
				emitted++
			}
		}
		return emitted == 1
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}
