export const BPMN_MODEL_NS = "http://www.omg.org/spec/BPMN/20100524/MODEL";

export type EditableTaskKind = "serviceTask" | "userTask";
export interface CanvasNode { id: string; name: string; type: string }
export interface CanvasEdge { id: string; sourceId: string; targetId: string; name: string; condition: string }
export interface CanvasInspection { nodes: CanvasNode[]; edges: CanvasEdge[]; editable: boolean; reason?: string; topology?: "linear" | "gateway"; branches?: CanvasNode[][]; splitGatewayId?: string; joinGatewayId?: string; defaultFlowId?:string }

const nodeKinds = new Set(["startEvent", "endEvent", "serviceTask", "userTask", "scriptTask", "exclusiveGateway", "parallelGateway"]);
const metadataKinds = new Set(["laneSet", "extensionElements", "documentation"]);

function parse(source: string): { document: XMLDocument; process: Element; prefix: string } | { error: string } {
  const document = new DOMParser().parseFromString(source, "application/xml");
  if (document.querySelector("parsererror")) return { error: "The BPMN XML cannot be parsed." };
  const definitions = document.documentElement;
  if (definitions.localName !== "definitions" || definitions.namespaceURI !== BPMN_MODEL_NS) return { error: "Expected a BPMN 2.0 definitions document." };
  const processes = Array.from(document.getElementsByTagNameNS(BPMN_MODEL_NS, "process"));
  if (processes.length !== 1) return { error: "The visual editor currently supports one BPMN process per draft." };
  const prefix = definitions.lookupPrefix(BPMN_MODEL_NS) || "bpmn";
  return { document, process: processes[0]!, prefix };
}

