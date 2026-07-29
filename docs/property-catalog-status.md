# P1-P53 Property Catalog Status

The canonical gate is:

```powershell
.\tools\check-property-catalog.ps1 -RequireComplete
```

Each canonical property has exactly one
`Feature: rust-bpm-platform, Property N:` marker. As of 2026-07-29 the
catalog checker reports **53/53**.

| Owner | Covered |
| --- | --- |
| Compiler/domain core | P1, P2, P4, P11, P21, P22, P53 |
| Human Runtime | P3, P5, P6, P35 |
| Engine runtime/event store | P7-P10, P12-P18, P27-P30, P37-P41, P44-P46, P48, P49 |
| Cockpit Gateway | P19, P20 |
| Raft/HA | P23 |
| API/platform reliability | P24-P26, P31-P34, P36, P47 |
| Projection Service | P42, P43 |
| Governance/crypto | P50-P52 |

Catalog completeness is a source-level invariant gate, not a production
capacity or disaster-recovery certificate. Release profiles must still run the
relevant PostgreSQL/Kafka integration, race, model, chaos, restore, KMS and
load tests described in the production deployment process.
