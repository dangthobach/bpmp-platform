import { describe, expect, it, vi } from "vitest";
import { loadCatalog } from "./catalog";
import type { ShellConfig } from "../config";

const config: ShellConfig = {
  shellVersion: "1.0",
  remoteContractVersion: "1.0",
  catalogUrl: "/mfe-catalog.json",
  catalogTimeoutMs: 100,
  remoteTimeoutMs: 100,
  apiTimeoutMs: 100,
  maxApiResponseBytes: 1_000,
  maxCatalogLifetimeMs: 86_400_000,
  allowedRemoteOrigins: new Set(["https://assets.example"]),
  allowUnsignedCatalog: false,
  catalogVerificationKeys: {},
};

const catalog = {
  schemaVersion: "1",
  catalogVersion: "v1",
  generatedAt: "2026-10-28T00:00:00Z",
  expiresAt: "2026-10-29T00:00:00Z",
  signature: "signed",
  keyId: "key-1",
  remotes: [{
    name: "task-mfe",
    entry: "https://assets.example/task/v1/mf-manifest.json",
    exposedModule: "route",
    contractVersion: "1.0",
    minShellVersion: "1.0",
    requiredCapabilities: ["task.read"],
  }],
};

describe("loadCatalog", () => {
  it("fails closed when signature verification fails", async () => {
    const fetchCatalog = vi.fn().mockResolvedValue(new Response(JSON.stringify(catalog), { status: 200 }));
    await expect(loadCatalog(config, async () => false, fetchCatalog, new Date("2026-10-28T12:00:00Z"))).rejects.toThrow("signature is invalid");
  });

  it("accepts a verified allowlisted catalog", async () => {
    const fetchCatalog = vi.fn().mockResolvedValue(new Response(JSON.stringify(catalog), { status: 200 }));
    await expect(loadCatalog(config, async () => true, fetchCatalog, new Date("2026-10-28T12:00:00Z"))).resolves.toMatchObject({ catalogVersion: "v1" });
  });

  it("rejects an expired catalog before remote registration", async () => {
    const fetchCatalog = vi.fn().mockResolvedValue(new Response(JSON.stringify(catalog), { status: 200 }));
    await expect(loadCatalog(config, async () => true, fetchCatalog, new Date("2026-10-30T00:00:00Z"))).rejects.toThrow("expired");
  });

  it.each([
    ["previous", "release-0.9"],
    ["current", "release-1.0"],
    ["canary", "release-1.1-canary.1"],
  ])("accepts the %s immutable release when its public contract remains compatible", async (_lane, catalogVersion) => {
    const candidate = { ...catalog, catalogVersion };
    const fetchCatalog = vi.fn().mockResolvedValue(new Response(JSON.stringify(candidate), { status: 200 }));
    await expect(loadCatalog(config, async () => true, fetchCatalog, new Date("2026-10-28T12:00:00Z")))
      .resolves.toMatchObject({ catalogVersion });
  });

  it("rejects a canary that requires a newer Shell", async () => {
    const candidate = { ...catalog, remotes: [{ ...catalog.remotes[0], minShellVersion: "1.1" }] };
    const fetchCatalog = vi.fn().mockResolvedValue(new Response(JSON.stringify(candidate), { status: 200 }));
    await expect(loadCatalog(config, async () => true, fetchCatalog, new Date("2026-10-28T12:00:00Z")))
      .rejects.toThrow("requires shell 1.1");
  });

  it("rejects a canary with a breaking remote contract", async () => {
    const candidate = { ...catalog, remotes: [{ ...catalog.remotes[0], contractVersion: "2.0" }] };
    const fetchCatalog = vi.fn().mockResolvedValue(new Response(JSON.stringify(candidate), { status: 200 }));
    await expect(loadCatalog(config, async () => true, fetchCatalog, new Date("2026-10-28T12:00:00Z")))
      .rejects.toThrow("contract 2.0 is incompatible");
  });
});
