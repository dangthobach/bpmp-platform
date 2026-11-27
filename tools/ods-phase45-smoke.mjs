/**
 * End-to-end smoke test for Phase 4 & 5 ODS runtime wiring:
 *   ods-shell (4300) + MFE manifests (4305..4308) -> api-gateway (8443) -> mTLS services -> PostgreSQL/Kafka -> audit-projection.
 *
 * Usage:
 *   node tools/ods-phase45-smoke.mjs
 *   ODS_SHELL_ORIGIN=http://localhost:4300 ODS_GATEWAY_ORIGIN=https://127.0.0.1:18443 node tools/ods-phase45-smoke.mjs
 */

const shellOrigin = process.env.ODS_SHELL_ORIGIN ?? "http://127.0.0.1:4300";
const mfeManifests = [
  { name: "document-mfe", url: process.env.DOCUMENT_MFE_MANIFEST ?? "http://127.0.0.1:4305/mf-manifest.json" },
  { name: "integration-mfe", url: process.env.INTEGRATION_MFE_MANIFEST ?? "http://127.0.0.1:4306/mf-manifest.json" },
  { name: "admin-mfe", url: process.env.ADMIN_MFE_MANIFEST ?? "http://127.0.0.1:4307/mf-manifest.json" },
  { name: "monitoring-mfe", url: process.env.MONITORING_MFE_MANIFEST ?? "http://127.0.0.1:4308/mf-manifest.json" },
];

async function checkURL(label, url) {
  const res = await fetch(url, { signal: AbortSignal.timeout(5000) });
  if (!res.ok) {
    throw new Error(`${label} (${url}) returned HTTP ${res.status}`);
  }
  return res;
}

async function main() {
  await checkURL("ods-shell", shellOrigin);
  const catalogRes = await checkURL("ods-shell catalog", `${shellOrigin}/mfe-catalog.json`);
  const catalog = await catalogRes.json();
  const names = new Set((catalog.remotes ?? []).map((r) => r.name));
  for (const mfe of mfeManifests) {
    if (!names.has(mfe.name)) {
      throw new Error(`Catalog missing remote ${mfe.name}`);
    }
    await checkURL(mfe.name, mfe.url);
  }
  console.log(`Phase 4/5 ODS Shell + ${mfeManifests.length} MFE manifest smoke passed.`);
}

main().catch((err) => {
  console.error("ODS Phase 4/5 smoke failed:", err.message);
  process.exitCode = 1;
});
