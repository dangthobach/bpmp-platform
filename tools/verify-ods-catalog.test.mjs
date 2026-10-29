import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
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
