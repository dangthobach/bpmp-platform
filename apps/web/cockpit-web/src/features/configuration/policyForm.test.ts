import { describe, expect, it } from "vitest";
import { buildPolicy, emptyPolicyForm, policyFields, policyToForm } from "./policyForm";

describe("configuration policy form", () => {
  it("requires every published policy value", () => {
    expect(() => buildPolicy(emptyPolicyForm())).toThrow("required");
  });

  it("round-trips a complete typed policy", () => {
    const form = emptyPolicyForm();
    for (const field of policyFields) form[field.key] = field.key.includes("worker_id") ? "worker-a" : "10";
    form.authorization_audit_key_scope = "tenant/audit";
    form.event_payload_key_scope = "tenant/operational";
    form["retry.multiplier_millis"] = "1000";
    form["workers.outbox_retry.multiplier_millis"] = "1000";
    form["workers.local_task_retry.multiplier_millis"] = "1000";
    const policy = buildPolicy(form);
    expect(policy.default_multi_instance_parallelism).toBe(10);
    expect(policyToForm(policy as unknown as Record<string, unknown>)["boundary.worker_id"]).toBe("worker-a");
  });

  it("builds Human Runtime reliability as typed nested policy", () => {
    const form = emptyPolicyForm("HUMAN_RUNTIME");
    for (const key of Object.keys(form)) form[key] = "10";
    form["engine_retry.multiplier_millis"] = "2000";
    form.engine_retryable_codes = "UNAVAILABLE, DEADLINE_EXCEEDED";

    const policy = buildPolicy(form, "HUMAN_RUNTIME") as Record<string, unknown>;
    expect(policy.engine_retry).toMatchObject({ max_attempts: 10, multiplier_millis: 2000 });
    expect(policy.engine_retryable_codes).toEqual(["UNAVAILABLE", "DEADLINE_EXCEEDED"]);
    expect(policyToForm(policy, "HUMAN_RUNTIME").engine_retryable_codes)
      .toBe("UNAVAILABLE, DEADLINE_EXCEEDED");
  });

  it("builds additive owner policies without flattening retries or arrays", () => {
    const configuration = emptyPolicyForm("CONFIGURATION_SERVICE");
    for (const key of Object.keys(configuration)) configuration[key] = "10";
    configuration["outbox_retry.multiplier_millis"] = "2000";
    expect(buildPolicy(configuration, "CONFIGURATION_SERVICE"))
      .toMatchObject({ outbox_retry: { max_attempts: 10, multiplier_millis: 2000 } });

    const cockpit = emptyPolicyForm("COCKPIT_GATEWAY");
    for (const key of Object.keys(cockpit)) cockpit[key] = "10";
    cockpit.allowed_signal_names = "workflow.updated, work-item.updated";
    cockpit.allowed_origins = "https://cockpit.example.com";
    expect(buildPolicy(cockpit, "COCKPIT_GATEWAY"))
      .toMatchObject({
        allowed_signal_names: ["workflow.updated", "work-item.updated"],
        allowed_origins: ["https://cockpit.example.com"],
      });
  });
});
