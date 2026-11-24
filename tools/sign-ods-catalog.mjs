import { createPrivateKey, createPublicKey, sign } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";

const options = parseArgs(process.argv.slice(2));
const [config, catalog, privateKeyPem] = await Promise.all([
  readJson(options.config), readJson(options.catalog), readFile(options.key, "utf8"),
]);
const privateKey = createPrivateKey(privateKeyPem);
if (privateKey.asymmetricKeyType !== "ed25519") fail("private key must be Ed25519");
const publicJwk = createPublicKey(privateKey).export({ format: "jwk" });
const now = new Date();
const lifetimeMs = Number(options.lifetime_hours ?? 24) * 60 * 60 * 1000;
if (!Number.isSafeInteger(lifetimeMs) || lifetimeMs < 60_000 || lifetimeMs > 7 * 24 * 60 * 60 * 1000) {
  fail("--lifetime-hours must be between 1/60 and 168");
}

config.catalogVerificationKeys = { ...config.catalogVerificationKeys, [options.key_id]: publicJwk };
if (options.remote_origin) {
  const origin = new URL(options.remote_origin).origin;
  if (origin !== options.remote_origin) fail("--remote-origin must be an origin without a path");
  config.allowedRemoteOrigins = [origin];
  catalog.remotes = catalog.remotes.map((remote) => ({
    ...remote,
    entry: `${origin}${new URL(remote.entry).pathname}`,
  }));
}
catalog.keyId = options.key_id;
catalog.catalogVersion = options.version ?? `local-${now.toISOString().replaceAll(/[-:.TZ]/g, "").slice(0, 14)}`;
catalog.generatedAt = now.toISOString();
catalog.expiresAt = new Date(now.getTime() + lifetimeMs).toISOString();
delete catalog.signature;
const payload = signedPayload(catalog);
catalog.signature = sign(null, Buffer.from(JSON.stringify(payload)), privateKey).toString("base64url");
await Promise.all([
  writeFile(options.out_config, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 }),
  writeFile(options.out_catalog, `${JSON.stringify(catalog, null, 2)}\n`, { mode: 0o600 }),
]);
process.stdout.write(`Signed ${catalog.catalogVersion} with ${options.key_id}\n`);

function signedPayload(value) {
  return {
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
      ...(remote.routeBasePath === undefined ? {} : { routeBasePath: remote.routeBasePath }),
    })),
    schemaVersion: value.schemaVersion,
  };
}

function parseArgs(args) {
  const result = {};
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index];
    const value = args[index + 1];
    if (!["--config", "--catalog", "--key", "--key-id", "--out-config", "--out-catalog", "--version", "--lifetime-hours", "--remote-origin"].includes(name) || value === undefined) {
      fail("usage: node tools/sign-ods-catalog.mjs --config <json> --catalog <json> --key <pem> --key-id <id> --out-config <json> --out-catalog <json> [--version <version>] [--lifetime-hours <1..168] [--remote-origin <origin>]");
    }
    result[name.slice(2).replaceAll("-", "_")] = value;
  }
  for (const field of ["config", "catalog", "key", "key_id", "out_config", "out_catalog"]) {
    if (!result[field]) fail(`--${field.replaceAll("_", "-")} is required`);
  }
  if (!/^[a-zA-Z0-9._-]{1,64}$/.test(result.key_id)) fail("invalid --key-id");
  return result;
}

async function readJson(path) {
  try { return JSON.parse(await readFile(path, "utf8")); }
  catch (error) { fail(`cannot read ${path}: ${error instanceof Error ? error.message : String(error)}`); }
}

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}
