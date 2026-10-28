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
  it("verifies Ed25519 signatures and rejects tampering", async () => {
    const pair = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
    const publicJWK = await crypto.subtle.exportKey("jwk", pair.publicKey);
    const signature = await crypto.subtle.sign({ name: "Ed25519" }, pair.privateKey, catalogSigningBytes(catalog));
    const signed = { ...catalog, signature: encodeBase64URL(new Uint8Array(signature)) };
    const verify = createCatalogSignatureVerifier({ "key-1": publicJWK });

    await expect(verify(signed)).resolves.toBe(true);
    await expect(verify({ ...signed, catalogVersion: "tampered" })).resolves.toBe(false);
  });

  it("fails closed for an unknown key", async () => {
    await expect(createCatalogSignatureVerifier({})(catalog)).resolves.toBe(false);
  });
});

function encodeBase64URL(value: Uint8Array): string {
  return btoa(String.fromCharCode(...value)).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}
