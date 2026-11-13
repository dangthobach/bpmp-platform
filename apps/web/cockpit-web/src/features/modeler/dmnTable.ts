export const DMN_MODEL_NS = "https://www.omg.org/spec/DMN/20191111/MODEL/";
export interface DmnRule { id: string; input: string; output: string }
export interface DmnTable { decisionName: string; inputName: string; inputExpression: string; outputName: string; hitPolicy: string; rules: DmnRule[] }
export const DMN_HIT_POLICIES = ["UNIQUE", "ANY", "FIRST", "PRIORITY", "RULE ORDER", "OUTPUT ORDER", "COLLECT"] as const;

function children(parent: Element, localName: string): Element[] {
  return Array.from(parent.children).filter((child) => child.namespaceURI === DMN_MODEL_NS && child.localName === localName);
}
function textValue(parent: Element, localName: string): string {
  const el = children(parent, localName)[0]; return el?.textContent ?? "";
}
function parse(source: string): XMLDocument | null {
  const document = new DOMParser().parseFromString(source, "application/xml");
  return document.querySelector("parsererror") || document.documentElement.namespaceURI !== DMN_MODEL_NS || document.documentElement.localName !== "definitions" ? null : document;
}

export function readDmnTable(source: string): { table?: DmnTable; reason?: string } {
  const document = parse(source); if (!document) return { reason: "Source is not a supported DMN 1.3 definitions document." };
  const decisions = Array.from(document.getElementsByTagNameNS(DMN_MODEL_NS, "decision"));
  if (decisions.length !== 1) return { reason: "Structured editing requires exactly one decision; use source mode to preserve this document." };
  const tables = Array.from(decisions[0]!.getElementsByTagNameNS(DMN_MODEL_NS, "decisionTable"));
  if (tables.length !== 1) return { reason: "Structured editing requires one decision table; use source mode to preserve this document." };
  const inputs = children(tables[0]!, "input"), outputs = children(tables[0]!, "output");
  if (inputs.length !== 1 || outputs.length !== 1) return { reason: "Structured editing currently supports one input and one output column; use source mode to preserve this document." };
  const rules = children(tables[0]!, "rule").map((rule) => ({ id: rule.getAttribute("id") ?? "", input: textValue(children(rule, "inputEntry")[0] ?? rule, "text"), output: textValue(children(rule, "outputEntry")[0] ?? rule, "text") }));
  return { table: { decisionName: decisions[0]!.getAttribute("name") ?? "Decision", inputName: inputs[0]!.getAttribute("label") ?? "Input", inputExpression: textValue(children(inputs[0]!, "inputExpression")[0] ?? inputs[0]!, "text"), outputName: outputs[0]!.getAttribute("name") ?? "Output", hitPolicy: tables[0]!.getAttribute("hitPolicy") ?? "UNIQUE", rules } };
}

export function writeDmnTable(source: string, table: DmnTable): string | null {
  const document = parse(source); if (!document) return null;
  const decision = Array.from(document.getElementsByTagNameNS(DMN_MODEL_NS, "decision"));
  if (decision.length !== 1) return null;
  const tables = Array.from(decision[0]!.getElementsByTagNameNS(DMN_MODEL_NS, "decisionTable")); if (tables.length !== 1) return null;
  const dmnTable = tables[0]!, inputs = children(dmnTable, "input"), outputs = children(dmnTable, "output"); if (inputs.length !== 1 || outputs.length !== 1) return null;
  decision[0]!.setAttribute("name", table.decisionName.trim() || "Decision");
  inputs[0]!.setAttribute("label", table.inputName.trim() || "Input"); outputs[0]!.setAttribute("name", table.outputName.trim() || "Output");
  dmnTable.setAttribute("hitPolicy", table.hitPolicy);
  const expression = children(inputs[0]!, "inputExpression")[0]; const expressionText = expression && children(expression, "text")[0]; if (!expressionText) return null; expressionText.textContent = table.inputExpression.trim() || "input";
  for (const old of children(dmnTable, "rule")) old.remove();
  const prefix = document.documentElement.lookupPrefix(DMN_MODEL_NS); const qname = (local: string) => prefix ? `${prefix}:${local}` : local;
  const make = (name: string) => document.createElementNS(DMN_MODEL_NS, qname(name));
  const makeEntry = (entryName: "inputEntry" | "outputEntry", value: string) => { const entry = make(entryName), text = make("text"); text.textContent = value; entry.appendChild(text); return entry; };
  for (const ruleData of table.rules) { const rule = make("rule"); rule.setAttribute("id", ruleData.id || `rule_${crypto.randomUUID().replaceAll("-", "")}`); rule.append(makeEntry("inputEntry", ruleData.input), makeEntry("outputEntry", ruleData.output)); dmnTable.appendChild(rule); }
  return new XMLSerializer().serializeToString(document);
}
