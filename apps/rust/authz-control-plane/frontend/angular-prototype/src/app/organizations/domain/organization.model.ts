// Pure domain model — no Angular, no HTTP, no signals.
// Mirrors the back-end OrganizationListItem shape.

export type NodeKind = 'GROUP' | 'SUBSIDIARY' | 'BRANCH' | 'DEPARTMENT';

export interface Organization {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly rootPath: string;
  readonly nodeCount: number;
  /** Optimistic-lock token. Bumped server-side on every aggregate write. */
  readonly version: number;
}

/** Same label rules as the back-end MaterializedPath VO. */
export const LABEL_RE = /^[a-z0-9_]{1,64}$/;

export function isValidLabel(label: string): boolean {
  return LABEL_RE.test(label);
}

export function pathSegments(path: string): string[] {
  return path.split('.');
}
