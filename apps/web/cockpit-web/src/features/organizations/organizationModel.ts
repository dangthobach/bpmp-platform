import type { NodeKind, OrganizationNode } from "./types";

export interface NamedCode {
  code: string;
  name: string;
}

const childKinds: Partial<Record<NodeKind, NodeKind>> = {
  GROUP: "SUBSIDIARY",
  SUBSIDIARY: "BRANCH",
  BRANCH: "DEPARTMENT",
};

export function childKind(parentKind: NodeKind): NodeKind | null {
  return childKinds[parentKind] ?? null;
}

export function parseNamedCodes(raw: string): NamedCode[] {
  const entries = raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line, index) => {
      const separator = line.indexOf(",");
      if (separator <= 0 || separator === line.length - 1) {
        throw new Error(`Line ${index + 1} must use CODE,Name`);
      }
      const code = line.slice(0, separator).trim();
      const name = line.slice(separator + 1).trim();
      if (!/^[A-Za-z0-9_]{1,64}$/.test(code)) {
        throw new Error(`Line ${index + 1} has an invalid code`);
      }
      return { code, name };
    });
  if (entries.length === 0) throw new Error("Enter at least one row");
  const normalizedCodes = entries.map(({ code }) => code.toLowerCase());
  if (new Set(normalizedCodes).size !== normalizedCodes.length) {
    throw new Error("Codes in the batch must be unique");
  }
  return entries;
}

export function eligibleMoveParents(
  node: OrganizationNode,
  nodes: readonly OrganizationNode[],
): OrganizationNode[] {
  const expectedParentKind = Object.entries(childKinds).find(
    ([, kind]) => kind === node.kind,
  )?.[0] as NodeKind | undefined;
  if (!expectedParentKind) return [];
  const subtreePrefix = `${node.path}.`;
  return nodes.filter(
    (candidate) =>
      candidate.kind === expectedParentKind &&
      candidate.id !== node.parent_id &&
      candidate.path !== node.path &&
      !candidate.path.startsWith(subtreePrefix),
  );
}
