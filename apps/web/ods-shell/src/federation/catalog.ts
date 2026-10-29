import { assertRemoteCatalogCompatibility, parseRemoteCatalog, type RemoteCatalog } from "@ods/mfe-contract";
import type { ShellConfig } from "../config";

export type CatalogSignatureVerifier = (catalog: RemoteCatalog) => Promise<boolean>;

export async function loadCatalog(
  config: ShellConfig,
  verifySignature: CatalogSignatureVerifier,
  fetchCatalog: typeof fetch = fetch,
  now: Date = new Date(),
): Promise<RemoteCatalog> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort("catalog timeout"), config.catalogTimeoutMs);
  try {
    const response = await fetchCatalog(config.catalogUrl, {
      signal: controller.signal,
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    if (!response.ok) throw new Error(`MFE catalog request failed with ${response.status}`);
    const catalog = parseRemoteCatalog(await response.json(), config.allowedRemoteOrigins);
    assertCatalogWindow(catalog, now, config.maxCatalogLifetimeMs);
    if (catalog.signature === undefined && !config.allowUnsignedCatalog) {
      throw new Error("unsigned MFE catalog is forbidden");
    }
    if (catalog.signature !== undefined && !await verifySignature(catalog)) {
      throw new Error("MFE catalog signature is invalid");
    }
    assertRemoteCatalogCompatibility(catalog, {
      shellVersion: config.shellVersion,
      remoteContractVersion: config.remoteContractVersion,
    });
    return catalog;
  } finally {
    clearTimeout(timeout);
  }
}

function assertCatalogWindow(catalog: RemoteCatalog, now: Date, maxLifetimeMs: number): void {
  const generatedAt = Date.parse(catalog.generatedAt);
  const expiresAt = Date.parse(catalog.expiresAt);
  const clockSkewMs = 60_000;
  if (generatedAt > now.getTime() + clockSkewMs) throw new Error("MFE catalog is not yet valid");
  if (expiresAt <= now.getTime()) throw new Error("MFE catalog has expired");
  if (expiresAt <= generatedAt || expiresAt - generatedAt > maxLifetimeMs) {
    throw new Error("MFE catalog lifetime exceeds policy");
  }
}
