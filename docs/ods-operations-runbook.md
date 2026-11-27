# ODS Operations & On-Call Runbook (Phase 5)

## 1. High Error Rate (`ODSHighErrorRate`) {#high-error-rate}
1. Check `correlation_id` and `trace_id` in OpenSearch / Tempo for the failing service.
2. Verify whether failures originate from an external dependency (check `IntegrationCircuitBreakerOpen`) or internal DB/KMS (`BPMPKMSFailClosed`).
3. If caused by a recent MFE or service release, execute catalog rollback (`tools/ods-cutover-canary.mjs`) or `helm rollback`.

## 2. High Latency (`ODSHighLatencyP95`) {#high-latency}
1. Inspect DB connection pool saturation, slow query plans, and Kafka consumer lag.
2. Confirm Document uploads are streaming directly to CSP quarantine rather than buffering in Gateway/BFF.

## 3. Pod Restarts (`ODSPodRestartLoop`) {#pod-restarts}
1. Inspect `kubectl describe pod` and previous container logs for OOMKill or failed `livenessProbe`.
2. Ensure `livenessProbe` does not fail on transient upstream outages.

## 4. Outbox & Projection Lag (`ODSOutboxOrProjectionLagHigh`) {#projection-lag}
1. Verify Kafka broker health and consumer group lag.
2. Check for poison messages routed to DLQ; never delete committed Engine events.

## 5. Integration Circuit Open (`IntegrationCircuitBreakerOpen`) {#integration-circuit-open}
1. Confirm downstream outage (T24/ESB, AML, RPA, IDP/OCR).
2. Leave circuit breaker active to prevent cascading failures; deliveries in `RECONCILING` or `RETRY_WAIT` retain their original `downstream_idempotency_key` for safe operator retry once recovered.

## 6. Redis / OpenSearch Loss Recovery (No Business State Loss)
- **Redis loss**: Browser sessions require re-authentication via Keycloak PKCE; MDM local caches automatically repopulate from PostgreSQL `mdm_publications`. Zero authoritative business data is lost.
- **OpenSearch / Audit Projection loss**: Authoritative audit records remain in each owner's PostgreSQL/RocksDB store. Trigger `POST /v1/audit/rebuild` (or replay committed Kafka audit topics) to increment `projection_generation` and reconstruct the searchable index.

## 7. Alert Drill Checklist
- [ ] Simulate 5xx burst in staging and verify `ODSHighErrorRate` fires after 5m window.
- [ ] Simulate Audit Projection wipe and verify `Rebuild()` restores 100% of tenant-scoped entries.
- [ ] Simulate MDM cache flush and verify pinned snapshot resolution returns identical SHA-256 digest.
