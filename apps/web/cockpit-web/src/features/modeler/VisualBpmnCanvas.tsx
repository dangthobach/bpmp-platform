import { useMemo, useState } from "react";
import { ArrowDown, Plus, Trash2 } from "lucide-react";
import { Button } from "../../components/Button";
import { canRemoveCanvasNode, inspectBpmn, insertParallelBranch, insertTaskBeforeEnd, removeCanvasNode, renameCanvasNode, type CanvasNode, type EditableTaskKind } from "./bpmnCanvas";

const nodeLabels: Record<string, string> = {
  startEvent: "Start event", endEvent: "End event", serviceTask: "Service task", userTask: "User task", scriptTask: "Script task", exclusiveGateway:"Exclusive gateway", parallelGateway:"Parallel gateway",
};

export function BpmnCanvas({ source, onChange }: { source: string; onChange: (value: string) => void }) {
  const inspection = useMemo(() => inspectBpmn(source), [source]);
  const [selectedId, setSelectedId] = useState("");
  const selected = inspection.nodes.find((node) => node.id === selectedId) ?? inspection.nodes[0];
  const addTask = (type: EditableTaskKind) => {
    const changed = insertTaskBeforeEnd(source, type);
    if (changed) {
      const after = inspectBpmn(changed);
      setSelectedId(after.nodes.at(-2)?.id ?? "");
      onChange(changed);
    }
  };
  const addParallelBranch=(type:EditableTaskKind)=>{if(!selected)return;const changed=insertParallelBranch(source,selected.id,type);if(changed){const after=inspectBpmn(changed);const node=after.nodes.find((item)=>!inspection.nodes.some((existing)=>existing.id===item.id));if(node)setSelectedId(node.id);onChange(changed)}};
  const renderNode=(node:CanvasNode)=><button type="button" key={node.id} className={`bpmn-canvas__node bpmn-canvas__node--${node.type} ${selected?.id===node.id?"is-selected":""}`} onClick={()=>setSelectedId(node.id)} disabled={!inspection.editable}><span>{nodeLabels[node.type]??node.type}</span><strong>{node.name||node.id}</strong><small className="mono">{node.id}</small></button>;

  return <section className="bpmn-canvas" aria-label="Visual BPMN editor">
    <header className="bpmn-canvas__header"><div><h3>Process canvas</h3><p>Linear paths and paired one-level gateways are editable; unsupported constructs stay source-only.</p></div><span className={`bpmn-canvas__mode ${inspection.editable ? "is-editable" : "is-readonly"}`}>{inspection.editable ? inspection.topology==="gateway"?"Gateway flow":"Editable path" : "Source view"}</span></header>
    {inspection.reason ? <p className="bpmn-canvas__notice" role="status">{inspection.reason}</p> : null}
    <div className="bpmn-canvas__body">
      <div className="bpmn-canvas__flow" aria-label="BPMN flow">
        {inspection.topology==="gateway"&&inspection.branches?<div className="bpmn-canvas__gateway-flow"><div className="bpmn-canvas__gateway-stage">{renderNode(inspection.nodes[0]!)}<ArrowDown className="bpmn-canvas__edge" size={17}/>{renderNode(inspection.nodes[1]!)}</div><div className="bpmn-canvas__branches">{inspection.branches.map((branch,index)=>{const flow=inspection.edges.find((edge)=>edge.sourceId===inspection.splitGatewayId&&edge.targetId===branch[0]!.id);return <div className="bpmn-canvas__branch" key={branch[0]!.id}><span className="bpmn-canvas__branch-label">Path {index+1}</span><span className="bpmn-canvas__branch-edge">{flow?.condition||flow?.name||(inspection.defaultFlowId===flow?.id?"Default path":inspection.nodes.find((node)=>node.id===inspection.splitGatewayId)?.type==="parallelGateway"?"Parallel path":"Unconditional path")}</span>{renderNode(branch[0]!)}<span className="bpmn-canvas__branch-edge">Join</span></div>})}</div><div className="bpmn-canvas__gateway-stage">{renderNode(inspection.nodes.at(-2)!)}<ArrowDown className="bpmn-canvas__edge" size={17}/>{renderNode(inspection.nodes.at(-1)!)}</div></div>:inspection.nodes.map((node,index)=><div className="bpmn-canvas__step" key={node.id}>{index>0?<ArrowDown className="bpmn-canvas__edge" size={17} aria-hidden="true"/>:null}{renderNode(node)}</div>)}
        {inspection.editable && inspection.nodes.length === 0 ? <p>Canvas unavailable for this BPMN source.</p> : null}
      </div>
      <aside className="bpmn-canvas__properties">
        <div className="bpmn-canvas__palette"><strong>Elements</strong>{inspection.topology==="linear"?<><Button icon={Plus} disabled={!inspection.editable} onClick={() => addTask("serviceTask")}>Service task</Button><Button icon={Plus} disabled={!inspection.editable} onClick={() => addTask("userTask")}>User task</Button></>:null}{inspection.topology==="gateway"&&selected?.id===inspection.splitGatewayId&&selected?.type==="parallelGateway"?<Button icon={Plus} disabled={inspection.branches!.length>=8} onClick={()=>addParallelBranch("serviceTask")}>Add parallel path</Button>:null}</div>
        {selected ? <div className="bpmn-canvas__property"><strong>Selected element</strong><label>Display name<input value={selected.name} disabled={!inspection.editable} onChange={(event) => { const changed = renameCanvasNode(source, selected.id, event.target.value); if (changed) onChange(changed); }} /></label><small>{nodeLabels[selected.type] ?? selected.type} · {selected.id}</small>
          {selected.type !== "startEvent" && selected.type !== "endEvent" ? <Button icon={Trash2} disabled={!canRemoveCanvasNode(source, selected.id)} onClick={() => { const changed = removeCanvasNode(source, selected.id); if (changed) { setSelectedId(""); onChange(changed); } }}>Remove element</Button> : null}
        </div> : null}
      </aside>
    </div>
  </section>;
}
