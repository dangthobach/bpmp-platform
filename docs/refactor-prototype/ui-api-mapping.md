# UI to API traceability

Generated lexical inventory summary; this is not accepted behavioral parity. A control and callback can describe one interaction. `API_CANDIDATE` does not mean the API is implemented.

Detailed source lines, prototype area, API contract owner/status and open owner review are in [CSV](ui-api-mapping.csv); source expressions and file hashes are in [inventory JSON](interaction-inventory.json). These are ownership candidates derived from lexical source and the API catalog, not domain-owner approval or behavioral parity.

| Classification | Rows |
|---|---:|
| API_CANDIDATE | 195 |
| LOCAL_DRAFT | 344 |
| LOCAL_UI | 813 |
| NO_DIRECT_HANDLER | 14 |
| REVIEW_REQUIRED | 20 |

| Contract owner candidate | Rows |
|---|---:|
| Audit | 5 |
| Authoring | 162 |
| Authz | 53 |
| Case | 11 |
| Case;Document | 12 |
| Document | 4 |
| Engine | 11 |
| Human | 25 |
| Identity | 40 |
| Integration | 16 |
| Projection | 2 |
| Report | 12 |
| Session | 1 |
| no backend operation inferred | 1032 |

The source inventory is lexical evidence. Dynamic labels, delegated handlers, disabled controls, submit behavior, and persistence boundaries require manual review. All rows remain pending source/browser review; an API catalog owner is a routing candidate, not approval. `REVIEW_REQUIRED` and `NO_DIRECT_HANDLER` rows must be resolved before the relevant UI slice is accepted.
