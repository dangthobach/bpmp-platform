import { describe, expect, it } from "vitest";
import { browserQueryLimits, compileQuery, newGroup, type RelationNode } from "./queryDsl";

describe("process query DSL", () => {
  it("compiles nested process and event expressions", () => {
    const root = newGroup("process");
    const first = root.children[0]!;
    if (first.type !== "predicate") throw new Error("expected predicate");
    first.rawValue = "approval";
    const relation: RelationNode = {
      id: "relation-1",
      type: "relation",
      scope: "process",
      quantifier: "RELATION_QUANTIFIER_EXISTS",
      child: newGroup("event"),
    };
    for (const child of relation.child.type === "group" ? relation.child.children : []) {
      if (child.type === "predicate") child.rawValue = "completed";
    }
    root.children[1] = relation;

    const result = compileQuery(root);
    expect(result.errors).toEqual({});
    expect(result.summary).toEqual({ depth: 4, predicates: 3, relations: 1 });
    expect(result.expression).toHaveProperty("group.expressions.1.event_relation");
  });

  it("rejects empty groups, unsafe integers, empty IN items and excessive nesting", () => {
    const empty = newGroup("process");
    empty.children = [];
    expect(compileQuery(empty).errors[empty.id]).toContain("at least two");

    const root = newGroup("process");
    const integer = root.children[0]!;
    const list = root.children[1]!;
    if (integer.type !== "predicate" || list.type !== "predicate") throw new Error("expected predicates");
    integer.field = "PROCESS_FIELD_DURATION_MS";
    integer.rawValue = "9007199254740992";
    list.operator = "COMPARISON_OPERATOR_IN";
    list.rawValue = "a,,b";
    const result = compileQuery(root);
    expect(result.errors[integer.id]).toContain("safe whole number");
    expect(result.errors[list.id]).toContain("without empty items");

    let nested = root.children[0]!;
    for (let index = 0; index < browserQueryLimits.maxDepth; index++) {
      nested = { id: `not-${index}`, type: "negated", scope: "process", child: nested };
    }
    root.children[0] = nested;
    expect(Object.values(compileQuery(root).errors).some((message) => message.includes("nesting depth"))).toBe(true);
  });

  it("emits only typed text transforms and coalesce values", () => {
    const root = newGroup("process");
    const predicate = root.children[0]!;
    const second = root.children[1]!;
    if (predicate.type !== "predicate" || second.type !== "predicate") throw new Error("expected predicates");
    predicate.rawValue = "approval";
    predicate.textTransform = "TEXT_TRANSFORM_TRIM_LOWER";
    predicate.coalesceValue = "unknown";
    second.rawValue = "completed";
    const result = compileQuery(root);
    expect(result.errors).toEqual({});
    expect(result.expression).toHaveProperty(
      "group.expressions.0.predicate.text_transform",
      "TEXT_TRANSFORM_TRIM_LOWER",
    );
    expect(result.expression).toHaveProperty(
      "group.expressions.0.predicate.coalesce_value.string_value",
      "unknown",
    );
  });

  it("compiles inclusive numeric and UTC date ranges with typed transforms", () => {
    const root = newGroup("process");
    const numeric = root.children[0]!;
    const date = root.children[1]!;
    if (numeric.type !== "predicate" || date.type !== "predicate") throw new Error("expected predicates");
    numeric.field = "PROCESS_FIELD_DURATION_MS";
    numeric.operator = "COMPARISON_OPERATOR_BETWEEN";
    numeric.rawValue = "-500";
    numeric.rangeEnd = "500";
    numeric.numericTransform = "NUMERIC_TRANSFORM_ABS";
    date.field = "PROCESS_FIELD_COMPLETED_AT";
    date.operator = "COMPARISON_OPERATOR_NOT_BETWEEN";
    date.rawValue = "2026-10-01T00:00";
    date.rangeEnd = "2026-11-01T00:00";
    date.dateTransform = "DATE_TRANSFORM_UTC_DAY";

    const result = compileQuery(root);
    expect(result.errors).toEqual({});
    expect(result.expression).toHaveProperty("group.expressions.0.predicate.values.1.integer_value", 500);
    expect(result.expression).toHaveProperty("group.expressions.0.predicate.numeric_transform", "NUMERIC_TRANSFORM_ABS");
    expect(result.expression).toHaveProperty("group.expressions.1.predicate.date_transform", "DATE_TRANSFORM_UTC_DAY");
  });

  it("rejects reversed ranges and transforms on incompatible fields", () => {
    const root = newGroup("process");
    const range = root.children[0]!;
    const invalidTransform = root.children[1]!;
    if (range.type !== "predicate" || invalidTransform.type !== "predicate") throw new Error("expected predicates");
    range.field = "PROCESS_FIELD_DURATION_MS";
    range.operator = "COMPARISON_OPERATOR_BETWEEN";
    range.rawValue = "20";
    range.rangeEnd = "10";
    invalidTransform.rawValue = "approval";
    invalidTransform.numericTransform = "NUMERIC_TRANSFORM_ABS";

    const result = compileQuery(root);
    expect(result.errors[range.id]).toContain("Range start");
    expect(result.errors[invalidTransform.id]).toContain("Numeric transforms");
  });
});
