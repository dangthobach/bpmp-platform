package domain

import (
	"bytes"
	"testing"
)

func TestParsePolicyProducesStableCanonicalHash(t *testing.T) {
	first, canonical, firstHash, err := ParsePolicy(OwnerEngine, validPolicyJSON())
	if err != nil {
		t.Fatal(err)
	}
	_, secondCanonical, secondHash, err := ParsePolicy(OwnerEngine, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if first.Engine.GetCommandTimeoutMs() != 15000 ||
		!bytes.Equal(canonical, secondCanonical) ||
		firstHash != secondHash {
		t.Fatal("policy canonicalization is not deterministic")
	}
}

func TestParsePolicyRejectsUnsafeOrIncompletePolicy(t *testing.T) {
	invalid := bytes.Replace(validPolicyJSON(), []byte(`"maxAttempts":3`), []byte(`"maxAttempts":0`), 1)
	if _, _, _, err := ParsePolicy(OwnerEngine, invalid); err == nil {
		t.Fatal("zero retry attempts must be rejected")
	}
	if _, _, _, err := ParsePolicy(OwnerEngine, []byte(`{"unknown":1}`)); err == nil {
		t.Fatal("unknown and incomplete policy must be rejected")
	}
}

func TestEveryBoundedContextPolicyIsTypedAndValidated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		owner Owner
		value string
	}{
		{OwnerAPIGateway, `{"rateLimitRequests":100,"rateLimitWindowMs":"60000","upstreamTimeoutMs":"3000","circuitBreakerFailureThreshold":5,"circuitBreakerOpenMs":"10000","bulkheadMaxConcurrency":64,"maxRequestBodyBytes":"1048576","maxUpstreamResponseBytes":"1048576","batchChunkSize":100,"batchConcurrency":10,"upstreamRetry":{"maxAttempts":3,"initialBackoffMs":"25","maxBackoffMs":"250","multiplierMillis":2000},"encryptionKeyScope":"tenant/workflows"}`},
		{OwnerHumanRuntime, `{"projectionBatchSize":100,"escalationBatchSize":50,"escalationLeaseMs":"30000","escalationRetryMs":"1000","escalationPollMs":"500","engineCommandTimeoutMs":"3000","maxAssignmentCandidates":1000,"maxDelegationDepth":8,"queryDefaultPageSize":50,"queryMaxPageSize":200,"engineRetry":{"maxAttempts":5,"initialBackoffMs":"50","maxBackoffMs":"1000","multiplierMillis":2000},"engineCircuitBreakerFailureThreshold":5,"engineCircuitBreakerOpenMs":"1000","engineRetryableCodes":["UNAVAILABLE","DEADLINE_EXCEEDED"]}`},
		{OwnerProjection, `{"consumeBatchSize":500,"rebuildBatchSize":1000,"queryDefaultPageSize":50,"queryMaxPageSize":200,"realtimePublishBatchSize":100,"checkpointFlushMs":"1000","maxProjectionLagMs":"30000"}`},
		{OwnerGovernance, `{"approvalTtlMs":"300000","freshAuthenticationMaxAgeMs":"60000","kmsRequestTimeoutMs":"3000","kmsRetry":{"maxAttempts":3,"initialBackoffMs":"100","maxBackoffMs":"1000","multiplierMillis":2000},"keyCacheTtlMs":"60000","revocationBarrierTimeoutMs":"30000","reconciliationBatchSize":100,"maxPendingCompensations":1000,"abortCapability":"governance.abort_and_reconcile","acceptedAuthAssurance":["mfa"],"approvalKeys":[{"keyId":"requester-key","ed25519PublicKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","enabled":true},{"keyId":"approver-key","ed25519PublicKey":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","enabled":true}],"requiredApproverCount":1}`},
	}
	for _, test := range cases {
		if _, _, _, err := ParsePolicy(test.owner, []byte(test.value)); err != nil {
			t.Fatalf("%s policy was rejected: %v", test.owner, err)
		}
	}
}

func TestGovernancePolicyAllowsDisabledRotationKeys(t *testing.T) {
	value := []byte(`{
		"approvalTtlMs":"300000",
		"freshAuthenticationMaxAgeMs":"60000",
		"kmsRequestTimeoutMs":"3000",
		"kmsRetry":{"maxAttempts":3,"initialBackoffMs":"100","maxBackoffMs":"1000","multiplierMillis":2000},
		"keyCacheTtlMs":"60000",
		"revocationBarrierTimeoutMs":"30000",
		"reconciliationBatchSize":100,
		"maxPendingCompensations":1000,
		"abortCapability":"governance.abort_and_reconcile",
		"acceptedAuthAssurance":["mfa"],
		"approvalKeys":[
			{"keyId":"retired-key","ed25519PublicKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","enabled":false},
			{"keyId":"active-key","ed25519PublicKey":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","enabled":true}
		],
		"requiredApproverCount":2
	}`)
	if _, _, _, err := ParsePolicy(OwnerGovernance, value); err != nil {
		t.Fatalf("rotation policy should be valid: %v", err)
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
