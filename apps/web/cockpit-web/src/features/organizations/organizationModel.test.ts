import { describe, expect, it } from "vitest";
import { childKind, eligibleMoveParents, parseNamedCodes } from "./organizationModel";
import type { OrganizationNode } from "./types";

function node(
  id: string,
  kind: OrganizationNode["kind"],
  path: string,
  parentId: string | null,
): OrganizationNode {
  return {
    id,
    parent_id: parentId,
    code: id,
    name: id,
    kind,
    path,
    is_active: true,
  };
}

describe("organization model", () => {
  it("parses a bounded-operation input without losing names containing commas", () => {
    expect(parseNamedCodes("APAC,Asia Pacific\nEU,Europe, Middle East")).toEqual([
      { code: "APAC", name: "Asia Pacific" },
      { code: "EU", name: "Europe, Middle East" },
    ]);
  });

  it("rejects duplicate codes case-insensitively", () => {
    expect(() => parseNamedCodes("APAC,One\napac,Two")).toThrow("unique");
  });

  it("maps the fixed organization hierarchy", () => {
    expect(childKind("GROUP")).toBe("SUBSIDIARY");
    expect(childKind("DEPARTMENT")).toBeNull();
  });

  it("only offers compatible parents outside the moved subtree", () => {
    const nodes = [
      node("root", "GROUP", "root", null),
      node("s1", "SUBSIDIARY", "root.s1", "root"),
      node("s2", "SUBSIDIARY", "root.s2", "root"),
      node("b1", "BRANCH", "root.s1.b1", "s1"),
      node("b2", "BRANCH", "root.s2.b2", "s2"),
    ];
    expect(eligibleMoveParents(nodes[3]!, nodes).map(({ id }) => id)).toEqual(["s2"]);
  });
});
