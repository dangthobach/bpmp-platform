import { parseRemoteCatalog, type RemoteCatalog } from "@ods/mfe-contract";
import type { ShellConfig } from "../config";

export type CatalogSignatureVerifier = (catalog: RemoteCatalog) => Promise<boolean>;

export async function loadCatalog(
  config: ShellConfig,
  verifySignature: CatalogSignatureVerifier,
  fetchCatalog: typeof fetch = fetch,
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
    if (catalog.signature === undefined && !config.allowUnsignedCatalog) {
      throw new Error("unsigned MFE catalog is forbidden");
    }
    if (catalog.signature !== undefined && !await verifySignature(catalog)) {
      throw new Error("MFE catalog signature is invalid");
    }
    return catalog;
  } finally {
    clearTimeout(timeout);
  }
}
