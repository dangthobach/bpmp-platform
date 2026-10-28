import { catalogSigningBytes, type RemoteCatalog } from "@ods/mfe-contract";

export function createCatalogSignatureVerifier(
  verificationKeys: Readonly<Record<string, JsonWebKey>>,
): (catalog: RemoteCatalog) => Promise<boolean> {
  return async (catalog) => {
    if (!catalog.keyId || !catalog.signature) return false;
    const jwk = verificationKeys[catalog.keyId];
    if (!jwk) return false;
    try {
      const key = await crypto.subtle.importKey("jwk", jwk, { name: "Ed25519" }, false, ["verify"]);
      return await crypto.subtle.verify(
        { name: "Ed25519" },
        key,
        decodeBase64URL(catalog.signature),
        catalogSigningBytes(catalog),
      );
    } catch {
      return false;
    }
  };
}

function decodeBase64URL(value: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new TypeError("invalid base64url signature");
  const padded = value.replaceAll("-", "+").replaceAll("_", "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  const binary = atob(padded);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}
