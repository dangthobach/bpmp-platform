export interface ShellConfig {
  shellVersion: string;
  catalogUrl: string;
  catalogTimeoutMs: number;
  remoteTimeoutMs: number;
  apiTimeoutMs: number;
  maxApiResponseBytes: number;
  maxCatalogLifetimeMs: number;
  allowedRemoteOrigins: ReadonlySet<string>;
  allowUnsignedCatalog: boolean;
  catalogVerificationKeys: Readonly<Record<string, JsonWebKey>>;
  telemetryTraceEndpoint?: string;
}

interface ShellConfigDocument {
  schemaVersion: "1";
  shellVersion: string;
  catalogUrl: string;
  catalogTimeoutMs: number;
  remoteTimeoutMs: number;
  apiTimeoutMs: number;
  maxApiResponseBytes: number;
  maxCatalogLifetimeMs: number;
  allowedRemoteOrigins: string[];
  catalogVerificationKeys: Record<string, JsonWebKey>;
  telemetryTraceEndpoint?: string;
}

export async function loadShellConfig(
  fetchConfig: typeof fetch = fetch,
  currentOrigin: string = location.origin,
  allowDevelopmentUnsigned: boolean = import.meta.env.DEV,
): Promise<ShellConfig> {
  const response = await fetchConfig("/shell-config.json", {
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error(`Shell configuration request failed with ${response.status}`);
  const document = parseShellConfigDocument(await response.json());
  if (!allowDevelopmentUnsigned && document.maxCatalogLifetimeMs > 7 * 24 * 60 * 60 * 1_000) {
    throw new TypeError("production catalog lifetime policy exceeds seven days");
  }
  const catalogURL = new URL(document.catalogUrl, currentOrigin);
  if (catalogURL.origin !== currentOrigin) throw new TypeError("catalogUrl must be same-origin");
  const origins = new Set(document.allowedRemoteOrigins.map((value) => new URL(value).origin));
  origins.add(currentOrigin);
  const telemetryEndpoint = document.telemetryTraceEndpoint === undefined
    ? undefined
    : new URL(document.telemetryTraceEndpoint, currentOrigin);
  if (telemetryEndpoint !== undefined && telemetryEndpoint.origin !== currentOrigin) {
    throw new TypeError("telemetryTraceEndpoint must be same-origin");
  }
  const config: ShellConfig = {
    shellVersion: document.shellVersion,
    catalogUrl: catalogURL.pathname + catalogURL.search,
    catalogTimeoutMs: document.catalogTimeoutMs,
    remoteTimeoutMs: document.remoteTimeoutMs,
    apiTimeoutMs: document.apiTimeoutMs,
    maxApiResponseBytes: document.maxApiResponseBytes,
    maxCatalogLifetimeMs: document.maxCatalogLifetimeMs,
    allowedRemoteOrigins: origins,
    allowUnsignedCatalog: allowDevelopmentUnsigned,
    catalogVerificationKeys: Object.freeze({ ...document.catalogVerificationKeys }),
  };
  if (telemetryEndpoint !== undefined) {
    config.telemetryTraceEndpoint = telemetryEndpoint.pathname + telemetryEndpoint.search;
  }
  return config;
}

function parseShellConfigDocument(value: unknown): ShellConfigDocument {
  if (!isRecord(value) || value.schemaVersion !== "1" || typeof value.shellVersion !== "string"
    || typeof value.catalogUrl !== "string" || !Array.isArray(value.allowedRemoteOrigins)
    || !isRecord(value.catalogVerificationKeys)) {
    throw new TypeError("invalid Shell configuration");
  }
  for (const field of ["catalogTimeoutMs", "remoteTimeoutMs", "apiTimeoutMs", "maxApiResponseBytes", "maxCatalogLifetimeMs"] as const) {
    if (!Number.isSafeInteger(value[field]) || (value[field] as number) <= 0) {
      throw new TypeError(`invalid Shell configuration field: ${field}`);
    }
  }
  if (value.allowedRemoteOrigins.some((origin) => typeof origin !== "string")) {
    throw new TypeError("allowedRemoteOrigins must contain strings");
  }
  for (const [keyID, key] of Object.entries(value.catalogVerificationKeys)) {
    if (!/^[A-Za-z0-9_.:-]{1,128}$/.test(keyID) || !isRecord(key) || key.kty !== "OKP" || key.crv !== "Ed25519") {
      throw new TypeError("catalog verification key must be an Ed25519 JWK");
    }
  }
  if (value.telemetryTraceEndpoint !== undefined && typeof value.telemetryTraceEndpoint !== "string") {
    throw new TypeError("telemetryTraceEndpoint must be a string");
  }
  return value as unknown as ShellConfigDocument;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
