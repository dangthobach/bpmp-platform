import type { ConfigurationOwner, ConfigurationPolicy, EngineConfigurationPolicy } from "./types";

export type PolicyForm = Record<string, string>;

export interface PolicyField {
  key: string;
  label: string;
  group: string;
  integer?: boolean;
}

export const policyFields: readonly PolicyField[] = [
  { key: "snapshot_interval_events", label: "Snapshot interval events", group: "Engine", integer: true },
  { key: "max_events_per_decision", label: "Max events per decision", group: "Engine", integer: true },
  { key: "command_timeout_ms", label: "Command timeout (ms)", group: "Engine" },
  { key: "event_payload_key_scope", label: "Event payload key scope", group: "Engine" },
  { key: "authorization_audit_key_scope", label: "Authorization audit key scope", group: "Engine" },
  { key: "max_multi_instance_cardinality", label: "Multi-instance cardinality", group: "Engine", integer: true },
  { key: "default_multi_instance_parallelism", label: "Multi-instance parallelism", group: "Engine", integer: true },
  { key: "retry.max_attempts", label: "Max attempts", group: "Retry", integer: true },
  { key: "retry.initial_backoff_ms", label: "Initial backoff (ms)", group: "Retry" },
  { key: "retry.max_backoff_ms", label: "Max backoff (ms)", group: "Retry" },
  { key: "retry.multiplier_millis", label: "Multiplier millis", group: "Retry", integer: true },
  { key: "wasm.max_module_bytes", label: "Max module bytes", group: "Local WASM" },
  { key: "wasm.max_input_bytes", label: "Max input bytes", group: "Local WASM" },
  { key: "wasm.max_output_bytes", label: "Max output bytes", group: "Local WASM" },
  { key: "wasm.max_memory_bytes", label: "Max memory bytes", group: "Local WASM" },
  { key: "wasm.max_wasm_stack_bytes", label: "Max stack bytes", group: "Local WASM" },
  { key: "wasm.max_table_elements", label: "Max table elements", group: "Local WASM", integer: true },
  { key: "wasm.max_instances", label: "Max instances", group: "Local WASM", integer: true },
  { key: "wasm.max_tables", label: "Max tables", group: "Local WASM", integer: true },
  { key: "wasm.max_memories", label: "Max memories", group: "Local WASM", integer: true },
  { key: "wasm.fuel", label: "Fuel", group: "Local WASM" },
  { key: "boundary.projection_batch_size", label: "Projection batch size", group: "Boundary runtime", integer: true },
  { key: "boundary.dispatch_batch_size", label: "Dispatch batch size", group: "Boundary runtime", integer: true },
  { key: "boundary.max_dispatch_attempts", label: "Dispatch attempts", group: "Boundary runtime", integer: true },
  { key: "boundary.retry_delay_ms", label: "Retry delay (ms)", group: "Boundary runtime" },
  { key: "boundary.lease_duration_ms", label: "Lease duration (ms)", group: "Boundary runtime" },
  { key: "boundary.max_timer_horizon_ms", label: "Timer horizon (ms)", group: "Boundary runtime" },
  { key: "boundary.max_expression_bytes", label: "Max expression bytes", group: "Boundary runtime", integer: true },
  { key: "boundary.worker_id", label: "Worker ID", group: "Boundary runtime" },
  { key: "boundary.max_signal_id_bytes", label: "Max signal ID bytes", group: "Boundary runtime", integer: true },
  { key: "boundary.max_reference_bytes", label: "Max reference bytes", group: "Boundary runtime", integer: true },
  { key: "boundary.max_subscriptions_per_instance", label: "Subscriptions per instance", group: "Boundary runtime", integer: true },
] as const;

