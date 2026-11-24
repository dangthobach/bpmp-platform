import { describe, expect, it } from "vitest";
import { catalogSigningBytes, type RemoteCatalog } from "@ods/mfe-contract";
import { createCatalogSignatureVerifier } from "./signature";

const catalog: RemoteCatalog = {
  schemaVersion: "1",
  catalogVersion: "catalog-1",
  generatedAt: "2026-10-29T00:00:00Z",
  expiresAt: "2026-10-30T00:00:00Z",
  keyId: "key-1",
  signature: "pending",
  remotes: [],
};

describe("createCatalogSignatureVerifier", () => {
  it("keeps legacy catalog signing bytes stable when no route path is declared", () => {
    expect(new TextDecoder().decode(catalogSigningBytes(catalog))).toBe(
      '{"catalogVersion":"catalog-1","expiresAt":"2026-10-30T00:00:00Z","generatedAt":"2026-10-29T00:00:00Z","keyId":"key-1","remotes":[],"schemaVersion":"1"}',
    );
  });

  it("verifies Ed25519 signatures and rejects tampering", async () => {
    const pair = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
    const publicJWK = await crypto.subtle.exportKey("jwk", pair.publicKey);
    const signature = await crypto.subtle.sign({ name: "Ed25519" }, pair.privateKey, catalogSigningBytes(catalog));
    const signed = { ...catalog, signature: encodeBase64URL(new Uint8Array(signature)) };
    const verify = createCatalogSignatureVerifier({ "key-1": publicJWK });

    await expect(verify(signed)).resolves.toBe(true);
    await expect(verify({ ...signed, catalogVersion: "tampered" })).resolves.toBe(false);
  });

  it("includes a declared route path in catalog signing bytes", () => {
    const routeEntry: RemoteCatalog["remotes"][number] = {
      name: "task-mfe",
      entry: "https://assets.example/task/v1/mf-manifest.json",
      exposedModule: "route",
      routeBasePath: "/tasks",
      contractVersion: "1.0",
      minShellVersion: "1.0",
      requiredCapabilities: ["task.read"],
    };
    const routed: RemoteCatalog = {
      ...catalog,
      remotes: [routeEntry],
    };
    const changed: RemoteCatalog = { ...routed, remotes: [{ ...routeEntry, routeBasePath: "/admin" }] };
    expect(catalogSigningBytes(changed)).not.toEqual(catalogSigningBytes(routed));
  });

  it("fails closed for an unknown key", async () => {
    await expect(createCatalogSignatureVerifier({})(catalog)).resolves.toBe(false);
  });
});

function encodeBase64URL(value: Uint8Array): string {
  return btoa(String.fromCharCode(...value)).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}
