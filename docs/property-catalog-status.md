# P1-P53 Property Catalog Status

The canonical gate is:

```powershell
.\tools\check-property-catalog.ps1 -RequireComplete
```

Each canonical property test must exercise a production primitive, execute at
least 100 generated cases, and have exactly one
`Feature: rust-bpm-platform, Property N:` marker.

## Current coverage

As of 2026-07-27, 34 of 53 properties have a canonical PBT.

| Owner | Covered |
| --- | --- |
| Compiler/domain core | P1, P2, P4, P11, P53 |
| Human Runtime | P3, P5, P6 |
| Engine runtime/event store | P8, P9, P12-P18, P27-P30, P37, P38, P45 |
| Raft/HA | P23 |
| API/platform reliability | P25, P31, P33, P34, P36, P47 |
| Governance/crypto | P50-P52 |

## Remaining implementation gates

| Properties | Required production work |
| --- | --- |
| P7, P10 | Durable compensation executor/resume ledger and WIR-scoped service-task retry policy |
| P19-P22 | Cockpit subscription registry and Progressive BPMN BA/Engineer views |
| P24, P26, P32 | Configured fallback, bulkhead isolation and alert evaluator/sink |
| P35, P42, P43 | Property generators against PostgreSQL keyset pagination and deterministic projection rebuild/query |
| P39-P41 | Versioned event/snapshot upcasters, safe-point migration and reference-counted WIR retirement |
| P44 | Authoritative local dead-letter replay command and idempotent node-only resume |
| P46 | Cross-adapter tenant-isolation suite covering engine, dispatch, Human Runtime query and Gateway |
| P48, P49 | PII read masking/tombstones and terminal-or-fenced key-destruction orchestration |

The catalog checker intentionally remains failing under `-RequireComplete`
until these production capabilities and their canonical PBTs exist. Model-only
tests must not be used to mark a missing capability complete.
