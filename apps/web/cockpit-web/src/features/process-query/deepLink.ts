import type { QueryScope } from "../../api/types";
import { newGroup, newPredicate, type QueryNode } from "./queryDsl";

export function processQueryFromSearch(search: string): { root: QueryNode; scope: QueryScope } {
  const params = new URLSearchParams(search);
  const workflowType = params.get("workflow_type")?.trim();
  let root: QueryNode = newGroup("process");
  if (workflowType) {
    const predicate = newPredicate("process");
    predicate.field = "PROCESS_FIELD_WORKFLOW_TYPE";
    predicate.rawValue = workflowType;
    root = predicate;
  }
  const requestedScope = params.get("scope");
  const scope: QueryScope = requestedScope === "QUERY_SCOPE_ACTIVE" || requestedScope === "QUERY_SCOPE_COMPLETED"
    ? requestedScope
    : "QUERY_SCOPE_COMPLETED";
  return { root, scope };
}
