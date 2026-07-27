package domain

import (
	"bytes"
	"testing"
)

func TestParsePolicyProducesStableCanonicalHash(t *testing.T) {
	first, canonical, firstHash, err := ParsePolicy(validPolicyJSON())
	if err != nil {
		t.Fatal(err)
	}
	_, secondCanonical, secondHash, err := ParsePolicy(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetCommandTimeoutMs() != 15000 ||
		!bytes.Equal(canonical, secondCanonical) ||
		firstHash != secondHash {
		t.Fatal("policy canonicalization is not deterministic")
	}
}

func TestParsePolicyRejectsUnsafeOrIncompletePolicy(t *testing.T) {
	invalid := bytes.Replace(validPolicyJSON(), []byte(`"maxAttempts":3`), []byte(`"maxAttempts":0`), 1)
	if _, _, _, err := ParsePolicy(invalid); err == nil {
		t.Fatal("zero retry attempts must be rejected")
	}
	if _, _, _, err := ParsePolicy([]byte(`{"unknown":1}`)); err == nil {
		t.Fatal("unknown and incomplete policy must be rejected")
	}
}

func validPolicyJSON() []byte {
	return []byte(`{
		"snapshotIntervalEvents":100,
		"maxEventsPerDecision":64,
		"commandTimeoutMs":"15000",
		"optimisticConflictRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000},
		"localWasm":{"maxModuleBytes":"1048576","maxInputBytes":"262144","maxOutputBytes":"262144","maxMemoryBytes":"16777216","maxWasmStackBytes":"1048576","maxTableElements":1000,"maxInstances":4,"maxTables":4,"maxMemories":1,"fuel":"1000000"},
		"eventPayloadKeyScope":"tenant/operational","authorizationAuditKeyScope":"tenant/audit",
		"maxMultiInstanceCardinality":1000,
		"defaultMultiInstanceParallelism":8,
		"boundaryRuntime":{"projectionBatchSize":100,"dispatchBatchSize":50,"maxDispatchAttempts":5,"retryDelayMs":"1000","leaseDurationMs":"30000","maxTimerHorizonMs":"31536000000","maxExpressionBytes":65536,"workerId":"boundary-worker","maxSignalIdBytes":256,"maxReferenceBytes":512,"maxSubscriptionsPerInstance":100}
	}`)
}
