import { createHash } from "node:crypto";

/**
 * Phase 6: Data migration reconciliation (count + canonical checksum + invariants)
 * and legacy route decommission readiness gate.
 */

export function canonicalRecordDigest(records) {
  const sorted = [...records].sort((a, b) => String(a.id).localeCompare(String(b.id)));
  return createHash("sha256").update(JSON.stringify(sorted)).digest("hex");
}

export function reconcileMigrationDataset(legacyRecords, odsRecords) {
  const legacyDigest = canonicalRecordDigest(legacyRecords);
  const odsDigest = canonicalRecordDigest(odsRecords);
  const legacyIds = new Set(legacyRecords.map((r) => r.id));
  const odsIds = new Set(odsRecords.map((r) => r.id));

  const missingInOds = [...legacyIds].filter((id) => !odsIds.has(id));
  const extraInOds = [...odsIds].filter((id) => !legacyIds.has(id));
  const passed =
    legacyRecords.length === odsRecords.length &&
    legacyDigest === odsDigest &&
    missingInOds.length === 0 &&
    extraInOds.length === 0;

  return {
    passed,
    legacyCount: legacyRecords.length,
    odsCount: odsRecords.length,
    legacyDigest,
    odsDigest,
    missingInOds,
    extraInOds,
  };
}

export function evaluateDecommissionGate(evidence) {
  const failures = [];
  if ((evidence.consecutiveStableReleases ?? 0) < 2) {
    failures.push("Requires at least 2 consecutive stable releases without rollback");
  }
  if (!evidence.sloPassed) {
    failures.push("SLO and error-budget acceptance has not passed");
  }
  if ((evidence.legacyRouteTrafficLast7d ?? 1) > 0) {
    failures.push("Legacy route still receives non-zero traffic");
  }
  if (!evidence.reconciliationPassed) {
    failures.push("Data migration reconciliation has drift or is incomplete");
  }
  const requiredDrills = ["rds_pitr_restore", "kafka_replay", "projection_rebuild", "raft_recovery"];
  for (const drill of requiredDrills) {
    if (!evidence.drDrills?.[drill]) {
      failures.push(`Missing DR drill evidence: ${drill}`);
    }
  }
  return {
    readyForDecommission: failures.length === 0,
    failures,
  };
}
