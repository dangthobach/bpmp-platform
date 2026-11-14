# Prototype control/event inventory

Generated lexical evidence. Includes native controls, non-button events, and custom callback props.
IDs are snapshot-local; regeneration can renumber them. File hashes are in JSON.
A control and its event are separate rows, not separate user journeys.
Expressions are evidence, not proof that an action persists or succeeds.
Conditional/dynamic labels, forwarded props, keyboard behavior and handler semantics require manual/browser review.
No browser walkthrough was performed. This is not a certified exhaustive runtime interaction inventory.

| File | Native controls | Event props |
|---|---:|---:|
| App.tsx | 0 | 21 |
| components/access/AccessModule.tsx | 30 | 29 |
| components/common/Header.tsx | 9 | 8 |
| components/common/Sidebar.tsx | 3 | 3 |
| components/foundation/EndpointsModal.tsx | 16 | 13 |
| components/foundation/FoundationModule.tsx | 15 | 15 |
| components/identity/IdentityModule.tsx | 167 | 178 |
| components/modeler/DmnTableModule.tsx | 35 | 48 |
| components/modeler/DmnVersionCompareModal.tsx | 5 | 5 |
| components/modeler/DmnVersionHistorySidebar.tsx | 14 | 13 |
| components/modeler/DmnVersionInspectModal.tsx | 3 | 3 |
| components/modeler/DmnVisualLogicEditor.tsx | 19 | 23 |
| components/modeler/ModelerModule.tsx | 23 | 37 |
| components/modeler/ModelerPalette.tsx | 4 | 7 |
| components/modeler/ModelerPropertiesPanel.tsx | 45 | 36 |
| components/operation/OperationModule.tsx | 30 | 35 |
| components/optimize/OptimizeModule.tsx | 12 | 20 |
| components/optimize/ProcessHealthReport.tsx | 9 | 9 |
| components/optimize/ProcessThroughputDashboard.tsx | 11 | 11 |
| components/portal/ClientPortalModule.tsx | 95 | 114 |
| components/portal/EmbeddedProcessViewer.tsx | 22 | 23 |
| components/portal/PortalMetricsDashboard.tsx | 4 | 6 |
| components/task-management/AccessAuditLog.tsx | 10 | 10 |
| components/task-management/TaskManagementModule.tsx | 33 | 38 |
| components/tasklist/TasklistModule.tsx | 31 | 36 |
| main.tsx | 0 | 0 |

Full row-level evidence and source expressions are in [CSV](interaction-inventory.csv) and [JSON](interaction-inventory.json). This Markdown file intentionally stays a short summary to avoid duplicating generated data.