const boundedContextFields: Record<Exclude<ConfigurationOwner, "ENGINE">, readonly PolicyField[]> = {
  API_GATEWAY: [
    ["rate_limit_requests", "Rate limit requests"],
    ["rate_limit_window_ms", "Rate limit window (ms)"],
    ["upstream_timeout_ms", "Upstream timeout (ms)"],
    ["circuit_breaker_failure_threshold", "Circuit breaker threshold"],
    ["circuit_breaker_open_ms", "Circuit breaker open (ms)"],
    ["bulkhead_max_concurrency", "Bulkhead concurrency"],
    ["max_request_body_bytes", "Max request body bytes"],
    ["max_upstream_response_bytes", "Max response bytes"],
    ["batch_chunk_size", "Batch chunk size"],
    ["batch_concurrency", "Batch concurrency"],
    ["upstream_retry.max_attempts", "Upstream retry attempts"],
    ["upstream_retry.initial_backoff_ms", "Initial retry backoff (ms)"],
    ["upstream_retry.max_backoff_ms", "Max retry backoff (ms)"],
    ["upstream_retry.multiplier_millis", "Retry multiplier"],
  ].map<PolicyField>((entry) => ({ key: entry[0]!, label: entry[1]!, group: "API Gateway", integer: true }))
    .concat([{ key: "encryption_key_scope", label: "Encryption key scope", group: "API Gateway", integer: false }]),
  HUMAN_RUNTIME: [
    ["projection_batch_size", "Projection batch size"],
    ["escalation_batch_size", "Escalation batch size"],
    ["escalation_lease_ms", "Escalation lease (ms)"],
    ["escalation_retry_ms", "Escalation retry (ms)"],
    ["escalation_poll_ms", "Escalation poll (ms)"],
    ["engine_command_timeout_ms", "Engine command timeout (ms)"],
    ["max_assignment_candidates", "Assignment candidates"],
    ["max_delegation_depth", "Delegation depth"],
    ["query_default_page_size", "Default page size"],
    ["query_max_page_size", "Max page size"],
    ["engine_retry.max_attempts", "Engine retry attempts"],
    ["engine_retry.initial_backoff_ms", "Engine initial backoff (ms)"],
    ["engine_retry.max_backoff_ms", "Engine max backoff (ms)"],
    ["engine_retry.multiplier_millis", "Engine retry multiplier"],
    ["engine_circuit_breaker_failure_threshold", "Engine circuit threshold"],
    ["engine_circuit_breaker_open_ms", "Engine circuit open (ms)"],
  ].map<PolicyField>((entry) => ({ key: entry[0]!, label: entry[1]!, group: "Human Runtime", integer: true }))
    .concat([{
      key: "engine_retryable_codes",
      label: "Engine retryable codes",
      group: "Human Runtime",
      integer: false,
    }]),
  PROJECTION: [
    ["consume_batch_size", "Consume batch size"],
    ["rebuild_batch_size", "Rebuild batch size"],
    ["query_default_page_size", "Default page size"],
    ["query_max_page_size", "Max page size"],
    ["realtime_publish_batch_size", "Realtime publish batch size"],
    ["checkpoint_flush_ms", "Checkpoint flush (ms)"],
    ["max_projection_lag_ms", "Max projection lag (ms)"],
  ].map((entry) => ({ key: entry[0]!, label: entry[1]!, group: "Projection", integer: true })),
  GOVERNANCE: [
    ["approval_ttl_ms", "Approval TTL (ms)", "Governance"],
    ["fresh_authentication_max_age_ms", "Fresh authentication age (ms)", "Governance"],
    ["kms_request_timeout_ms", "KMS request timeout (ms)", "KMS"],
    ["kms_retry.max_attempts", "KMS retry attempts", "KMS"],
    ["kms_retry.initial_backoff_ms", "KMS initial backoff (ms)", "KMS"],
    ["kms_retry.max_backoff_ms", "KMS max backoff (ms)", "KMS"],
    ["kms_retry.multiplier_millis", "KMS retry multiplier", "KMS"],
    ["key_cache_ttl_ms", "Key cache TTL (ms)", "KMS"],
    ["revocation_barrier_timeout_ms", "Revocation barrier timeout (ms)", "Governance"],
    ["reconciliation_batch_size", "Reconciliation batch size", "Governance"],
    ["max_pending_compensations", "Pending compensation limit", "Governance"],
  ].map((entry) => ({ key: entry[0]!, label: entry[1]!, group: entry[2]!, integer: true })),
};

