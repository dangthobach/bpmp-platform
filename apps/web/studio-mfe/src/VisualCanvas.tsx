import { useMemo, useState } from "react";
import { Button, Paper, Stack, Typography } from "@ods/shared-ui";
import { canRemoveCanvasNode, inspectBpmn, insertParallelBranch, insertTaskBeforeEnd, removeCanvasNode, renameCanvasNode, type CanvasNode, type EditableTaskKind } from "./bpmnCanvas";

const labels: Record<string, string> = { startEvent: "Start event", endEvent: "End event", serviceTask: "Service task", userTask: "User task", scriptTask: "Script task", exclusiveGateway: "Exclusive gateway", parallelGateway: "Parallel gateway" };

export function BpmnCanvas({ source, onChange }: { source: string; onChange: (value: string) => void }) {
  const inspection = useMemo(() => inspectBpmn(source), [source]);
  const [selectedId, setSelectedId] = useState("");
  const selected = inspection.nodes.find((node) => node.id === selectedId) ?? inspection.nodes[0];
  const add = (kind: EditableTaskKind) => {
    if (!selected) return;
    const changed = inspection.topology === "gateway" ? insertParallelBranch(source, selected.id, kind) : insertTaskBeforeEnd(source, kind);
    if (changed) { const next = inspectBpmn(changed); const created = next.nodes.find((item) => !inspection.nodes.some((existing) => existing.id === item.id)); if (created) setSelectedId(created.id); onChange(changed); }
  };
  const node = (item: CanvasNode) => <Button key={item.id} type="button" variant={selected?.id === item.id ? "contained" : "outlined"} disabled={!inspection.editable} onClick={() => setSelectedId(item.id)} sx={{ display: "grid", minWidth: 150, textTransform: "none", textAlign: "left" }}><strong>{labels[item.type] ?? item.type}</strong><span>{item.name || item.id}</span><small>{item.id}</small></Button>;
  return <Paper variant="outlined" sx={{ p: 2, mt: 2 }} aria-label="Visual BPMN editor">
    <Stack direction="row" justifyContent="space-between"><div><Typography variant="h3">Process canvas</Typography><Typography color="text.secondary">Linear paths and paired one-level gateways are editable. Unsupported BPMN remains available in source mode.</Typography></div><Typography role="status">{inspection.editable ? inspection.topology === "gateway" ? "Gateway flow" : "Editable path" : "Source view"}</Typography></Stack>
    {inspection.reason ? <Typography role="status" sx={{ my: 1 }}>{inspection.reason}</Typography> : null}
    <Stack direction="row" spacing={2} sx={{ overflowX: "auto", alignItems: "center", py: 2 }} aria-label="BPMN flow">
      {inspection.topology === "gateway" && inspection.branches ? <Stack spacing={1} alignItems="center">{node(inspection.nodes[0]!)}<span>↓</span>{node(inspection.nodes[1]!)}<Stack direction="row" spacing={1} alignItems="stretch">{inspection.branches.map((branch, index) => <Paper variant="outlined" key={branch[0]!.id} sx={{ p: 1, display: "grid", gap: 1 }}><Typography>Path {index + 1}</Typography>{node(branch[0]!)}<Typography align="center">Join</Typography></Paper>)}</Stack>{node(inspection.nodes.at(-2)!)}<span>↓</span>{node(inspection.nodes.at(-1)!)}</Stack> : inspection.nodes.map((item, index) => <Stack key={item.id} direction="row" spacing={1} alignItems="center">{index ? <span aria-hidden="true">↓</span> : null}{node(item)}</Stack>)}
    </Stack>
    <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
      <Stack direction="row" spacing={1}><Button type="button" disabled={!inspection.editable || inspection.topology !== "linear"} onClick={() => add("serviceTask")}>Add service task</Button><Button type="button" disabled={!inspection.editable || inspection.topology !== "linear"} onClick={() => add("userTask")}>Add user task</Button>{inspection.topology === "gateway" && selected?.id === inspection.splitGatewayId && selected?.type === "parallelGateway" ? <Button type="button" disabled={inspection.branches!.length >= 8} onClick={() => add("serviceTask")}>Add parallel path</Button> : null}</Stack>
      {selected ? <Stack spacing={1} sx={{ minWidth: 260 }}><Typography variant="h3">Selected element</Typography><label>Display name<input aria-label="Canvas element name" disabled={!inspection.editable} value={selected.name} onChange={(event) => { const changed = renameCanvasNode(source, selected.id, event.target.value); if (changed) onChange(changed); }} /></label><Typography variant="caption">{labels[selected.type] ?? selected.type} · {selected.id}</Typography>{selected.type !== "startEvent" && selected.type !== "endEvent" ? <Button type="button" disabled={!canRemoveCanvasNode(source, selected.id)} onClick={() => { const changed = removeCanvasNode(source, selected.id); if (changed) { setSelectedId(""); onChange(changed); } }}>Remove element</Button> : null}</Stack> : null}
    </Stack>
  </Paper>;
}
