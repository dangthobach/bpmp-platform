import { createPublicKey, verify } from "node:crypto";
import { readFile } from "node:fs/promises";

const options = parseArguments(process.argv.slice(2));
const [shellConfig, catalog] = await Promise.all([
  readJSON(options.config),
  readJSON(options.catalog),
]);
const now = options.now === undefined ? new Date() : new Date(options.now);
if (Number.isNaN(now.getTime())) fail("--now must be an ISO-8601 timestamp");
validateShellConfig(shellConfig);
validateCatalog(catalog, shellConfig, now);
process.stdout.write(`Verified ODS catalog ${catalog.catalogVersion} with key ${catalog.keyId}\n`);

function parseArguments(args) {
  const result = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index];
    const value = args[index + 1];
    if (!["--config", "--catalog", "--now"].includes(name) || value === undefined) {
      fail("usage: node tools/verify-ods-catalog.mjs --config <file> --catalog <file> [--now <ISO-8601>]");
    }
    result[name.slice(2)] = value;
  }
  if (!result.config || !result.catalog) fail("--config and --catalog are required");
  return result;
}

async function readJSON(path) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch (error) {
    fail(`cannot read ${path}: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function validateShellConfig(value) {
  if (!record(value) || value.schemaVersion !== "1" || typeof value.shellVersion !== "string"
    || value.catalogUrl !== "/mfe-catalog.json" || !Array.isArray(value.allowedRemoteOrigins)
    || !record(value.catalogVerificationKeys)) fail("invalid shell configuration");
  for (const field of ["catalogTimeoutMs", "remoteTimeoutMs", "apiTimeoutMs", "maxApiResponseBytes", "maxCatalogLifetimeMs"]) {
    if (!Number.isSafeInteger(value[field]) || value[field] <= 0) fail(`invalid shell field ${field}`);
  }
  if (value.maxCatalogLifetimeMs > 7 * 24 * 60 * 60 * 1_000) fail("catalog lifetime policy exceeds seven days");
  if (value.telemetryTraceEndpoint !== undefined
    && (typeof value.telemetryTraceEndpoint !== "string"
      || !value.telemetryTraceEndpoint.startsWith("/")
      || value.telemetryTraceEndpoint.startsWith("//"))) {
    fail("telemetry trace endpoint must be same-origin");
  }
  for (const origin of value.allowedRemoteOrigins) {
    if (typeof origin !== "string" || new URL(origin).origin !== origin) fail(`invalid allowed remote origin ${origin}`);
  }
}

function validateCatalog(value, config, now) {
  if (!record(value) || value.schemaVersion !== "1" || typeof value.catalogVersion !== "string"
    || typeof value.generatedAt !== "string" || typeof value.expiresAt !== "string"
    || typeof value.keyId !== "string" || typeof value.signature !== "string"
    || !Array.isArray(value.remotes)) fail("invalid signed catalog envelope");
  const generatedAt = Date.parse(value.generatedAt);
  const expiresAt = Date.parse(value.expiresAt);
  if (!Number.isFinite(generatedAt) || !Number.isFinite(expiresAt)) fail("invalid catalog time window");
  if (generatedAt > now.getTime() + 60_000 || expiresAt <= now.getTime()) fail("catalog is not currently valid");
  if (expiresAt <= generatedAt || expiresAt - generatedAt > config.maxCatalogLifetimeMs) fail("catalog lifetime exceeds policy");
  const allowedOrigins = new Set(config.allowedRemoteOrigins);
  const names = new Set();
  for (const remote of value.remotes) {
    if (!record(remote) || !/^[a-z][a-z0-9-]{1,63}$/.test(remote.name)
      || typeof remote.entry !== "string" || typeof remote.exposedModule !== "string"
      || typeof remote.contractVersion !== "string" || typeof remote.minShellVersion !== "string"
      || !Array.isArray(remote.requiredCapabilities)
      || remote.requiredCapabilities.some((capability) => typeof capability !== "string")) fail("invalid remote entry");
    const entry = new URL(remote.entry);
    if (!allowedOrigins.has(entry.origin) || !entry.pathname.endsWith("/mf-manifest.json")) fail(`remote ${remote.name} is not allowlisted`);
    if (names.has(remote.name)) fail(`duplicate remote ${remote.name}`);
    names.add(remote.name);
  }
  const key = config.catalogVerificationKeys[value.keyId];
  if (!record(key) || key.kty !== "OKP" || key.crv !== "Ed25519") fail("catalog key is not configured");
  const payload = {
    catalogVersion: value.catalogVersion,
    expiresAt: value.expiresAt,
    generatedAt: value.generatedAt,
    keyId: value.keyId,
    remotes: value.remotes.map((remote) => ({
      contractVersion: remote.contractVersion,
      entry: remote.entry,
      exposedModule: remote.exposedModule,
      integrity: remote.integrity ?? "",
      minShellVersion: remote.minShellVersion,
      name: remote.name,
      requiredCapabilities: [...remote.requiredCapabilities],
    })),
    schemaVersion: value.schemaVersion,
  };
  let valid = false;
  try {
    valid = verify(
      null,
      Buffer.from(JSON.stringify(payload)),
      createPublicKey({ key, format: "jwk" }),
      Buffer.from(value.signature, "base64url"),
    );
  } catch {
    valid = false;
  }
  if (!valid) fail("catalog signature is invalid");
}

function record(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}
