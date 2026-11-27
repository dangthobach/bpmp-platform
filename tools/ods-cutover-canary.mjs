import { createHash } from "node:crypto";

/**
 * Phase 6: Route-by-route strangler cutover, tenant/capability canary promotion,
 * and instant rollback helper for signed ODS MFE catalogs.
 */

export function promoteCanaryRoute(catalog, routeUpdate) {
  if (!catalog || !Array.isArray(catalog.remotes)) {
    throw new Error("Invalid catalog: remotes array is required");
  }
  if (!routeUpdate?.name || !routeUpdate?.manifestUrl || !routeUpdate?.routeBasePath) {
    throw new Error("routeUpdate requires name, manifestUrl, and routeBasePath");
  }
  if (!routeUpdate.routeBasePath.startsWith("/")) {
    throw new Error("routeBasePath must be an absolute path");
  }
  const previousSnapshot = structuredClone(catalog);
  const nextRemotes = catalog.remotes.map((r) => ({ ...r }));
  const idx = nextRemotes.findIndex((r) => r.name === routeUpdate.name);
  const promoted = {
    name: routeUpdate.name,
    version: routeUpdate.version,
    manifestUrl: routeUpdate.manifestUrl,
    routeBasePath: routeUpdate.routeBasePath,
    enabledTenants: routeUpdate.enabledTenants ?? ["*"],
    requiredCapabilities: routeUpdate.requiredCapabilities ?? [],
    canaryPercent: routeUpdate.canaryPercent ?? 100,
    rollbackTarget: idx >= 0 ? nextRemotes[idx].manifestUrl : null,
  };
  if (idx >= 0) {
    nextRemotes[idx] = promoted;
  } else {
    nextRemotes.push(promoted);
  }
  return {
    catalog: {
      ...catalog,
      catalogVersion: (catalog.catalogVersion ?? 0) + 1,
      remotes: nextRemotes,
      previousCatalogDigest: digestCatalog(previousSnapshot),
    },
    rollbackSnapshot: previousSnapshot,
  };
}

export function rollbackCatalog(rollbackSnapshot) {
  if (!rollbackSnapshot || !Array.isArray(rollbackSnapshot.remotes)) {
    throw new Error("Valid rollbackSnapshot is required");
  }
  return {
    ...structuredClone(rollbackSnapshot),
    rolledBackAt: new Date().toISOString(),
  };
}

export function evaluateRouteCutover(routeState, tenantId, grantedCapabilities = []) {
  if (!routeState) return { target: "legacy", reason: "route_not_registered" };
  const tenantAllowed =
    routeState.enabledTenants.includes("*") || routeState.enabledTenants.includes(tenantId);
  if (!tenantAllowed) {
    return { target: "legacy", reason: "tenant_not_in_canary" };
  }
  const hasCaps = (routeState.requiredCapabilities ?? []).every((c) =>
    grantedCapabilities.includes(c),
  );
  if (!hasCaps) {
    return { target: "legacy", reason: "missing_required_capabilities" };
  }
  return { target: "ods_remote", remote: routeState.name, manifestUrl: routeState.manifestUrl };
}

export function digestCatalog(catalog) {
  return createHash("sha256").update(JSON.stringify(catalog)).digest("hex");
}