export function policyFieldsFor(owner: ConfigurationOwner): readonly PolicyField[] {
  return owner === "ENGINE" ? policyFields : boundedContextFields[owner];
}

export function emptyPolicyForm(owner: ConfigurationOwner = "ENGINE"): PolicyForm {
  return Object.fromEntries(policyFieldsFor(owner).map(({ key }) => [key, ""]));
}

export function buildPolicy(form: PolicyForm, owner: ConfigurationOwner = "ENGINE"): ConfigurationPolicy {
  const fields = policyFieldsFor(owner);
  for (const field of fields) {
    if (!form[field.key]?.trim()) throw new Error(`${field.label} is required`);
  }
  const positive = (key: string) => {
    const value = Number(form[key]);
    if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${key} must be a positive integer`);
    return value;
  };
  const text = (key: string) => form[key]!.trim();
  if (owner !== "ENGINE") {
    const flat = Object.fromEntries(fields.map(({ key, integer }) => [
      key,
      integer === false ? text(key) : positive(key),
    ]));
    const value = (key: string) => {
      const result = flat[key];
      if (typeof result !== "number") throw new Error(`${key} is required`);
      return result;
    };
    if (owner === "API_GATEWAY" && value("batch_concurrency") > value("batch_chunk_size")) {
      throw new Error("Batch concurrency cannot exceed chunk size");
    }
    if (owner === "PROJECTION" && value("query_max_page_size") < value("query_default_page_size")) {
      throw new Error("Max page size cannot be less than default page size");
    }
    if (owner === "HUMAN_RUNTIME" && value("query_max_page_size") < value("query_default_page_size")) {
      throw new Error("Max page size cannot be less than default page size");
    }
    if (owner === "API_GATEWAY") {
      if (value("upstream_retry.max_backoff_ms") < value("upstream_retry.initial_backoff_ms")) {
        throw new Error("Upstream max backoff cannot be less than initial backoff");
      }
      if (value("upstream_retry.multiplier_millis") < 1000) {
        throw new Error("Upstream retry multiplier must be at least 1000");
      }
      const upstreamRetry = Object.fromEntries(Object.entries(flat)
        .filter(([key]) => key.startsWith("upstream_retry."))
        .map(([key, entry]) => [key.slice(15), entry]));
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) => !key.startsWith("upstream_retry."))),
        upstream_retry: upstreamRetry,
      };
    }
    if (owner === "GOVERNANCE") {
      if (value("kms_retry.max_backoff_ms") < value("kms_retry.initial_backoff_ms")) {
        throw new Error("KMS max backoff cannot be less than initial backoff");
      }
      if (value("kms_retry.multiplier_millis") < 1000) {
        throw new Error("KMS retry multiplier must be at least 1000");
      }
      const kmsRetry = Object.fromEntries(Object.entries(flat)
        .filter(([key]) => key.startsWith("kms_retry."))
        .map(([key, value]) => [key.slice(10), value]));
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) => !key.startsWith("kms_retry."))),
        kms_retry: kmsRetry,
      };
    }
    if (owner === "HUMAN_RUNTIME") {
      if (value("engine_retry.max_backoff_ms") < value("engine_retry.initial_backoff_ms")) {
        throw new Error("Engine max backoff cannot be less than initial backoff");
      }
      if (value("engine_retry.multiplier_millis") < 1000) {
        throw new Error("Engine retry multiplier must be at least 1000");
      }
      const retryableCodes = text("engine_retryable_codes")
        .split(",")
        .map((code) => code.trim())
        .filter(Boolean);
      if (retryableCodes.length === 0) throw new Error("At least one engine retryable code is required");
      const engineRetry = Object.fromEntries(Object.entries(flat)
        .filter(([key]) => key.startsWith("engine_retry."))
        .map(([key, entry]) => [key.slice(13), entry]));
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) =>
          !key.startsWith("engine_retry.") && key !== "engine_retryable_codes")),
        engine_retry: engineRetry,
        engine_retryable_codes: retryableCodes,
      };
    }
    return flat;
  }
  const cardinality = positive("max_multi_instance_cardinality");
  const parallelism = positive("default_multi_instance_parallelism");
  if (parallelism > cardinality) throw new Error("Parallelism cannot exceed cardinality");
  const initialBackoff = positive("retry.initial_backoff_ms");
  const maxBackoff = positive("retry.max_backoff_ms");
  if (maxBackoff < initialBackoff) throw new Error("Max backoff cannot be less than initial backoff");
  if (positive("retry.multiplier_millis") < 1000) throw new Error("Retry multiplier must be at least 1000");
  const engine: EngineConfigurationPolicy = {
    snapshot_interval_events: positive("snapshot_interval_events"),
    max_events_per_decision: positive("max_events_per_decision"),
    command_timeout_ms: String(positive("command_timeout_ms")),
    event_payload_key_scope: text("event_payload_key_scope"),
    optimistic_conflict_retry: {
      max_attempts: positive("retry.max_attempts"),
      initial_backoff_ms: String(initialBackoff),
      max_backoff_ms: String(maxBackoff),
      multiplier_millis: positive("retry.multiplier_millis"),
    },
    local_wasm: Object.fromEntries(policyFields
      .filter(({ key }) => key.startsWith("wasm."))
      .map(({ key, integer }) => [key.slice(5), integer ? positive(key) : String(positive(key))])),
    authorization_audit_key_scope: text("authorization_audit_key_scope"),
    max_multi_instance_cardinality: cardinality,
    default_multi_instance_parallelism: parallelism,
    boundary_runtime: Object.fromEntries(policyFields
      .filter(({ key }) => key.startsWith("boundary."))
      .map(({ key, integer }) => [
        key.slice(9),
        key === "boundary.worker_id" ? text(key) : integer ? positive(key) : String(positive(key)),
      ])),
  };
  return engine;
}

export function policyToForm(
  policy: Record<string, unknown>,
  owner: ConfigurationOwner = "ENGINE",
): PolicyForm {
  const form = emptyPolicyForm(owner);
  if (owner !== "ENGINE") {
    const kmsRetry = record(policy.kms_retry);
    const upstreamRetry = record(policy.upstream_retry);
    const engineRetry = record(policy.engine_retry);
    for (const field of policyFieldsFor(owner)) {
      const value = field.key.startsWith("kms_retry.")
        ? kmsRetry[field.key.slice(10)]
        : field.key.startsWith("upstream_retry.")
          ? upstreamRetry[field.key.slice(15)]
          : field.key.startsWith("engine_retry.")
            ? engineRetry[field.key.slice(13)]
            : policy[field.key];
      form[field.key] = Array.isArray(value)
        ? value.join(", ")
        : value === undefined || value === null ? "" : String(value);
    }
    return form;
  }
  const retry = record(policy.optimistic_conflict_retry);
  const wasm = record(policy.local_wasm);
  const boundary = record(policy.boundary_runtime);
  for (const field of policyFields) {
    const value = field.key.startsWith("retry.")
      ? retry[field.key.slice(6)]
      : field.key.startsWith("wasm.")
        ? wasm[field.key.slice(5)]
        : field.key.startsWith("boundary.")
          ? boundary[field.key.slice(9)]
          : policy[field.key];
    form[field.key] = value === undefined || value === null ? "" : String(value);
  }
  return form;
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
}
