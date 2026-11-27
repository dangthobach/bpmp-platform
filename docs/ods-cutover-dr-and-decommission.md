# ODS Cutover, DR Drills, Hardening & Legacy Decommission Guide (Phase 6)

## 1. Route-by-Route Strangler Cutover Plan
Each domain route transitions from `cockpit-web` to `ods-shell` + independent remote MFE without dual-writing any aggregate:
1. `/tasks/*` -> `task-mfe` (Human Runtime + BPMP Engine)
2. `/cases/*` -> `case-mfe` (Case Service + Projection)
3. `/documents/*` -> `document-mfe` (Document Adapter + CSP)
4. `/integrations/*` -> `integration-mfe` (Integration Hub)
5. `/notifications/*` -> `notification-mfe` (Notification Service)
6. `/admin/*` -> `admin-mfe` (Master Data + Audit Projection)
7. `/monitoring/*` -> `monitoring-mfe` (Projection/Observability)

### Canary & Instant Rollback
- Promote one route per tenant cohort using `tools/ods-cutover-canary.mjs` + signed catalog (`tools/sign-ods-catalog.mjs` & `tools/verify-ods-catalog.mjs`).
- Rollback restores the prior signed catalog snapshot in seconds without rebuilding Shell or any backend service.

## 2. Disaster Recovery (DR) Drill Procedures
1. **RDS PITR Restore (`rds_pitr_restore`)**: Restore isolated service PostgreSQL databases (`case_db`, `document_db`, `integration_db`, `mdm_db`, `notification_db`) to a target timestamp; verify schema migration table and run `reconcileMigrationDataset`.
2. **Kafka Replay (`kafka_replay`)**: Reset consumer group offsets on a staging replay consumer; verify inbox deduplication `(tenant_id, event_id)` produces zero duplicate side effects.
3. **Projection Rebuild (`projection_rebuild`)**: Wipe `audit_projection_entries` / projection read tables; invoke rebuild from authoritative owner event logs; verify `projection_generation` increments and counts/checksums match 100%.
4. **Engine Raft Recovery (`raft_recovery`)**: Terminate a Raft follower/leader node, verify quorum election and command idempotency continuity, rejoin node and run `--storage-check`.

## 3. Data Migration Reconciliation (`expand -> migrate/backfill -> switch -> contract`)
- Export snapshot + watermark from legacy owner and new ODS owner.
- Run `reconcileMigrationDataset` (`tools/ods-hardening-reconcile.mjs`) to verify identical record count, canonical SHA-256 checksum, and zero missing/extra IDs.
- Never contract/drop legacy tables until at least one full release cycle after `switch`.

## 4. Legacy `cockpit-web` Decommission Checklist
Enforced programmatically by `evaluateDecommissionGate`:
- [ ] At least **2 consecutive stable releases** without rollback.
- [ ] Production SLOs (p95, error budget, projection lag) within approved thresholds.
- [ ] **Zero traffic** observed on legacy routes for 7 consecutive days.
- [ ] Signed data reconciliation report with zero drift.
- [ ] All 4 DR drills (`rds_pitr_restore`, `kafka_replay`, `projection_rebuild`, `raft_recovery`) executed and recorded.
