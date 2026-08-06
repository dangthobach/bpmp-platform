interface AccessTokenClaims {
  tenant_id?: unknown;
  exp?: unknown;
}

export function validateSessionInput(
  tenantId: string,
  accessToken: string,
  nowEpochSeconds = Math.floor(Date.now() / 1_000),
): string | null {
  if (!tenantId || !accessToken) return "Tenant ID and access token are required";

  const claims = decodeClaims(accessToken);
  if (!claims) return "Access token is not a valid JWT";
  if (typeof claims.tenant_id !== "string" || claims.tenant_id !== tenantId) {
    return "Tenant ID does not match the access token";
  }
  if (typeof claims.exp !== "number" || !Number.isFinite(claims.exp)) {
    return "Access token does not contain a valid expiry";
  }
  if (claims.exp <= nowEpochSeconds) return "Access token has expired";
  return null;
}

function decodeClaims(accessToken: string): AccessTokenClaims | null {
  const parts = accessToken.split(".");
  const payload = parts[1];
  if (parts.length !== 3 || !payload) return null;
  try {
    const normalized = payload.replace(/-/g, "+").replace(/_/g, "/");
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
    const value: unknown = JSON.parse(atob(padded));
    return value !== null && typeof value === "object" ? value as AccessTokenClaims : null;
  } catch {
    return null;
  }
}