function splitDefaultFlow(process:Element,gatewayId:string):string|undefined {
  const gateway=Array.from(process.children).find((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.getAttribute("id")===gatewayId);if(!gateway)return undefined;const defaultFlow=gateway.getAttribute("default");return defaultFlow&&Array.from(process.children).some((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.localName==="sequenceFlow"&&element.getAttribute("id")===defaultFlow&&element.getAttribute("sourceRef")===gatewayId)?defaultFlow:undefined;
}

export function inspectBpmn(source: string): CanvasInspection {
  const parsed = parse(source);
  if ("error" in parsed) return { nodes: [], edges: [], editable: false, reason: parsed.error };
  const directChildren = Array.from(parsed.process.children).filter((element) => element.namespaceURI === BPMN_MODEL_NS);
  const elements = directChildren.filter((element) => nodeKinds.has(element.localName));
  const flows = directChildren.filter((element) => element.localName === "sequenceFlow");
  const unsupported = directChildren.find((element) => !nodeKinds.has(element.localName) && element.localName !== "sequenceFlow" && !metadataKinds.has(element.localName));
  const nodes = elements.map((element) => ({ id: element.getAttribute("id") ?? "", name: element.getAttribute("name") ?? "", type: element.localName }));
  const edges: CanvasEdge[] = flows.map((flow) => ({ id:flow.getAttribute("id") ?? "", sourceId:flow.getAttribute("sourceRef") ?? "", targetId:flow.getAttribute("targetRef") ?? "", name:flow.getAttribute("name") ?? "", condition:flow.getElementsByTagNameNS(BPMN_MODEL_NS,"conditionExpression")[0]?.textContent ?? "" }));
  const readOnly = (reason: string): CanvasInspection => ({ nodes, edges, editable: false, reason });
  if (unsupported) return readOnly(`Visual editing is disabled because “${unsupported.localName}” is outside the current visual editor subset. The BPMN source is preserved.`);
  if (nodes.length < 2 || nodes.some((node) => !node.id)) return readOnly("Add at least one start and one end event with IDs before visual editing.");
  const starts = nodes.filter((node) => node.type === "startEvent");
  const ends = nodes.filter((node) => node.type === "endEvent");
  if (starts.length !== 1 || ends.length !== 1) return readOnly("Visual editing currently requires exactly one start event and one end event.");
  const nodeIds = new Set(nodes.map((node) => node.id));
  if (nodeIds.size !== nodes.length) return readOnly("Duplicate BPMN element IDs prevent safe visual editing.");
  const allIds = [...nodes.map((node) => node.id), ...edges.map((edge) => edge.id)];
  if (allIds.some((id) => !id) || new Set(allIds).size !== allIds.length) return readOnly("Duplicate or missing BPMN element IDs prevent safe visual editing.");
  const incoming = new Map<string, CanvasEdge[]>();
  const outgoing = new Map<string, CanvasEdge[]>();
  for (const node of nodes) { incoming.set(node.id, []); outgoing.set(node.id, []); }
  for (const edge of edges) {
    if (!nodeIds.has(edge.sourceId) || !nodeIds.has(edge.targetId)) return readOnly("A sequence flow references an element outside the supported visual subset.");
    outgoing.get(edge.sourceId)!.push(edge);
    incoming.get(edge.targetId)!.push(edge);
  }
  let linear = true;
  for (const node of nodes) {
    const inputCount = incoming.get(node.id)!.length;
    const outputCount = outgoing.get(node.id)!.length;
    if ((node.type === "startEvent" ? inputCount !== 0 : inputCount !== 1) || (node.type === "endEvent" ? outputCount !== 0 : outputCount !== 1)) {
      linear = false;
    }
  }
  if (linear) {
    const ordered: CanvasNode[] = [];
    let current: CanvasNode | undefined = starts[0];
    const visited = new Set<string>();
    while (current && !visited.has(current.id)) { ordered.push(current); visited.add(current.id); const nextId = outgoing.get(current.id)![0]?.targetId; current = nextId ? nodes.find((node) => node.id === nextId) : undefined; }
    if (visited.size === nodes.length && ordered.at(-1)?.type === "endEvent" && edges.length === nodes.length - 1) return { nodes:ordered, edges, editable:true, topology:"linear" };
  }
  const gateways=nodes.filter((node)=>node.type==="exclusiveGateway"||node.type==="parallelGateway");
  if(gateways.length!==2)return readOnly("The visual editor supports a linear flow or one paired gateway split/join. Other branching stays source-only.");
  const split=gateways.find((node)=>incoming.get(node.id)!.length===1&&outgoing.get(node.id)!.length>=2);
  const join=gateways.find((node)=>incoming.get(node.id)!.length>=2&&outgoing.get(node.id)!.length===1);
  if(!split||!join||split.id===join.id||split.type!==join.type)return readOnly("A visual gateway flow needs a matching split and join of the same gateway type.");
  const start=starts[0]!,end=ends[0]!;
  const startEdge=outgoing.get(start.id)![0],joinExit=outgoing.get(join.id)![0];
  if(outgoing.get(start.id)!.length!==1||startEdge?.targetId!==split.id||incoming.get(join.id)!.length!==outgoing.get(split.id)!.length||joinExit?.targetId!==end.id||incoming.get(end.id)!.length!==1)return readOnly("The split/join must form one complete path from the unique start to the unique end.");
  const branches:CanvasNode[][]=[];
  for(const edge of outgoing.get(split.id)!){const task=nodes.find((node)=>node.id===edge.targetId);if(!task||["startEvent","endEvent","exclusiveGateway","parallelGateway"].includes(task.type)||incoming.get(task.id)!.length!==1||outgoing.get(task.id)!.length!==1||outgoing.get(task.id)![0]!.targetId!==join.id)return readOnly("Each supported gateway branch must contain exactly one task between the paired gateways.");branches.push([task]);}
  if(branches.length<2||branches.length>8||new Set(branches.flat().map((node)=>node.id)).size!==nodes.length-4)return readOnly("The visual gateway editor supports two to eight single-task branches without loops or extra nodes.");
  if(edges.length!==2+branches.length*2)return readOnly("Extra sequence flows prevent safe visual gateway editing.");
  const splitElement=Array.from(parsed.process.children).find((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.getAttribute("id")===split.id);
  const configuredDefault=splitElement?.getAttribute("default")||"";
  const defaultFlowId=splitDefaultFlow(parsed.process,split.id);
  if(configuredDefault&&!defaultFlowId)return readOnly("The gateway default flow does not reference one of its outgoing sequence flows.");
  if(split.type==="exclusiveGateway"&&outgoing.get(split.id)!.some((edge)=>edge.id!==defaultFlowId&&!edge.condition.trim()))return readOnly("Exclusive gateway branches need conditions and may have one default flow; use source mode to add decision logic.");
  const branchEdges=edges.filter((edge)=>edge.sourceId===split.id||branches.some((branch)=>branch[0]!.id===edge.sourceId));
  if(split.type==="parallelGateway"&&(configuredDefault||branchEdges.some((edge)=>edge.condition.trim())))return readOnly("Parallel gateway paths cannot carry conditions or a default flow in the visual editor.");
  return {nodes:[start,split,...branches.flat(),join,end],edges,editable:true,topology:"gateway",branches,splitGatewayId:split.id,joinGatewayId:join.id,...(defaultFlowId?{defaultFlowId}:{})};
}

function mutableDocument(source: string): { document: XMLDocument; process: Element; prefix: string; nodes: CanvasNode[]; inspection:CanvasInspection } | null {
  const inspection = inspectBpmn(source);
  if (!inspection.editable) return null;
  const parsed = parse(source);
  if ("error" in parsed) return null;
  return { ...parsed, nodes: inspection.nodes, inspection };
}

function serialize(document: XMLDocument): string {
  return new XMLSerializer().serializeToString(document);
}

export function renameCanvasNode(source: string, nodeId: string, name: string): string | null {
  const state = mutableDocument(source);
  if (!state) return null;
  const element = Array.from(state.process.children).find((child) => child.namespaceURI === BPMN_MODEL_NS && child.getAttribute("id") === nodeId);
  if (!element) return null;
  if (name.trim()) element.setAttribute("name", name.trim());
  else element.removeAttribute("name");
  return serialize(state.document);
}

export function insertTaskBeforeEnd(source: string, type: EditableTaskKind): string | null {
  const state = mutableDocument(source);
  if (!state || state.inspection.topology!=="linear") return null;
  const end = state.nodes.at(-1);
  const previous = state.nodes.at(-2);
  if (!end || !previous) return null;
  const priorFlow = Array.from(state.process.children).find((element) => element.namespaceURI === BPMN_MODEL_NS && element.localName === "sequenceFlow" && element.getAttribute("sourceRef") === previous.id && element.getAttribute("targetRef") === end.id);
  if (!priorFlow) return null;
  const nodeId = `node_${crypto.randomUUID().replaceAll("-", "")}`;
  const flowId = `flow_${crypto.randomUUID().replaceAll("-", "")}`;
  const nodeElement = state.document.createElementNS(BPMN_MODEL_NS, `${state.prefix}:${type}`);
  nodeElement.setAttribute("id", nodeId);
  nodeElement.setAttribute("name", type === "serviceTask" ? "Service task" : "User task");
  const firstFlow = Array.from(state.process.children).find((element) => element.namespaceURI === BPMN_MODEL_NS && element.localName === "sequenceFlow");
  state.process.insertBefore(nodeElement, firstFlow ?? null);
  priorFlow.setAttribute("targetRef", nodeId);
  const newFlow = state.document.createElementNS(BPMN_MODEL_NS, `${state.prefix}:sequenceFlow`);
  newFlow.setAttribute("id", flowId);
  newFlow.setAttribute("sourceRef", nodeId);
  newFlow.setAttribute("targetRef", end.id);
  state.process.appendChild(newFlow);
  return serialize(state.document);
}

export function removeCanvasNode(source: string, nodeId: string): string | null {
  const state = mutableDocument(source);
  if (!state || state.nodes[0]?.id === nodeId || state.nodes.at(-1)?.id === nodeId) return null;
  if(state.inspection.topology==="gateway")return removeParallelBranch(source,nodeId);
  const index = state.nodes.findIndex((node) => node.id === nodeId);
  if (index < 1 || index >= state.nodes.length - 1) return null;
  const previous = state.nodes[index - 1]!;
  const next = state.nodes[index + 1]!;
  const edges = Array.from(state.process.children).filter((element) => element.namespaceURI === BPMN_MODEL_NS && element.localName === "sequenceFlow");
  const incoming = edges.find((edge) => edge.getAttribute("sourceRef") === previous.id && edge.getAttribute("targetRef") === nodeId);
  const outgoing = edges.find((edge) => edge.getAttribute("sourceRef") === nodeId && edge.getAttribute("targetRef") === next.id);
  const node = Array.from(state.process.children).find((element) => element.namespaceURI === BPMN_MODEL_NS && element.getAttribute("id") === nodeId);
  if (!incoming || !outgoing || !node) return null;
  const incomingHasLogic = Boolean(incoming.getAttribute("name") || incoming.getElementsByTagNameNS(BPMN_MODEL_NS, "conditionExpression").length);
  const outgoingHasLogic = Boolean(outgoing.getAttribute("name") || outgoing.getElementsByTagNameNS(BPMN_MODEL_NS, "conditionExpression").length);
  if (incomingHasLogic && outgoingHasLogic) return null;
  if (outgoingHasLogic) {
    incoming.remove();
    outgoing.setAttribute("sourceRef", previous.id);
  } else {
    incoming.setAttribute("targetRef", next.id);
    outgoing.remove();
  }
  node.remove();
  return serialize(state.document);
}

export function canRemoveCanvasNode(source: string, nodeId: string): boolean {
  const state = mutableDocument(source);
  if (!state || state.nodes[0]?.id === nodeId || state.nodes.at(-1)?.id === nodeId) return false;
  if(state.inspection.topology==="gateway")return canRemoveParallelBranch(state.inspection,nodeId);
  const index = state.nodes.findIndex((node) => node.id === nodeId);
  if (index < 1 || index >= state.nodes.length - 1) return false;
  const previous = state.nodes[index - 1]!;
  const next = state.nodes[index + 1]!;
  const edges = Array.from(state.process.children).filter((element) => element.namespaceURI === BPMN_MODEL_NS && element.localName === "sequenceFlow");
  const incoming = edges.find((edge) => edge.getAttribute("sourceRef") === previous.id && edge.getAttribute("targetRef") === nodeId);
  const outgoing = edges.find((edge) => edge.getAttribute("sourceRef") === nodeId && edge.getAttribute("targetRef") === next.id);
  if (!incoming || !outgoing) return false;
  const hasLogic = (edge: Element) => Boolean(edge.getAttribute("name") || edge.getElementsByTagNameNS(BPMN_MODEL_NS, "conditionExpression").length);
  return !(hasLogic(incoming) && hasLogic(outgoing));
}

export function insertParallelBranch(source:string,gatewayId:string,type:EditableTaskKind):string|null {
  const state=mutableDocument(source);const inspection=state?.inspection;if(!state||inspection?.topology!=="gateway"||inspection.nodes.find((node)=>node.id===gatewayId)?.type!=="parallelGateway"||inspection.branches!.length>=8)return null;
  const split=Array.from(state.process.children).find((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.getAttribute("id")===inspection.splitGatewayId);const joinId=inspection.joinGatewayId;if(!split||!joinId)return null;
  const nodeId=`node_${crypto.randomUUID().replaceAll("-","")}`,incomingId=`flow_${crypto.randomUUID().replaceAll("-","")}`,outgoingId=`flow_${crypto.randomUUID().replaceAll("-","")}`;
  const node=state.document.createElementNS(BPMN_MODEL_NS,`${state.prefix}:${type}`);node.setAttribute("id",nodeId);node.setAttribute("name",type==="serviceTask"?"Service task":"User task");state.process.insertBefore(node,Array.from(state.process.children).find((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.localName==="sequenceFlow")??null);
  const incoming=state.document.createElementNS(BPMN_MODEL_NS,`${state.prefix}:sequenceFlow`);incoming.setAttribute("id",incomingId);incoming.setAttribute("sourceRef",inspection.splitGatewayId!);incoming.setAttribute("targetRef",nodeId);const outgoing=state.document.createElementNS(BPMN_MODEL_NS,`${state.prefix}:sequenceFlow`);outgoing.setAttribute("id",outgoingId);outgoing.setAttribute("sourceRef",nodeId);outgoing.setAttribute("targetRef",joinId);state.process.append(incoming,outgoing);return serialize(state.document);
}

function canRemoveParallelBranch(inspection:CanvasInspection,nodeId:string):boolean {
  if(inspection.topology!=="gateway"||inspection.nodes.find((node)=>node.id===inspection.splitGatewayId)?.type!=="parallelGateway")return false;
  const branch=inspection.branches?.find((items)=>items.some((node)=>node.id===nodeId));if(!branch||inspection.branches!.length<=2)return false;
  const task=branch[0]!;const incoming=inspection.edges.find((edge)=>edge.sourceId===inspection.splitGatewayId&&edge.targetId===task.id);const split=inspection.nodes.find((node)=>node.id===inspection.splitGatewayId);
  const outgoing=inspection.edges.find((edge)=>edge.sourceId===task.id&&edge.targetId===inspection.joinGatewayId);
  return Boolean(incoming&&outgoing&&!incoming.condition&&!outgoing.condition&&incoming.id!==inspection.defaultFlowId&&split);
}
function removeParallelBranch(source:string,nodeId:string):string|null {
  const state=mutableDocument(source);if(!state||!canRemoveParallelBranch(state.inspection,nodeId))return null;const node=Array.from(state.process.children).find((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.getAttribute("id")===nodeId);if(!node)return null;
  for(const edge of Array.from(state.process.children).filter((element)=>element.namespaceURI===BPMN_MODEL_NS&&element.localName==="sequenceFlow"&&(element.getAttribute("sourceRef")===nodeId||element.getAttribute("targetRef")===nodeId)))edge.remove();node.remove();return serialize(state.document);
}
