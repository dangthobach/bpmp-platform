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
		{OwnerProjection, `{"consumeBatchSize":500,"rebuildBatchSize":1000,"queryDefaultPageSize":50,"queryMaxPageSize":200,"realtimePublishBatchSize":100,"checkpointFlushMs":"1000","maxProjectionLagMs":"30000","queryMaxFilterDepth":8,"queryMaxPredicates":32,"queryMaxSortFields":4,"queryMaxRelationJoins":4,"queryMaxInValues":100,"queryTimeoutMs":"3000","queryMaxStringBytes":512,"queryCursorSigningKeyId":"projection-cursor-v1","queryAllCapability":"workflow.query.all","queryOwnCapability":"workflow.query.own","queryMaxTokenAgeMs":"3600000","queryPdpResourceType":"workflow_instance","queryPdpAction":"query","queryRevokeRetryAttempts":3,"queryRevokeRetryBackoffMs":"250","queryRevokeOperationTimeoutMs":"3000"}`},
		{OwnerGovernance, `{"approvalTtlMs":"300000","freshAuthenticationMaxAgeMs":"60000","kmsRequestTimeoutMs":"3000","kmsRetry":{"maxAttempts":3,"initialBackoffMs":"100","maxBackoffMs":"1000","multiplierMillis":2000},"keyCacheTtlMs":"60000","revocationBarrierTimeoutMs":"30000","reconciliationBatchSize":100,"maxPendingCompensations":1000,"abortCapability":"governance.abort_and_reconcile","acceptedAuthAssurance":["mfa"],"approvalKeys":[{"keyId":"requester-key","ed25519PublicKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","enabled":true},{"keyId":"approver-key","ed25519PublicKey":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","enabled":true}],"requiredApproverCount":1}`},
		{OwnerConfigurationService, `{"outboxBatchSize":100,"outboxLeaseMs":"30000","outboxPollMs":"500","outboxRetry":{"maxAttempts":3,"initialBackoffMs":"100","maxBackoffMs":"1000","multiplierMillis":2000},"queryDefaultPageSize":50,"queryMaxPageSize":200,"maxRequestBodyBytes":"1048576"}`},
		{OwnerCockpitGateway, `{"maxNamesPerConnection":10,"maxSignalNamesBytes":1024,"maxConnections":1000,"maxSubscriptions":100,"outboundBufferSize":256,"replaySizePerStream":100,"maxReplayStreams":100,"heartbeatIntervalMs":"15000","consumeBatchSize":100,"allowedSignalNames":["workflow.updated"],"allowedOrigins":["https://cockpit.example.com"]}`},
		{OwnerAuthzControlPlane, `{"requestTimeoutMs":"3000","connectTimeoutMs":"1000","httpPoolMaxIdlePerHost":32,"graphMaxDepth":10,"graphMemoCapacity":"10000","graphMemoTtlMs":"60000","inactiveUserDays":60,"inactiveUserBatchSize":1000,"inactiveUserPollMs":"86400000","inactiveUserRetry":{"maxAttempts":3,"initialBackoffMs":"100","maxBackoffMs":"1000","multiplierMillis":2000}}`},
		{OwnerNotification, validNotificationPolicyString()},
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

func TestNotificationPolicyRejectsUnsafeOrIncompleteValues(t *testing.T) {
	t.Parallel()
	base := []byte(validNotificationPolicyString())
	if err := ValidateOwner(OwnerNotification); err != nil {
		t.Fatalf("OwnerNotification rejected by ValidateOwner: %v", err)
	}
	parsed, canonical, hash, err := ParsePolicy(OwnerNotification, base)
	if err != nil || parsed.Notification == nil || len(canonical) == 0 || hash == ([32]byte{}) {
		t.Fatalf("valid NotificationPolicy failed to parse: parsed=%+v err=%v", parsed, err)
	}

	replacements := [][2]string{
		{`"workerConcurrency":16`, `"workerConcurrency":0`},
		{`"claimBatchSize":32`, `"claimBatchSize":0`},
		{`"leaseDurationMs":"30000"`, `"leaseDurationMs":"0"`},
		{`"maxAttempts":5`, `"maxAttempts":0`},
		{`"failureRateThresholdBps":5000`, `"failureRateThresholdBps":0`},
		{`"failureRateThresholdBps":5000`, `"failureRateThresholdBps":10001`},
		{`"halfOpenMaxProbes":3`, `"halfOpenMaxProbes":0`},
		{`"maxPayloadBytes":65536`, `"maxPayloadBytes":0`},
		{`"transactionalPayloadDays":30`, `"transactionalPayloadDays":0`},
		{`"defaultProviderBulkheadConcurrency":32`, `"defaultProviderBulkheadConcurrency":0`},
		{`"providerCallTimeoutMs":"5000"`, `"providerCallTimeoutMs":"0"`},
		{`"allowedProviderHosts":["email.us-east-1.amazonaws.com"]`, `"allowedProviderHosts":["","email.us-east-1.amazonaws.com"]`},
		{`"otpPerRecipientLimit":5`, `"otpPerRecipientLimit":0`},
	}
	for _, tc := range replacements {
		mutated := bytes.Replace(base, []byte(tc[0]), []byte(tc[1]), 1)
		if bytes.Equal(mutated, base) {
			t.Fatalf("test mutation target %q not found in fixture", tc[0])
		}
		if _, _, _, err := ParsePolicy(OwnerNotification, mutated); err == nil {
			t.Fatalf("expected invalid NotificationPolicy rejection when replacing %s -> %s", tc[0], tc[1])
		}
	}

	if _, _, _, err := ParsePolicy(OwnerNotification, []byte(`{}`)); err == nil {
		t.Fatal("empty NotificationPolicy must be rejected")
	}
}

