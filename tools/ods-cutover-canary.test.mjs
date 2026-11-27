import test from "node:test";
import assert from "node:assert/strict";
import {
  promoteCanaryRoute,
  rollbackCatalog,
  evaluateRouteCutover,
} from "./ods-cutover-canary.mjs";

test("strangler canary promotion routes canary tenant to ODS and non-canary to legacy, with instant rollback", () => {
  const initialCatalog = {
    catalogVersion: 1,
    remotes: [
      {
        name: "task_mfe",
        version: "1.0.0",
        manifestUrl: "https://cdn.example/assets/task-mfe/1.0.0/mf-manifest.json",
        routeBasePath: "/tasks",
        enabledTenants: ["*"],
        requiredCapabilities: ["task.read"],
      },
    ],
  };

  const { catalog: promoted, rollbackSnapshot } = promoteCanaryRoute(initialCatalog, {
    name: "document_mfe",
    version: "1.0.0",
    manifestUrl: "https://cdn.example/assets/document-mfe/1.0.0/mf-manifest.json",
    routeBasePath: "/documents",
    enabledTenants: ["tenant-canary"],
    requiredCapabilities: ["document.read"],
    canaryPercent: 10,
  });

  assert.equal(promoted.catalogVersion, 2);
  const docRoute = promoted.remotes.find((r) => r.name === "document_mfe");
  assert.equal(
    evaluateRouteCutover(docRoute, "tenant-canary", ["document.read"]).target,
    "ods_remote",
  );
  assert.equal(
    evaluateRouteCutover(docRoute, "tenant-prod-2", ["document.read"]).target,
    "legacy",
  );
  assert.equal(
    evaluateRouteCutover(docRoute, "tenant-canary", []).target,
    "legacy",
  );

  // Rollback restores exact prior catalog without document_mfe
  const restored = rollbackCatalog(rollbackSnapshot);
  assert.equal(restored.catalogVersion, 1);
  assert.equal(restored.remotes.length, 1);
  assert.equal(restored.remotes[0].name, "task_mfe");
});
