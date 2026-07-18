// Centralised query-key factory. Prevents typo-induced cache desync and lets
// you invalidate at the granularity that the back-end's optimistic-lock
// version makes meaningful.
export const orgKeys = {
  all: ["organizations"] as const,
  list: (offset: number, limit: number) =>
    [...orgKeys.all, "list", { offset, limit }] as const,
  detail: (id: string) => [...orgKeys.all, "detail", id] as const,
};
