// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { readDmnTable, writeDmnTable } from "./dmnTable";

const source = `<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="d"><decision id="decision" name="Decision"><decisionTable id="table" hitPolicy="FIRST"><input id="input" label="Value"><inputExpression id="expr"><text>value</text></inputExpression></input><output id="output" name="Result"/><rule id="r1"><inputEntry><text>&gt; 5</text></inputEntry><outputEntry><text>yes</text></outputEntry></rule></decisionTable></decision></definitions>`;

describe("structured DMN source adapter", () => {
  it("reads and writes a simple decision table while preserving its definition envelope", () => {
    const inspected = readDmnTable(source);
    expect(inspected.table?.rules[0]).toEqual({id:"r1",input:"> 5",output:"yes"});
    const updated = writeDmnTable(source,{...inspected.table!,rules:[...inspected.table!.rules,{id:"r2",input:"<= 5",output:"no"}]});
    expect(updated).toContain('id="d"');
    expect(readDmnTable(updated!).table?.rules).toHaveLength(2);
    expect(readDmnTable(updated!).table?.rules[1]?.output).toBe("no");
  });

  it("keeps unsupported multi-input tables out of structured write mode", () => {
    const extended = source.replace('</decisionTable>', '<input id="second"><inputExpression><text>second</text></inputExpression></input></decisionTable>');
    expect(readDmnTable(extended).table).toBeUndefined();
    expect(readDmnTable(extended).reason).toContain("one input and one output");
  });
});