func TestNotificationPolicySlowCallBreakerValidation(t *testing.T) {
	t.Parallel()
	base := []byte(validNotificationPolicyString())

	// Valid: both zero (disabled, as in validNotificationPolicyString).
	if parsed, _, _, err := ParsePolicy(OwnerNotification, base); err != nil {
		t.Fatalf("expected disabled slow-call config to be valid: %v", err)
	} else if parsed.Notification.GetDefaultBreaker().GetSlowCallThresholdMs() != 0 ||
		parsed.Notification.GetDefaultBreaker().GetSlowCallRateThresholdBps() != 0 {
		t.Fatalf("expected zero slow-call defaults, got %+v", parsed.Notification.GetDefaultBreaker())
	}

	// Valid: slow_call_threshold_ms > 0, <= provider_call_timeout_ms (5000), and bps in 1..10000.
	validSlowCases := []string{
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"2000","slowCallRateThresholdBps":5000`,
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"5000","slowCallRateThresholdBps":10000`,
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"1","slowCallRateThresholdBps":1`,
	}
	for _, repl := range validSlowCases {
		mutated := bytes.Replace(base, []byte(`"maxManualOverrideTtlMs":"3600000"`), []byte(repl), 1)
		if _, _, _, err := ParsePolicy(OwnerNotification, mutated); err != nil {
			t.Fatalf("expected valid slow-call config (%s) to pass, got %v", repl, err)
		}
	}

	// Invalid slow-call combinations.
	invalidSlowCases := []string{
		// threshold > 0, rate == 0
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"2000","slowCallRateThresholdBps":0`,
		// threshold == 0, rate > 0
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"0","slowCallRateThresholdBps":5000`,
		// rate > 10000
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"2000","slowCallRateThresholdBps":10001`,
		// threshold > providerCallTimeoutMs (5000)
		`"maxManualOverrideTtlMs":"3600000","slowCallThresholdMs":"5001","slowCallRateThresholdBps":5000`,
	}
	for _, repl := range invalidSlowCases {
		mutated := bytes.Replace(base, []byte(`"maxManualOverrideTtlMs":"3600000"`), []byte(repl), 1)
		if _, _, _, err := ParsePolicy(OwnerNotification, mutated); err == nil {
			t.Fatalf("expected invalid slow-call config (%s) to be rejected", repl)
		}
	}
}

func validNotificationPolicyString() string {
	return `{
		"transactionalLane":{"workerConcurrency":16,"claimBatchSize":32,"leaseDurationMs":"30000","retry":{"maxAttempts":5,"initialBackoffMs":"250","maxBackoffMs":"10000","multiplierMillis":2000},"maxQueueLagAlertMs":"2000"},
		"normalLane":{"workerConcurrency":8,"claimBatchSize":64,"leaseDurationMs":"45000","retry":{"maxAttempts":6,"initialBackoffMs":"500","maxBackoffMs":"30000","multiplierMillis":2000},"maxQueueLagAlertMs":"15000"},
		"bulkLane":{"workerConcurrency":4,"claimBatchSize":128,"leaseDurationMs":"60000","retry":{"maxAttempts":4,"initialBackoffMs":"1000","maxBackoffMs":"60000","multiplierMillis":2000},"maxQueueLagAlertMs":"60000"},
		"defaultBreaker":{"rollingWindowMs":"60000","minRequests":20,"failureRateThresholdBps":5000,"openStateDurationMs":"30000","halfOpenMaxProbes":3,"maxManualOverrideTtlMs":"3600000"},
		"renderLimits":{"maxPayloadBytes":65536,"maxEmailHtmlBytes":102400,"maxSmsSegments":6,"maxPushBodyBytes":4096,"maxArrayItems":100,"previewTimeoutMs":"2000","previewBulkheadConcurrency":16,"previewRateLimitPerMinute":120},
		"retention":{"transactionalPayloadDays":30,"bulkPayloadDays":7,"deliveryMetadataMonths":13,"attemptCallbackDays":90,"metricsHourlyMonths":25,"idempotencyInboxDays":14,"inappMessageDays":90,"instanceContextRetentionHours":24,"cleanupBatchSize":500,"cleanupIntervalMs":"60000"},
		"defaultProviderBulkheadConcurrency":32,
		"providerCallTimeoutMs":"5000",
		"allowedProviderHosts":["email.us-east-1.amazonaws.com"],
		"otpPerRecipientLimit":5,
		"otpRateWindowMs":"600000"
	}`
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
		"boundaryRuntime":{"projectionBatchSize":100,"dispatchBatchSize":50,"maxDispatchAttempts":5,"retryDelayMs":"1000","leaseDurationMs":"30000","maxTimerHorizonMs":"31536000000","maxExpressionBytes":65536,"workerId":"boundary-worker","maxSignalIdBytes":256,"maxReferenceBytes":512,"maxSubscriptionsPerInstance":100},
		"workers":{"pollIntervalMs":"100","outboxBatchSize":100,"outboxRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000},"localTaskBatchSize":50,"localTaskRetry":{"maxAttempts":3,"initialBackoffMs":"20","maxBackoffMs":"200","multiplierMillis":2000}}
	}`)
}
