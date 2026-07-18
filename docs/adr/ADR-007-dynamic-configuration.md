# ADR-007: Versioned dynamic configuration

- Status: Accepted
- Date: 2026-07-18

## Context

Business policy and operational tuning vary by environment, tenant, workflow,
and time. Hardcoded values make these changes unsafe, unauditable, and difficult
to replay.

## Decision

Changeable values are published as immutable `ConfigurationProfile` versions.
Resolution order is platform, environment, tenant, workflow type, workflow
version, then an approved instance override. The resolved snapshot is validated
before use and passed explicitly into the application/domain decision path.

Events record `config_version` and `policy_version`. Invalid publications do not
replace the last valid version. Rollback publishes a new version referencing
known-good content; history is never edited.

SLA, escalation, retry/backoff, timeout/deadline, circuit breaker, bulkhead,
rate limit, quota, worker routing, feature flags, residency, retention, KMS
cache/rotation policy, projection batch sizes, pagination limits, and integration
endpoints must not be embedded in domain/application handlers.

Protobuf field numbers, schema versions, stable enum tags, and compatibility
guards are immutable protocol constants and are not runtime configuration.

## Consequences

Every bounded context owns and validates its configuration schema. The engine
may cache published snapshots, but a remote configuration call is not required
for every command. Tests and local adapters must provide complete configuration;
the codebase has no hidden operational defaults.

