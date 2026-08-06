// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { validateSessionInput } from "./sessionValidation";

function token(claims: Record<string, unknown>) {
  const encode = (value: object) => btoa(JSON.stringify(value))
    .replace(/=/g, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_");
  return `${encode({ alg: "EdDSA", typ: "JWT" })}.${encode(claims)}.signature`;
}

describe("validateSessionInput", () => {
  it("accepts a non-expired token for the selected tenant", () => {
    expect(validateSessionInput("tenant-a", token({ tenant_id: "tenant-a", exp: 2_000 }), 1_000))
      .toBeNull();
  });

  it("rejects a tenant mismatch before creating a session", () => {
    expect(validateSessionInput("tenant-b", token({ tenant_id: "tenant-a", exp: 2_000 }), 1_000))
      .toBe("Tenant ID does not match the access token");
  });

  it("rejects malformed and expired tokens", () => {
    expect(validateSessionInput("tenant-a", "not-a-jwt", 1_000))
      .toBe("Access token is not a valid JWT");
    expect(validateSessionInput("tenant-a", token({ tenant_id: "tenant-a", exp: 999 }), 1_000))
      .toBe("Access token has expired");
  });
});
