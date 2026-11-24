import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { generateKeyPairSync, sign } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const configPath = "platform/ods/helm/ods-foundation/ci/shell-config.json";
const catalogPath = "platform/ods/helm/ods-foundation/ci/mfe-catalog.json";
const verifier = "tools/verify-ods-catalog.mjs";
const fixedNow = "2026-10-29T12:00:00Z";

test("accepts an allowlisted, current and correctly signed catalog", () => {
  const output = execFileSync(process.execPath, [
    verifier, "--config", configPath, "--catalog", catalogPath, "--now", fixedNow,
  ], { encoding: "utf8" });
  assert.match(output, /Verified ODS catalog ci-1/);
});

test("rejects a cross-origin telemetry endpoint", () => {
  const directory = mkdtempSync(join(tmpdir(), "ods-config-test-"));
  try {
    const config = JSON.parse(readFileSync(configPath, "utf8"));
    config.telemetryTraceEndpoint = "https://collector.evil.example/v1/traces";
    const candidate = join(directory, "shell-config.json");
    writeFileSync(candidate, JSON.stringify(config));
    const result = spawnSync(process.execPath, [
      verifier, "--config", candidate, "--catalog", catalogPath, "--now", fixedNow,
    ], { encoding: "utf8" });
    assert.notEqual(result.status, 0, "cross-origin telemetry endpoint unexpectedly passed");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("rejects a signed catalog when the configured Shell is too old", () => {
  const directory = mkdtempSync(join(tmpdir(), "ods-compat-test-"));
  try {
    const config = JSON.parse(readFileSync(configPath, "utf8"));
    config.shellVersion = "0.9";
    const candidate = join(directory, "shell-config.json");
    writeFileSync(candidate, JSON.stringify(config));
    const result = spawnSync(process.execPath, [
      verifier, "--config", candidate, "--catalog", catalogPath, "--now", fixedNow,
    ], { encoding: "utf8" });
    assert.notEqual(result.status, 0, "newer remote unexpectedly passed an older Shell gate");
    assert.match(result.stderr, /requires a newer shell/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("accepts a signed route base and rejects route tampering", () => {
  const directory = mkdtempSync(join(tmpdir(), "ods-route-catalog-test-"));
  try {
    const config = JSON.parse(readFileSync(configPath, "utf8"));
    const catalog = JSON.parse(readFileSync(catalogPath, "utf8"));
    const { privateKey, publicKey } = generateKeyPairSync("ed25519");
    const publicJwk = publicKey.export({ format: "jwk" });
    config.catalogVerificationKeys.route_test_key = publicJwk;
    catalog.keyId = "route_test_key";
    catalog.remotes[0].routeBasePath = "/tasks";
    const signingPayload = {
      catalogVersion: catalog.catalogVersion,
      expiresAt: catalog.expiresAt,
      generatedAt: catalog.generatedAt,
      keyId: catalog.keyId,
      remotes: catalog.remotes.map((remote) => ({
        contractVersion: remote.contractVersion,
        entry: remote.entry,
        exposedModule: remote.exposedModule,
        integrity: remote.integrity ?? "",
        minShellVersion: remote.minShellVersion,
        name: remote.name,
        requiredCapabilities: [...remote.requiredCapabilities],
        routeBasePath: remote.routeBasePath,
      })),
      schemaVersion: catalog.schemaVersion,
    };
    catalog.signature = sign(null, Buffer.from(JSON.stringify(signingPayload)), privateKey).toString("base64url");

    const configCandidate = join(directory, "shell-config.json");
    const catalogCandidate = join(directory, "mfe-catalog.json");
    writeFileSync(configCandidate, JSON.stringify(config));
    writeFileSync(catalogCandidate, JSON.stringify(catalog));
    const verify = () => spawnSync(process.execPath, [
      verifier, "--config", configCandidate, "--catalog", catalogCandidate, "--now", fixedNow,
    ], { encoding: "utf8" });
    assert.equal(verify().status, 0, "correctly signed route path should be accepted");

    catalog.remotes[0].routeBasePath = "/admin";
    writeFileSync(catalogCandidate, JSON.stringify(catalog));
    const tampered = verify();
    assert.notEqual(tampered.status, 0, "unsigned route change unexpectedly passed");
    assert.match(tampered.stderr, /catalog signature is invalid/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("rejects an invalid route base", () => {
  const directory = mkdtempSync(join(tmpdir(), "ods-route-shape-test-"));
  try {
    const config = JSON.parse(readFileSync(configPath, "utf8"));
    const catalog = JSON.parse(readFileSync(catalogPath, "utf8"));
    catalog.remotes[0].routeBasePath = "//evil.example/tasks";
    const configCandidate = join(directory, "shell-config.json");
    const catalogCandidate = join(directory, "mfe-catalog.json");
    writeFileSync(configCandidate, JSON.stringify(config));
    writeFileSync(catalogCandidate, JSON.stringify(catalog));
    const invalid = spawnSync(process.execPath, [
      verifier, "--config", configCandidate, "--catalog", catalogCandidate, "--now", fixedNow,
    ], { encoding: "utf8" });
    assert.notEqual(invalid.status, 0);
    assert.match(invalid.stderr, /invalid routeBasePath/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("rejects duplicate signed route bases", () => {
  const directory = mkdtempSync(join(tmpdir(), "ods-route-duplicate-test-"));
  try {
    const config = JSON.parse(readFileSync(configPath, "utf8"));
    const catalog = JSON.parse(readFileSync(catalogPath, "utf8"));
    catalog.remotes[0].routeBasePath = "/tasks";
    catalog.remotes.push({ ...catalog.remotes[0], name: "case-mfe" });
    const configCandidate = join(directory, "shell-config.json");
    const catalogCandidate = join(directory, "mfe-catalog.json");
    writeFileSync(configCandidate, JSON.stringify(config));
    writeFileSync(catalogCandidate, JSON.stringify(catalog));
    const duplicate = spawnSync(process.execPath, [
      verifier, "--config", configCandidate, "--catalog", catalogCandidate, "--now", fixedNow,
    ], { encoding: "utf8" });
    assert.notEqual(duplicate.status, 0);
    assert.match(duplicate.stderr, /duplicate remote route/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

for (const [name, mutate] of [
  ["tampered", (catalog) => { catalog.remotes[0].contractVersion = "9.9"; }],
  ["expired", (catalog) => { catalog.expiresAt = "2026-10-29T11:00:00Z"; }],
  ["unapproved origin", (catalog) => { catalog.remotes[0].entry = "https://evil.example/mf-manifest.json"; }],
]) {
  test(`rejects ${name} catalog`, () => {
    const directory = mkdtempSync(join(tmpdir(), "ods-catalog-test-"));
    try {
      const catalog = JSON.parse(readFileSync(catalogPath, "utf8"));
      mutate(catalog);
      const candidate = join(directory, "catalog.json");
      writeFileSync(candidate, JSON.stringify(catalog));
      const result = spawnSync(process.execPath, [
        verifier, "--config", configPath, "--catalog", candidate, "--now", fixedNow,
      ], { encoding: "utf8" });
      assert.notEqual(result.status, 0, `${name} catalog unexpectedly passed`);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
}
