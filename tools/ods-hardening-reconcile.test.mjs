import test from "node:test";
import assert from "node:assert/strict";
import {
  reconcileMigrationDataset,
  evaluateDecommissionGate,
} from "./ods-hardening-reconcile.mjs";

test("migration reconciliation passes on matching count/checksum and fails closed on drift", () => {
  const legacy = [
    { id: "c-2", status: "ACTIVE", version: 3 },
    { id: "c-1", status: "CLOSED", version: 5 },
  ];
  const ods = [
    { id: "c-1", status: "CLOSED", version: 5 },
    { id: "c-2", status: "ACTIVE", version: 3 },
  ];
  const ok = reconcileMigrationDataset(legacy, ods);
  assert.equal(ok.passed, true);
  assert.equal(ok.legacyDigest, ok.odsDigest);

  const drifted = reconcileMigrationDataset(legacy, [ods[0], { id: "c-2", status: "DRAFT", version: 3 }]);
  assert.equal(drifted.passed, false);
});

test("decommission gate blocks legacy retirement until 2 stable releases, zero traffic, reconciliation, and all 4 DR drills pass", () => {
  const blocked = evaluateDecommissionGate({
    consecutiveStableReleases: 1,
    sloPassed: true,
    legacyRouteTrafficLast7d: 42,
    reconciliationPassed: true,
    drDrills: { rds_pitr_restore: true, kafka_replay: true, projection_rebuild: true, raft_recovery: false },
  });
  assert.equal(blocked.readyForDecommission, false);
  assert.equal(blocked.failures.length, 3);

  const ready = evaluateDecommissionGate({
    consecutiveStableReleases: 2,
    sloPassed: true,
    legacyRouteTrafficLast7d: 0,
    reconciliationPassed: true,
    drDrills: { rds_pitr_restore: true, kafka_replay: true, projection_rebuild: true, raft_recovery: true },
  });
  assert.equal(ready.readyForDecommission, true);
  assert.deepEqual(ready.failures, []);
});
