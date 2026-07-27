import type { ConfigurationPolicy } from "./types";

export type PolicyForm = Record<string, string>;

export interface PolicyField {
  key: string;
  label: string;
  group: "Engine" | "Retry" | "Local WASM" | "Boundary runtime";
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

export function emptyPolicyForm(): PolicyForm {
  return Object.fromEntries(policyFields.map(({ key }) => [key, ""]));
}

export function buildPolicy(form: PolicyForm): ConfigurationPolicy {
  for (const field of policyFields) {
    if (!form[field.key]?.trim()) throw new Error(`${field.label} is required`);
  }
  const positive = (key: string) => {
    const value = Number(form[key]);
    if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${key} must be a positive integer`);
    return value;
  };
  const text = (key: string) => form[key]!.trim();
  const cardinality = positive("max_multi_instance_cardinality");
  const parallelism = positive("default_multi_instance_parallelism");
  if (parallelism > cardinality) throw new Error("Parallelism cannot exceed cardinality");
  const initialBackoff = positive("retry.initial_backoff_ms");
  const maxBackoff = positive("retry.max_backoff_ms");
  if (maxBackoff < initialBackoff) throw new Error("Max backoff cannot be less than initial backoff");
  if (positive("retry.multiplier_millis") < 1000) throw new Error("Retry multiplier must be at least 1000");
  return {
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
}

export function policyToForm(policy: Record<string, unknown>): PolicyForm {
  const form = emptyPolicyForm();
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
