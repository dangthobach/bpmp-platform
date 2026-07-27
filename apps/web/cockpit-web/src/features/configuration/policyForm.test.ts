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
    const policy = buildPolicy(form);
    expect(policy.default_multi_instance_parallelism).toBe(10);
    expect(policyToForm(policy as unknown as Record<string, unknown>)["boundary.worker_id"]).toBe("worker-a");
  });
});
