import test from "node:test";
import assert from "node:assert/strict";
import {
  promoteCanaryRoute,
  rollbackCatalog,
  evaluateRouteCutover,
  digestCatalog,
} from "./ods-cutover-canary.mjs";
import {
  reconcileMigrationDataset,
  evaluateDecommissionGate,
} from "./ods-hardening-reconcile.mjs";

test("Phase 6 Drill 1: multi-wave strangler canary cutover and instant rollback on readiness regression", () => {
  const baseline = {
    catalogVersion: 10,
    remotes: [
      {
        name: "task-mfe",
        version: "1.0.0",
        manifestUrl: "https://cdn.ods.internal/assets/task-mfe/1.0.0/mf-manifest.json",
        routeBasePath: "/tasks",
        enabledTenants: ["*"],
        requiredCapabilities: ["task.read"],
      },
      {
        name: "case-mfe",
        version: "1.0.0",
        manifestUrl: "https://cdn.ods.internal/assets/case-mfe/1.0.0/mf-manifest.json",
        routeBasePath: "/cases",
        enabledTenants: ["*"],
        requiredCapabilities: ["case.read"],
      },
    ],
  };

  // Wave 1: promote document-mfe + integration-mfe to pilot tenant only
  const w1 = promoteCanaryRoute(baseline, {
    name: "document-mfe",
    version: "1.0.0",
    manifestUrl: "https://cdn.ods.internal/assets/document-mfe/1.0.0/mf-manifest.json",
    routeBasePath: "/documents",
    enabledTenants: ["tenant-pilot"],
    requiredCapabilities: ["document.read"],
    canaryPercent: 10,
  });
  const w2 = promoteCanaryRoute(w1.catalog, {
    name: "integration-mfe",
    version: "1.0.0",
    manifestUrl: "https://cdn.ods.internal/assets/integration-mfe/1.0.0/mf-manifest.json",
    routeBasePath: "/integrations",
    enabledTenants: ["tenant-pilot"],
    requiredCapabilities: ["integration.endpoint.read"],
    canaryPercent: 10,
  });

  // Pilot tenant sees new remotes; production tenant stays on legacy
  const intRemote = w2.catalog.remotes.find((r) => r.name === "integration-mfe");
  assert.equal(evaluateRouteCutover(intRemote, "tenant-pilot", ["integration.endpoint.read"]).target, "ods_remote");
  assert.equal(evaluateRouteCutover(intRemote, "tenant-main", ["integration.endpoint.read"]).target, "legacy");

  // Simulate readiness failure on wave 2 -> instant rollback restores exact wave 1 digest
  const rolledBack = rollbackCatalog(w2.rollbackSnapshot);
  assert.equal(rolledBack.catalogVersion, w1.catalog.catalogVersion);
  assert.equal(rolledBack.remotes.some((r) => r.name === "integration-mfe"), false);
  assert.equal(digestCatalog({ ...rolledBack, rolledBackAt: undefined }), digestCatalog({ ...w1.catalog, rolledBackAt: undefined }));
});

test("Phase 6 Drill 2: migration reconciliation across 1,000 records and decommission gate enforcement", () => {
  const legacy = Array.from({ length: 1000 }, (_, i) => ({
    id: `case-${String(i).padStart(5, "0")}`,
    tenant: "t1",
    status: i % 3 === 0 ? "CLOSED" : "ACTIVE",
    version: (i % 7) + 1,
  }));
  // Shuffled order in ODS store must still reconcile cleanly by canonical ID sort + SHA-256
  const ods = [...legacy].reverse();
  const report = reconcileMigrationDataset(legacy, ods);
  assert.equal(report.passed, true);
  assert.equal(report.legacyCount, 1000);
  assert.equal(report.odsCount, 1000);

  // Decommission allowed ONLY when all 4 DR drills, 2 stable releases, SLOs, zero traffic, and reconciliation pass
  const gate = evaluateDecommissionGate({
    consecutiveStableReleases: 2,
    sloPassed: true,
    legacyRouteTrafficLast7d: 0,
    reconciliationPassed: report.passed,
    drDrills: {
      rds_pitr_restore: true,
      kafka_replay: true,
      projection_rebuild: true,
      raft_recovery: true,
    },
  });
  assert.equal(gate.readyForDecommission, true);
});
