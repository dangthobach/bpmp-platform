import { describe, expect, it } from "vitest";
import { compileQuery } from "./queryDsl";
import { processQueryFromSearch } from "./deepLink";

describe("process query drill-through", () => {
  it("prefills a valid workflow predicate and the requested supported scope", () => {
    const { root, scope } = processQueryFromSearch("?workflow_type=procurement&scope=QUERY_SCOPE_ACTIVE");
    const compiled = compileQuery(root);

    expect(scope).toBe("QUERY_SCOPE_ACTIVE");
    expect(compiled.errors).toEqual({});
    expect(compiled.expression).toMatchObject({
      predicate: {
        field: "PROCESS_FIELD_WORKFLOW_TYPE",
        operator: "COMPARISON_OPERATOR_EQUAL",
        values: [{ string_value: "procurement" }],
      },
    });
  });

  it("ignores unsupported scopes and empty workflow values", () => {
    const { root, scope } = processQueryFromSearch("?workflow_type=%20&scope=QUERY_SCOPE_DELETED");

    expect(scope).toBe("QUERY_SCOPE_COMPLETED");
    expect(root.type).toBe("group");
  });
});
