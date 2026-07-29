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
  { key: "workers.poll_interval_ms", label: "Poll interval (ms)", group: "Workers" },
  { key: "workers.outbox_batch_size", label: "Outbox batch size", group: "Workers", integer: true },
  { key: "workers.outbox_retry.max_attempts", label: "Outbox retry attempts", group: "Workers", integer: true },
  { key: "workers.outbox_retry.initial_backoff_ms", label: "Outbox initial backoff (ms)", group: "Workers" },
  { key: "workers.outbox_retry.max_backoff_ms", label: "Outbox max backoff (ms)", group: "Workers" },
  { key: "workers.outbox_retry.multiplier_millis", label: "Outbox retry multiplier", group: "Workers", integer: true },
  { key: "workers.local_task_batch_size", label: "Local task batch size", group: "Workers", integer: true },
  { key: "workers.local_task_retry.max_attempts", label: "Local task retry attempts", group: "Workers", integer: true },
  { key: "workers.local_task_retry.initial_backoff_ms", label: "Local task initial backoff (ms)", group: "Workers" },
  { key: "workers.local_task_retry.max_backoff_ms", label: "Local task max backoff (ms)", group: "Workers" },
  { key: "workers.local_task_retry.multiplier_millis", label: "Local task retry multiplier", group: "Workers", integer: true },
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
    ["required_approver_count", "Required approver count", "Governance"],
  ].map((entry) => ({ key: entry[0]!, label: entry[1]!, group: entry[2]!, integer: true }))
    .concat([
      { key: "abort_capability", label: "Abort capability", group: "Governance", integer: false },
      { key: "accepted_auth_assurance", label: "Accepted assurance (comma separated)", group: "Governance", integer: false },
      { key: "approval_keys_json", label: "Approval keys (JSON array)", group: "Governance", integer: false },
    ]),
  CONFIGURATION_SERVICE: [
    ["outbox_batch_size", "Outbox batch size"],
    ["outbox_lease_ms", "Outbox lease (ms)"],
    ["outbox_poll_ms", "Outbox poll (ms)"],
    ["outbox_retry.max_attempts", "Outbox retry attempts"],
    ["outbox_retry.initial_backoff_ms", "Initial retry backoff (ms)"],
    ["outbox_retry.max_backoff_ms", "Max retry backoff (ms)"],
    ["outbox_retry.multiplier_millis", "Retry multiplier"],
    ["query_default_page_size", "Default page size"],
    ["query_max_page_size", "Max page size"],
    ["max_request_body_bytes", "Max request body bytes"],
  ].map((entry) => ({ key: entry[0]!, label: entry[1]!, group: "Configuration Service", integer: true })),
  COCKPIT_GATEWAY: [
    ["max_names_per_connection", "Names per connection"],
    ["max_signal_names_bytes", "Signal names bytes"],
    ["max_connections", "Max connections"],
    ["max_subscriptions", "Max subscriptions"],
    ["outbound_buffer_size", "Outbound buffer size"],
    ["replay_size_per_stream", "Replay size per stream"],
    ["max_replay_streams", "Max replay streams"],
    ["heartbeat_interval_ms", "Heartbeat interval (ms)"],
    ["consume_batch_size", "Consume batch size"],
  ].map<PolicyField>((entry) => ({
    key: entry[0]!, label: entry[1]!, group: "Cockpit Gateway", integer: true,
  })).concat([
    { key: "allowed_signal_names", label: "Allowed signals (comma separated)", group: "Cockpit Gateway", integer: false },
    { key: "allowed_origins", label: "Allowed origins (comma separated)", group: "Cockpit Gateway", integer: false },
  ]),
  AUTHZ_CONTROL_PLANE: [
    ["request_timeout_ms", "Request timeout (ms)"],
    ["connect_timeout_ms", "Connect timeout (ms)"],
    ["http_pool_max_idle_per_host", "HTTP idle connections per host"],
    ["graph_max_depth", "Graph max depth"],
    ["graph_memo_capacity", "Graph memo capacity"],
    ["graph_memo_ttl_ms", "Graph memo TTL (ms)"],
    ["inactive_user_days", "Inactive user days"],
    ["inactive_user_batch_size", "Inactive user batch size"],
    ["inactive_user_poll_ms", "Inactive user poll (ms)"],
    ["inactive_user_retry.max_attempts", "Inactive user retry attempts"],
    ["inactive_user_retry.initial_backoff_ms", "Initial retry backoff (ms)"],
    ["inactive_user_retry.max_backoff_ms", "Max retry backoff (ms)"],
    ["inactive_user_retry.multiplier_millis", "Retry multiplier"],
  ].map((entry) => ({ key: entry[0]!, label: entry[1]!, group: "AuthZ Control Plane", integer: true })),
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
      const assurance = text("accepted_auth_assurance")
        .split(",").map((entry) => entry.trim()).filter(Boolean);
      if (assurance.length === 0) throw new Error("Accepted assurance is required");
      let approvalKeys: unknown;
      try {
        approvalKeys = JSON.parse(text("approval_keys_json"));
      } catch {
        throw new Error("Approval keys must be valid JSON");
      }
      if (!Array.isArray(approvalKeys) || approvalKeys.length === 0) {
        throw new Error("At least one approval key is required");
      }
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) =>
          !key.startsWith("kms_retry.") &&
          key !== "accepted_auth_assurance" &&
          key !== "approval_keys_json")),
        kms_retry: kmsRetry,
        accepted_auth_assurance: assurance,
        approval_keys: approvalKeys,
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
    if (owner === "CONFIGURATION_SERVICE") {
      if (value("query_max_page_size") < value("query_default_page_size")) {
        throw new Error("Max page size cannot be less than default page size");
      }
      const outboxRetry = retryObject(flat, "outbox_retry.", value);
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) =>
          !key.startsWith("outbox_retry."))),
        outbox_retry: outboxRetry,
      };
    }
    if (owner === "COCKPIT_GATEWAY") {
      const allowedSignalNames = csv(text("allowed_signal_names"));
      const allowedOrigins = csv(text("allowed_origins"));
      if (allowedSignalNames.length === 0 || allowedOrigins.length === 0) {
        throw new Error("Allowed signals and origins are required");
      }
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) =>
          key !== "allowed_signal_names" && key !== "allowed_origins")),
        allowed_signal_names: allowedSignalNames,
        allowed_origins: allowedOrigins,
      };
    }
    if (owner === "AUTHZ_CONTROL_PLANE") {
      const inactiveUserRetry = retryObject(flat, "inactive_user_retry.", value);
      return {
        ...Object.fromEntries(Object.entries(flat).filter(([key]) =>
          !key.startsWith("inactive_user_retry."))),
        inactive_user_retry: inactiveUserRetry,
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
  const outboxInitial = positive("workers.outbox_retry.initial_backoff_ms");
  const outboxMax = positive("workers.outbox_retry.max_backoff_ms");
  const localInitial = positive("workers.local_task_retry.initial_backoff_ms");
  const localMax = positive("workers.local_task_retry.max_backoff_ms");
  if (outboxMax < outboxInitial || localMax < localInitial) {
    throw new Error("Worker max backoff cannot be less than initial backoff");
  }
  if (positive("workers.outbox_retry.multiplier_millis") < 1000 ||
    positive("workers.local_task_retry.multiplier_millis") < 1000) {
    throw new Error("Worker retry multiplier must be at least 1000");
  }
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
    workers: {
      poll_interval_ms: String(positive("workers.poll_interval_ms")),
      outbox_batch_size: positive("workers.outbox_batch_size"),
      outbox_retry: {
        max_attempts: positive("workers.outbox_retry.max_attempts"),
        initial_backoff_ms: String(outboxInitial),
        max_backoff_ms: String(outboxMax),
        multiplier_millis: positive("workers.outbox_retry.multiplier_millis"),
      },
      local_task_batch_size: positive("workers.local_task_batch_size"),
      local_task_retry: {
        max_attempts: positive("workers.local_task_retry.max_attempts"),
        initial_backoff_ms: String(localInitial),
        max_backoff_ms: String(localMax),
        multiplier_millis: positive("workers.local_task_retry.multiplier_millis"),
      },
    },
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
    const outboxRetry = record(policy.outbox_retry);
    const inactiveUserRetry = record(policy.inactive_user_retry);
    for (const field of policyFieldsFor(owner)) {
      const value = field.key.startsWith("kms_retry.")
        ? kmsRetry[field.key.slice(10)]
        : field.key.startsWith("upstream_retry.")
          ? upstreamRetry[field.key.slice(15)]
          : field.key.startsWith("engine_retry.")
            ? engineRetry[field.key.slice(13)]
            : field.key.startsWith("outbox_retry.")
              ? outboxRetry[field.key.slice(13)]
              : field.key.startsWith("inactive_user_retry.")
                ? inactiveUserRetry[field.key.slice(20)]
                : field.key === "approval_keys_json"
                  ? JSON.stringify(policy.approval_keys ?? [], null, 2)
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
  const workers = record(policy.workers);
  const outboxRetry = record(workers.outbox_retry);
  const localTaskRetry = record(workers.local_task_retry);
  for (const field of policyFields) {
    const value = field.key.startsWith("retry.")
      ? retry[field.key.slice(6)]
      : field.key.startsWith("wasm.")
        ? wasm[field.key.slice(5)]
        : field.key.startsWith("boundary.")
          ? boundary[field.key.slice(9)]
          : field.key.startsWith("workers.outbox_retry.")
            ? outboxRetry[field.key.slice(21)]
            : field.key.startsWith("workers.local_task_retry.")
              ? localTaskRetry[field.key.slice(25)]
              : field.key.startsWith("workers.")
                ? workers[field.key.slice(8)]
          : policy[field.key];
    form[field.key] = value === undefined || value === null ? "" : String(value);
  }
  return form;
}

function retryObject(
  flat: Record<string, string | number>,
  prefix: string,
  value: (key: string) => number,
) {
  const initial = value(`${prefix}initial_backoff_ms`);
  const maximum = value(`${prefix}max_backoff_ms`);
  if (maximum < initial || value(`${prefix}multiplier_millis`) < 1000) {
    throw new Error("Retry backoff or multiplier is invalid");
  }
  return Object.fromEntries(Object.entries(flat)
    .filter(([key]) => key.startsWith(prefix))
    .map(([key, entry]) => [key.slice(prefix.length), entry]));
}

function csv(value: string): string[] {
  return value.split(",").map((entry) => entry.trim()).filter(Boolean);
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null ? value as Record<string, unknown> : {};
}
