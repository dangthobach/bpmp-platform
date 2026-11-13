// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { inspectBpmn } from "./bpmnCanvas";
import { BpmnCanvas } from "./VisualBpmnCanvas";

const parallel = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p"><bpmn:startEvent id="start"/><bpmn:parallelGateway id="split"/><bpmn:serviceTask id="left"/><bpmn:userTask id="right"/><bpmn:parallelGateway id="join"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="f0" sourceRef="start" targetRef="split"/><bpmn:sequenceFlow id="f1" sourceRef="split" targetRef="left"/><bpmn:sequenceFlow id="f2" sourceRef="split" targetRef="right"/><bpmn:sequenceFlow id="f3" sourceRef="left" targetRef="join"/><bpmn:sequenceFlow id="f4" sourceRef="right" targetRef="join"/><bpmn:sequenceFlow id="f5" sourceRef="join" targetRef="end"/></bpmn:process></bpmn:definitions>`;

describe("BPMN canvas controls",()=>{
  it("adds a parallel path through the in-memory model change handler",()=>{
    let changed="";let sequence=1;vi.stubGlobal("crypto",{randomUUID:()=>`00000000-0000-4000-8000-${String(sequence++).padStart(12,"0")}`});
    render(<BpmnCanvas source={parallel} onChange={(value)=>{changed=value}}/>);
    fireEvent.click(screen.getByRole("button",{name:/Parallel gateway.*split/s}));
    fireEvent.click(screen.getByRole("button",{name:"Add parallel path"}));
    expect(inspectBpmn(changed).branches).toHaveLength(3);
    expect(changed).toContain("Service task");
  });
});
