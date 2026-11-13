// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { canRemoveCanvasNode, inspectBpmn, insertParallelBranch, insertTaskBeforeEnd, removeCanvasNode, renameCanvasNode } from "./bpmnCanvas";

const linear = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ext="urn:custom">
  <bpmn:process id="p"><bpmn:extensionElements><ext:tag value="keep"/></bpmn:extensionElements>
    <bpmn:startEvent id="start"/><bpmn:serviceTask id="work" name="Work"><bpmn:extensionElements><ext:job type="invoice"/></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/><bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`;
const parallel = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p"><bpmn:startEvent id="start"/><bpmn:parallelGateway id="split" gatewayDirection="Diverging"/><bpmn:serviceTask id="left" name="Left"/><bpmn:userTask id="right" name="Right"/><bpmn:parallelGateway id="join" gatewayDirection="Converging"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="f0" sourceRef="start" targetRef="split"/><bpmn:sequenceFlow id="f1" sourceRef="split" targetRef="left"/><bpmn:sequenceFlow id="f2" sourceRef="split" targetRef="right"/><bpmn:sequenceFlow id="f3" sourceRef="left" targetRef="join"/><bpmn:sequenceFlow id="f4" sourceRef="right" targetRef="join"/><bpmn:sequenceFlow id="f5" sourceRef="join" targetRef="end"/></bpmn:process></bpmn:definitions>`;

afterEach(() => vi.unstubAllGlobals());

describe("BPMN visual editing safety", () => {
  it("edits a linear path while preserving unrelated extension XML", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "00000000-0000-4000-8000-000000000001" });
    expect(inspectBpmn(linear)).toMatchObject({ editable: true, nodes: [{ id: "start" }, { id: "work" }, { id: "end" }] });
    const inserted = insertTaskBeforeEnd(linear, "userTask");
    expect(inserted).toBeTruthy();
    expect(inserted).toContain("<ext:tag value=\"keep\"");
    expect(inserted).toContain("<ext:job type=\"invoice\"");
    const inspection = inspectBpmn(inserted!);
    expect(inspection.editable).toBe(true);
    expect(inspection.nodes).toHaveLength(4);
    const task = inspection.nodes[2]!;
    const renamed = renameCanvasNode(inserted!, task.id, "Reviewed by finance");
    expect(renamed).toContain('name="Reviewed by finance"');
    const removed = removeCanvasNode(renamed!, task.id);
    expect(inspectBpmn(removed!).nodes.map((node) => node.id)).toEqual(["start", "work", "end"]);
  });

  it("keeps branching processes source-only instead of flattening their logic", () => {
    const branching = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p">
      <bpmn:startEvent id="start"/><bpmn:serviceTask id="a"/><bpmn:serviceTask id="b"/><bpmn:endEvent id="end"/>
      <bpmn:sequenceFlow id="1" sourceRef="start" targetRef="a"/><bpmn:sequenceFlow id="2" sourceRef="start" targetRef="b"/><bpmn:sequenceFlow id="3" sourceRef="a" targetRef="end"/><bpmn:sequenceFlow id="4" sourceRef="b" targetRef="end"/>
    </bpmn:process></bpmn:definitions>`;
    const result = inspectBpmn(branching);
    expect(result.editable).toBe(false);
    expect(result.reason).toContain("branching");
    expect(insertTaskBeforeEnd(branching, "userTask")).toBeNull();
  });

  it("renders and safely edits a paired parallel gateway with bounded branches",()=>{
    let sequence=2;vi.stubGlobal("crypto",{randomUUID:()=>`00000000-0000-4000-8000-${String(sequence++).padStart(12,"0")}`});
    const before=inspectBpmn(parallel);expect(before).toMatchObject({editable:true,topology:"gateway",splitGatewayId:"split",joinGatewayId:"join"});expect(before.branches).toHaveLength(2);
    const expanded=insertParallelBranch(parallel,"split","serviceTask");expect(expanded).toBeTruthy();const after=inspectBpmn(expanded!);expect(after.editable,after.reason).toBe(true);expect(after.branches).toHaveLength(3);
    const newNode=after.branches!.find((branch)=>!before.nodes.some((node)=>node.id===branch[0]!.id))![0]!;expect(canRemoveCanvasNode(expanded!,newNode.id)).toBe(true);
    const reduced=removeCanvasNode(expanded!,newNode.id);expect(inspectBpmn(reduced!).branches).toHaveLength(2);expect(inspectBpmn(reduced!).editable).toBe(true);
  });

  it("keeps unpaired gateways and nested branching source-only",()=>{
    const unsupported=parallel.replace('<bpmn:parallelGateway id="join" gatewayDirection="Converging"/>','');
    expect(inspectBpmn(unsupported).editable).toBe(false);
    expect(insertParallelBranch(unsupported,"split","serviceTask")).toBeNull();
  });

  it("shows exclusive gateway branch conditions but disables destructive branch edits",()=>{
    const exclusive=parallel.replaceAll("parallelGateway","exclusiveGateway").replace('<bpmn:exclusiveGateway id="split" gatewayDirection="Diverging"/>','<bpmn:exclusiveGateway id="split" gatewayDirection="Diverging" default="f2"/>').replace('<bpmn:sequenceFlow id="f1" sourceRef="split" targetRef="left"/>','<bpmn:sequenceFlow id="f1" sourceRef="split" targetRef="left"><bpmn:conditionExpression>amount &gt; 0</bpmn:conditionExpression></bpmn:sequenceFlow>');
    const inspection=inspectBpmn(exclusive);expect(inspection.editable).toBe(true);expect(inspection.edges.find((edge)=>edge.id==="f1")?.condition).toBe("amount > 0");expect(canRemoveCanvasNode(exclusive,"left")).toBe(false);
  });
});
