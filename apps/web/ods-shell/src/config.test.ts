import { describe, expect, it, vi } from "vitest";
import { loadShellConfig } from "./config";

const valid = {
  schemaVersion: "1",
  shellVersion: "1.0",
  catalogUrl: "/mfe-catalog.json",
  catalogTimeoutMs: 5_000,
  remoteTimeoutMs: 10_000,
  apiTimeoutMs: 15_000,
  maxApiResponseBytes: 4_194_304,
  maxCatalogLifetimeMs: 86_400_000,
  allowedRemoteOrigins: ["https://assets.example"],
  catalogVerificationKeys: {},
};

describe("loadShellConfig", () => {
  it("loads a bounded same-origin runtime configuration", async () => {
    const fetchConfig = vi.fn().mockResolvedValue(new Response(JSON.stringify(valid), { status: 200 }));
    const config = await loadShellConfig(fetchConfig, "https://portal.example", false);
    expect(config.catalogUrl).toBe("/mfe-catalog.json");
    expect(config.allowedRemoteOrigins).toEqual(new Set(["https://assets.example", "https://portal.example"]));
    expect(config.allowUnsignedCatalog).toBe(false);
  });

  it("rejects a cross-origin catalog URL", async () => {
    const fetchConfig = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      ...valid,
      catalogUrl: "https://attacker.example/catalog.json",
    }), { status: 200 }));
    await expect(loadShellConfig(fetchConfig, "https://portal.example", false)).rejects.toThrow("same-origin");
  });
});
