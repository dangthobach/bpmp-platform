import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft, ArrowRight, BookmarkPlus, Braces, ChevronDown, ChevronRight, Download, Filter, GitBranch, ListFilter, Plus,
  Eye, Play, RefreshCw, Search, Trash2, X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useApi } from "../../api/ApiContext";
import type { CompletedProcessQueryInput, ProcessExpressionInput, ProcessField, ProcessQueryInput, QueryScope } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { Dialog } from "../../components/Dialog";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { StatusBadge } from "../../components/StatusBadge";
import { processQueryFromSearch } from "./deepLink";
import {
  allowedOperators, browserQueryLimits, compileFieldValue, compileQuery, dateTransforms, eventFields, fieldDefinition,
  newGroup, newPredicate, operatorLabel, processFields, removeNode, updateNode,
  numericTransforms, textTransforms, type DateTransform, type GroupNode, type NumericTransform, type QueryNode, type TextTransform,
} from "./queryDsl";

interface SortDraft {
  id: string;
  field: string;
  direction: "SORT_DIRECTION_ASCENDING" | "SORT_DIRECTION_DESCENDING";
  textTransform: TextTransform;
  numericTransform: NumericTransform;
  dateTransform: DateTransform;
  coalesceValue: string;
}

interface ExecutedQuery {
  request: ProcessQueryInput;
  signature: string;
}

export function ProcessQueryPage() {
  const api = useApi();
  const queryClient = useQueryClient();
  const { identity } = useAuth();
  const [initialQuery] = useState(() => processQueryFromSearch(window.location.search));
  const [root, setRoot] = useState<QueryNode>(initialQuery.root);
  const [sorts, setSorts] = useState<SortDraft[]>([
    { id: crypto.randomUUID(), field: "PROCESS_FIELD_COMPLETED_AT", direction: "SORT_DIRECTION_DESCENDING", textTransform: "TEXT_TRANSFORM_NONE", numericTransform: "NUMERIC_TRANSFORM_NONE", dateTransform: "DATE_TRANSFORM_NONE", coalesceValue: "" },
  ]);
  const [pageSize, setPageSize] = useState(50);
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [scope, setScope] = useState<QueryScope>(initialQuery.scope);
  const [selectedFields, setSelectedFields] = useState<ProcessField[]>([]);
  const [collapse, setCollapse] = useState<ProcessField | "">("");
  const [expandEventLimit, setExpandEventLimit] = useState(0);
  const [executed, setExecuted] = useState<ExecutedQuery | null>(null);
  const [pageTokens, setPageTokens] = useState<string[]>([""]);
  const [submitErrors, setSubmitErrors] = useState<Record<string, string>>({});
  const [savedQueryName, setSavedQueryName] = useState("");
  const [savedPageTokens, setSavedPageTokens] = useState<string[]>([""]);
  const [exportId, setExportId] = useState("");
  const [inspectedInstanceId, setInspectedInstanceId] = useState("");
  const [eventPageTokens, setEventPageTokens] = useState<string[]>([""]);
  const eventPageToken = eventPageTokens.at(-1) ?? "";
  const savedPageToken = savedPageTokens.at(-1) ?? "";
  const pageToken = pageTokens.at(-1) ?? "";
  const capabilities = useQuery({
    queryKey: ["process-query-capabilities", identity?.tenantId],
    queryFn: () => api.getProcessQueryCapabilities(),
    enabled: Boolean(identity),
    staleTime: 60_000,
  });
  const effectiveLimits = useMemo(() => capabilities.data ? {
    maxDepth: capabilities.data.max_filter_depth,
    maxPredicates: capabilities.data.max_predicates,
    maxRelations: capabilities.data.max_relation_joins,
    maxInValues: capabilities.data.max_in_values,
    maxStringBytes: capabilities.data.max_string_bytes,
    maxSortFields: capabilities.data.max_sort_fields,
  } : browserQueryLimits, [capabilities.data]);
  const draft = useMemo(() => compileQuery(root, effectiveLimits), [root, effectiveLimits]);

  useEffect(() => {
    if (!capabilities.data) return;
    setPageSize((value) => Math.min(value, capabilities.data.max_page_size));
    if (!capabilities.data.scopes.includes(scope)) setScope(capabilities.data.scopes[0] ?? "QUERY_SCOPE_COMPLETED");
  }, [capabilities.data, scope]);

  const query = useQuery({
    queryKey: ["completed-process-query", identity?.tenantId, executed?.signature, pageToken],
    queryFn: () => api.queryProcesses({
      ...executed!.request,
      ...(pageToken ? { page_token: pageToken } : {}),
    }),
    enabled: Boolean(identity && executed),
    placeholderData: keepPreviousData,
  });
  const savedQueries = useQuery({
    queryKey: ["saved-process-queries", identity?.tenantId, savedPageToken],
    queryFn: () => api.listSavedQueries(savedPageToken),
    enabled: Boolean(identity),
    placeholderData: keepPreviousData,
  });
  const saveSavedQuery = useMutation({
    mutationFn: (input: { id: string; name: string; query: ProcessQueryInput }) => api.saveQuery(input),
    onSuccess: async () => { setSavedQueryName(""); await queryClient.invalidateQueries({ queryKey: ["saved-process-queries", identity?.tenantId] }); },
  });
  const deleteSavedQuery = useMutation({
    mutationFn: (input: { id: string; version: number }) => api.deleteSavedQuery(input.id, input.version),
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: ["saved-process-queries", identity?.tenantId] }); },
  });
  const createExport = useMutation({
    mutationFn: () => {
      if (!executed) throw new Error("Run a process query before exporting.");
      return api.createQueryExport(executed.request, 10_000);
    },
    onSuccess: (result) => setExportId(result.id),
  });
  const exportStatus = useQuery({
    queryKey: ["process-query-export", identity?.tenantId, exportId],
    queryFn: () => api.getQueryExport(exportId),
    enabled: Boolean(identity && exportId),
    refetchInterval: (current) => current.state.data?.status === "QUERY_EXPORT_STATUS_PENDING" ? 2_000 : false,
  });
  const cancelExport = useMutation({
    mutationFn: () => api.cancelQueryExport(exportId),
    onSuccess: (result) => { if (result.status === "QUERY_EXPORT_STATUS_CANCELLED") setExportId(""); },
  });
  const eventHistory = useQuery({
    queryKey: ["process-event-history", identity?.tenantId, inspectedInstanceId, eventPageToken],
    queryFn: () => api.queryEvents({
      process_filter: { predicate: { field: "PROCESS_FIELD_INSTANCE_ID", operator: "COMPARISON_OPERATOR_EQUAL", values: [{ string_value: inspectedInstanceId }] } },
      sort: [{ field: "EVENT_FIELD_SEQUENCE", direction: "SORT_DIRECTION_ASCENDING" }],
      page_size: capabilities.data?.default_page_size ?? 50,
      ...(eventPageToken ? { page_token: eventPageToken } : {}),
    }),
    enabled: Boolean(identity && inspectedInstanceId),
    placeholderData: keepPreviousData,
  });

  function runQuery() {
    const nextErrors = { ...draft.errors };
    const seen = new Set<string>();
    const compiledSorts: NonNullable<CompletedProcessQueryInput["sort"]> = [];
    for (const sort of sorts) {
      if (seen.has(sort.field)) nextErrors[`sort-${sort.id}`] = "Each sort field can appear only once.";
      seen.add(sort.field);
      const definition = fieldDefinition("process", sort.field)!;
      if (definition.kind !== "string" && sort.textTransform !== "TEXT_TRANSFORM_NONE") {
        nextErrors[`sort-${sort.id}`] = "Text transforms require a string sort field.";
      }
      if (definition.kind !== "integer" && sort.numericTransform !== "NUMERIC_TRANSFORM_NONE") {
        nextErrors[`sort-${sort.id}`] = "Numeric transforms require a numeric sort field.";
      }
      if (definition.kind !== "timestamp" && sort.dateTransform !== "DATE_TRANSFORM_NONE") {
        nextErrors[`sort-${sort.id}`] = "Date transforms require a date sort field.";
      }
      const coalesce = sort.coalesceValue.trim()
        ? compileFieldValue(definition, sort.coalesceValue.trim())
        : {};
      if (coalesce.error) nextErrors[`sort-${sort.id}`] = `Null fallback: ${coalesce.error}`;
      compiledSorts.push({
        field: sort.field as ProcessField,
        direction: sort.direction,
        text_transform: sort.textTransform,
        numeric_transform: sort.numericTransform,
        date_transform: sort.dateTransform,
        ...(coalesce.value ? { coalesce_value: coalesce.value } : {}),
      });
    }
    const normalizedSortCount = sorts.length + (seen.has("PROCESS_FIELD_INSTANCE_ID") ? 0 : 1);
    if (normalizedSortCount > effectiveLimits.maxSortFields) {
      nextErrors.sort = `At most ${effectiveLimits.maxSortFields} executed sort fields are allowed, including the automatic Instance ID tie-breaker.`;
    }
    const maxPageSize = capabilities.data?.max_page_size ?? 100;
    if (!Number.isInteger(pageSize) || pageSize < 1 || pageSize > maxPageSize) {
      nextErrors.pageSize = `Page size must be between 1 and ${maxPageSize}.`;
    }
    if (collapse && compiledSorts[0]?.field !== collapse) nextErrors.collapse = "Collapse field must be the first sort field.";
    if (!Number.isInteger(expandEventLimit) || expandEventLimit < 0 || expandEventLimit > effectiveLimits.maxInValues) nextErrors.expandEvents = `Event expansion must be between 0 and ${effectiveLimits.maxInValues}.`;
    setSubmitErrors(nextErrors);
    if (!draft.expression || Object.keys(nextErrors).length > 0) return;
    const request: ProcessQueryInput = {
      scope,
      filter: draft.expression as ProcessExpressionInput,
      sort: compiledSorts,
      include_deleted: includeDeleted,
      page_size: pageSize,
      ...(selectedFields.length ? { select: selectedFields } : {}),
      ...(collapse ? { collapse } : {}),
      ...(expandEventLimit ? { expand_events: { limit: expandEventLimit } } : {}),
    };
    setPageTokens([""]);
    setExportId("");
    createExport.reset();
    setExecuted({ request, signature: JSON.stringify(request) });
  }

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Process explorer</h1>
          <p>Build tenant-scoped queries with nested Boolean logic and correlated event joins.</p>
        </div>
        <div className="query-budget" aria-label="Query complexity">
          <span>Depth {draft.summary.depth}/{effectiveLimits.maxDepth}</span>
          <span>Rules {draft.summary.predicates}/{effectiveLimits.maxPredicates}</span>
          <span>Joins {draft.summary.relations}/{effectiveLimits.maxRelations}</span>
        </div>
      </header>

      <section className="query-workbench" aria-label="Visual process query builder">
        <header>
          <div>
            <Filter size={17} aria-hidden="true" />
            <strong>Filter tree</strong>
            <span>All values are parameterized; event joins use bounded EXISTS predicates.</span>
          </div>
          <Button icon={Search} variant="primary" onClick={runQuery}>Run query</Button>
        </header>
        <div className="query-tree">
          <QueryNodeEditor
            node={root}
            errors={{ ...draft.errors, ...submitErrors }}
            onChange={(id, change) => setRoot((value) => updateNode(value, id, change))}
            onRemove={(id) => setRoot((value) => removeNode(value, id))}
            removable={false}
          />
        </div>
        <div className="query-options">
          <div className="sort-builder">
            <div className="sort-builder__header">
              <strong>Sort order</strong>
              <Button
                icon={Plus}
                variant="ghost"
                onClick={() => setSorts((value) => [...value, {
                  id: crypto.randomUUID(), field: "PROCESS_FIELD_UPDATED_AT", direction: "SORT_DIRECTION_DESCENDING", textTransform: "TEXT_TRANSFORM_NONE", numericTransform: "NUMERIC_TRANSFORM_NONE", dateTransform: "DATE_TRANSFORM_NONE", coalesceValue: "",
                }])}
                disabled={sorts.length >= effectiveLimits.maxSortFields}
              >Add sort</Button>
            </div>
            {sorts.map((sort) => (
              <div className="sort-row" key={sort.id}>
                <select
                  aria-label="Sort field"
                  value={sort.field}
                  onChange={(event) => setSorts((items) => items.map((item) => item.id === sort.id ? {
                    ...item,
                    field: event.target.value,
                    textTransform: "TEXT_TRANSFORM_NONE",
                    numericTransform: "NUMERIC_TRANSFORM_NONE",
                    dateTransform: "DATE_TRANSFORM_NONE",
                    coalesceValue: "",
                  } : item))}
                >
                  {processFields.filter((field) => field.sortable).map((field) => <option key={field.value} value={field.value}>{field.label}</option>)}
                </select>
                <select
                  aria-label="Sort direction"
                  value={sort.direction}
                  onChange={(event) => setSorts((items) => items.map((item) => item.id === sort.id ? { ...item, direction: event.target.value as SortDraft["direction"] } : item))}
                >
                  <option value="SORT_DIRECTION_ASCENDING">Ascending</option>
                  <option value="SORT_DIRECTION_DESCENDING">Descending</option>
                </select>
                <SortTransformSelect sort={sort} onChange={(change) => setSorts((items) => items.map((item) => item.id === sort.id ? { ...item, ...change } : item))} />
                <input
                  aria-label="Sort null fallback"
                  placeholder="Null fallback (optional)"
                  type={fieldDefinition("process", sort.field)?.kind === "timestamp" ? "datetime-local" : "text"}
                  value={sort.coalesceValue}
                  onChange={(event) => setSorts((items) => items.map((item) => item.id === sort.id ? { ...item, coalesceValue: event.target.value } : item))}
                />
                <IconButton icon={Trash2} label="Remove sort" disabled={sorts.length === 1} onClick={() => setSorts((items) => items.filter((item) => item.id !== sort.id))} />
                {submitErrors[`sort-${sort.id}`] ? <span className="query-node__error">{submitErrors[`sort-${sort.id}`]}</span> : null}
              </div>
            ))}
            {submitErrors.sort ? <span className="query-node__error">{submitErrors.sort}</span> : null}
          </div>
          <div className="query-run-options">
            <label>
              Query scope
              <select aria-label="Query scope" value={scope} onChange={(event) => setScope(event.target.value as QueryScope)}>
                {(capabilities.data?.scopes ?? ["QUERY_SCOPE_COMPLETED"]).map((value) => <option key={value} value={value}>{value.replace("QUERY_SCOPE_", "").toLowerCase()}</option>)}
              </select>
            </label>
            <label>
              Page size
              <input type="number" min={1} max={capabilities.data?.max_page_size ?? 100} value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))} />
            </label>
            <label>
              Collapse by
              <select aria-label="Collapse field" value={collapse} onChange={(event) => setCollapse(event.target.value as ProcessField | "")}>
                <option value="">No collapse</option>
                {processFields.filter((field) => field.sortable).map((field) => <option key={field.value} value={field.value}>{field.label}</option>)}
              </select>
            </label>
            <label>
              Expand latest events
              <input aria-label="Expanded event limit" type="number" min={0} max={effectiveLimits.maxInValues} value={expandEventLimit} onChange={(event) => setExpandEventLimit(Number(event.target.value))} />
            </label>
            <label>
              Returned fields
              <select multiple aria-label="Returned fields" value={selectedFields} onChange={(event) => setSelectedFields(Array.from(event.currentTarget.selectedOptions, (option) => option.value as ProcessField))}>
                {(capabilities.data?.fields.filter((field) => field.selectable).map((field) => ({ value: field.field, label: field.field.replace("PROCESS_FIELD_", "").toLowerCase() })) ?? processFields.map((field) => ({ value: field.value as ProcessField, label: field.label }))).map((field) => <option key={field.value} value={field.value}>{field.label}</option>)}
              </select>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={includeDeleted} onChange={(event) => setIncludeDeleted(event.target.checked)} />
              Include deleted projections
            </label>
            {submitErrors.pageSize ? <span className="query-node__error">{submitErrors.pageSize}</span> : null}
            {submitErrors.collapse ? <span className="query-node__error">{submitErrors.collapse}</span> : null}
            {submitErrors.expandEvents ? <span className="query-node__error">{submitErrors.expandEvents}</span> : null}
            <span className="query-policy-note">Tenant policy and field authorization are revalidated by the Projection Service.</span>
          </div>
        </div>
      </section>

      <section className="table-surface saved-query-manager">
        <header className="portal-section-heading"><div><h2>Saved queries</h2><p>Saved queries belong to your authenticated actor. Running one submits the stored snapshot to Projection Service.</p></div></header>
        <form className="header-actions" onSubmit={(event) => { event.preventDefault(); if (!executed || !savedQueryName.trim()) return; saveSavedQuery.mutate({ id: crypto.randomUUID(), name: savedQueryName.trim(), query: executed.request }); }}>
          <label className="saved-query-manager__name">Name current query<input value={savedQueryName} onChange={(event) => setSavedQueryName(event.target.value)} maxLength={256} placeholder="e.g. Recently completed approvals" disabled={!executed || saveSavedQuery.isPending} /></label>
          <Button icon={BookmarkPlus} type="submit" disabled={!executed || !savedQueryName.trim() || saveSavedQuery.isPending}>{saveSavedQuery.isPending ? "Saving…" : "Save query"}</Button>
        </form>
        {saveSavedQuery.isError ? <p role="alert" className="query-node__error">{saveSavedQuery.error.message}</p> : null}
        {savedQueries.isPending ? <LoadingRows /> : null}
        {savedQueries.isError ? <ErrorState message={savedQueries.error.message} retry={() => void savedQueries.refetch()} /> : null}
        {savedQueries.isSuccess && savedQueries.data.saved_queries.length === 0 ? <EmptyState title="No saved queries" detail="Run a query, give it a name, and save it for reuse." /> : null}
        {savedQueries.data?.saved_queries.length ? <div className="table-scroll"><table><thead><tr><th>Name</th><th>Scope</th><th>Updated</th><th>Version</th><th>Actions</th></tr></thead><tbody>{savedQueries.data.saved_queries.map((saved) => <tr key={saved.id}><td>{saved.name}</td><td>{saved.query.scope?.replace("QUERY_SCOPE_", "") ?? "—"}</td><td>{new Date(saved.updated_at_epoch_ms).toLocaleString()}</td><td>{saved.version}</td><td className="saved-query-manager__actions"><Button icon={Play} onClick={() => { setPageTokens([""]); setExportId(""); createExport.reset(); setExecuted({ request: saved.query, signature: JSON.stringify(saved.query) }); }}>Run</Button><Button icon={Trash2} disabled={deleteSavedQuery.isPending} onClick={() => deleteSavedQuery.mutate({ id: saved.id, version: saved.version })}>Delete</Button></td></tr>)}</tbody></table></div> : null}
        {deleteSavedQuery.isError ? <p role="alert" className="query-node__error">{deleteSavedQuery.error.message}</p> : null}
        {savedQueries.data ? <footer className="pagination"><span>Saved query page {savedPageTokens.length}</span><div><IconButton icon={ArrowLeft} label="Previous saved query page" disabled={savedPageTokens.length === 1 || savedQueries.isFetching} onClick={() => setSavedPageTokens((tokens) => tokens.slice(0, -1))} /><IconButton icon={ArrowRight} label="Next saved query page" disabled={!savedQueries.data.next_page_token || savedQueries.isFetching} onClick={() => { const next = savedQueries.data.next_page_token; if (next) setSavedPageTokens((tokens) => [...tokens, next]); }} /></div></footer> : null}
      </section>

      <section className="table-surface query-results" aria-live="polite">
        <header>
          <div>
            <ListFilter size={17} aria-hidden="true" />
            <strong>Results</strong>
            <span>{query.data?.processes.length ?? 0} rows on this page</span>
          </div>
          <div className="header-actions"><Button icon={Download} onClick={() => createExport.mutate()} disabled={!executed || createExport.isPending || Boolean(exportId)}>{createExport.isPending ? "Submitting export…" : "Export query CSV"}</Button><IconButton icon={RefreshCw} label="Refresh query" disabled={!executed || query.isFetching} onClick={() => void query.refetch()} /></div>
        </header>
        {createExport.isError ? <p role="alert" className="query-node__error">{createExport.error.message}</p> : null}
        {exportId && exportStatus.isPending ? <LoadingRows /> : null}
        {exportStatus.isError ? <ErrorState message={exportStatus.error.message} retry={() => void exportStatus.refetch()} /> : null}
        {exportStatus.data?.status === "QUERY_EXPORT_STATUS_PENDING" ? <div className="inline-notice" role="status">The authorization-checked export is processing.<Button icon={X} onClick={() => cancelExport.mutate()} disabled={cancelExport.isPending}>{cancelExport.isPending ? "Cancelling…" : "Cancel export"}</Button></div> : null}
        {exportStatus.data?.status === "QUERY_EXPORT_STATUS_COMPLETED" ? <div className="inline-notice" role="status">Export ready · {exportStatus.data.row_count?.toLocaleString() ?? 0} rows · expires {new Date(exportStatus.data.expires_at_epoch_ms).toLocaleString()} <Button icon={Download} onClick={() => downloadQueryCsv(exportStatus.data!.csv, executed?.request.scope)}>Download CSV</Button><Button onClick={() => setExportId("")}>Dismiss</Button></div> : null}
        {exportStatus.data?.status === "QUERY_EXPORT_STATUS_FAILED" ? <ErrorState message={`Export failed (${exportStatus.data.error_code || "unknown error"})`} retry={() => { setExportId(""); createExport.mutate(); }} /> : null}
        {cancelExport.isError ? <ErrorState message={cancelExport.error.message} retry={() => cancelExport.mutate()} /> : null}
        {query.isPending && executed ? <LoadingRows /> : null}
        {query.isError ? <ErrorState message={query.error instanceof Error ? query.error.message : "Query failed"} retry={() => void query.refetch()} /> : null}
        {!executed ? <EmptyState title="Query not run" detail="Complete the filter tree, then run the query." /> : null}
        {query.isSuccess && query.data.processes.length === 0 ? <EmptyState title="No completed processes" detail="No authorized rows match this filter." /> : null}
        {query.data && query.data.processes.length > 0 ? (
          <div className="table-scroll">
            <table className="process-results-table">
              <thead><tr><th>Instance</th><th>Workflow</th><th>Status</th><th>Completed</th><th>Duration</th><th>Events</th><th>Initiator</th><th>History</th></tr></thead>
              <tbody>
                {query.data.processes.map((process) => (
                  <tr key={process.instance.instance_id}>
                    <td><strong>{process.instance.instance_id}</strong><span className="cell-secondary">seq {display(process.instance.last_event_sequence)}</span></td>
                    <td>{display(process.instance.workflow_type)}<span className="cell-secondary">v{display(process.instance.workflow_version)}</span></td>
                    <td>{process.instance.status ? <StatusBadge status={process.instance.status} /> : "Masked"}</td>
                    <td>{formatTimestamp(process.instance.completed_at_epoch_ms)}</td>
                    <td>{formatDuration(process.duration_ms)}</td>
                    <td>{display(process.event_count)}</td>
                    <td>{display(process.instance.initiator_actor_id)}</td>
                    <td><Button icon={Eye} onClick={() => { setEventPageTokens([""]); setInspectedInstanceId(process.instance.instance_id); }}>View events</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {executed ? (
          <footer className="pagination">
            <span>Page {pageTokens.length}</span>
            <div>
              <IconButton icon={ArrowLeft} label="Previous page" disabled={pageTokens.length === 1 || query.isFetching} onClick={() => setPageTokens((tokens) => tokens.slice(0, -1))} />
              <IconButton icon={ArrowRight} label="Next page" disabled={!query.data?.next_page_token || query.isFetching} onClick={() => {
                const next = query.data?.next_page_token;
                if (next) setPageTokens((tokens) => [...tokens, next]);
              }} />
            </div>
          </footer>
        ) : null}
      </section>
      <Dialog
        open={Boolean(inspectedInstanceId)}
        title="Process event history"
        description={`Authorized events for instance ${inspectedInstanceId}`}
        onClose={() => setInspectedInstanceId("")}
        size="wide"
      >
        {eventHistory.isPending ? <LoadingRows /> : null}
        {eventHistory.isError ? <ErrorState message={eventHistory.error.message} retry={() => void eventHistory.refetch()} /> : null}
        {eventHistory.isSuccess && eventHistory.data.events.length === 0 ? <EmptyState title="No events available" detail="No authorized events were returned for this process instance." /> : null}
        {eventHistory.data?.events.length ? <div className="table-scroll"><table><thead><tr><th>Sequence</th><th>Occurred</th><th>Event</th><th>Node</th><th>Actor</th><th>Config</th><th>Policy</th></tr></thead><tbody>{eventHistory.data.events.map((event, index) => <tr key={`${event.instance_id ?? inspectedInstanceId}:${event.sequence ?? index}`}><td>{display(event.sequence)}</td><td>{formatTimestamp(event.occurred_at_epoch_ms)}</td><td>{display(event.event_type)}</td><td>{display(event.node_id)}</td><td>{display(event.actor_id)}</td><td>{display(event.config_version)}</td><td>{display(event.policy_version)}</td></tr>)}</tbody></table></div> : null}
        {eventHistory.data ? <footer className="pagination"><span>Event page {eventPageTokens.length}</span><div><IconButton icon={ArrowLeft} label="Previous event page" disabled={eventPageTokens.length === 1 || eventHistory.isFetching} onClick={() => setEventPageTokens((tokens) => tokens.slice(0, -1))} /><IconButton icon={ArrowRight} label="Next event page" disabled={!eventHistory.data.next_page_token || eventHistory.isFetching} onClick={() => { const next = eventHistory.data.next_page_token; if (next) setEventPageTokens((tokens) => [...tokens, next]); }} /></div></footer> : null}
        <p className="query-policy-note">Projection Service reapplies tenant row and field authorization to this event query; masked fields are shown as “Masked”.</p>
      </Dialog>
    </>
  );
}

function QueryNodeEditor({ node, errors, onChange, onRemove, removable }: {
  node: QueryNode;
  errors: Record<string, string>;
  onChange: (id: string, change: (node: QueryNode) => QueryNode) => void;
  onRemove: (id: string) => void;
  removable: boolean;
}) {
  const [collapsed, setCollapsed] = useState(false);
  if (node.type === "predicate") {
    const fields = node.scope === "process" ? processFields : eventFields;
    const definition = fieldDefinition(node.scope, node.field) ?? fields[0]!;
    const operators = allowedOperators(definition.kind);
    const multiple = node.operator === "COMPARISON_OPERATOR_IN" || node.operator === "COMPARISON_OPERATOR_NOT_IN";
    const range = node.operator === "COMPARISON_OPERATOR_BETWEEN" || node.operator === "COMPARISON_OPERATOR_NOT_BETWEEN";
    const noValue = node.operator === "COMPARISON_OPERATOR_IS_NULL" || node.operator === "COMPARISON_OPERATOR_IS_NOT_NULL";
    return (
      <div className={`query-node query-node--predicate ${errors[node.id] ? "query-node--invalid" : ""}`}>
        <span className="query-node__kind">{node.scope === "event" ? "Event" : "Process"}</span>
        <select aria-label={`${node.scope} field`} value={node.field} onChange={(event) => onChange(node.id, (current) => {
          if (current.type !== "predicate") return current;
          const nextField = fieldDefinition(current.scope, event.target.value) ?? fields[0]!;
          const nextOperators = allowedOperators(nextField.kind);
          return {
            ...current,
            field: nextField.value,
            operator: nextOperators.includes(current.operator) ? current.operator : nextOperators[0]!,
            rawValue: "",
            rangeEnd: "",
            coalesceValue: "",
            textTransform: nextField.kind === "string" ? current.textTransform : "TEXT_TRANSFORM_NONE",
            numericTransform: nextField.kind === "integer" ? current.numericTransform : "NUMERIC_TRANSFORM_NONE",
            dateTransform: nextField.kind === "timestamp" ? current.dateTransform : "DATE_TRANSFORM_NONE",
          };
        })}>
          {fields.map((field) => <option value={field.value} key={field.value}>{field.label}</option>)}
        </select>
        <select aria-label="Comparison operator" value={node.operator} onChange={(event) => onChange(node.id, (current) => current.type === "predicate" ? { ...current, operator: event.target.value as typeof current.operator } : current)}>
          {operators.map((operator) => <option value={operator} key={operator}>{operatorLabel(operator)}</option>)}
        </select>
        <PredicateTransformSelect node={node} kind={definition.kind} onChange={(change) => onChange(node.id, (current) => current.type === "predicate" ? { ...current, ...change } : current)} />
        <div className={`query-node__value ${range ? "query-node__value--range" : ""}`}>
          {!noValue ? <input
            type={definition.kind === "timestamp" && !multiple ? "datetime-local" : "text"}
            inputMode={definition.kind === "integer" ? "numeric" : undefined}
            aria-label="Predicate value"
            placeholder={range ? "From" : multiple ? "value 1, value 2" : definition.kind === "integer" ? "0" : "Value"}
            value={node.rawValue}
            onChange={(event) => onChange(node.id, (current) => current.type === "predicate" ? { ...current, rawValue: event.target.value } : current)}
          /> : <span className="query-node__no-value">No value required</span>}
          {range ? <input
            type={definition.kind === "timestamp" ? "datetime-local" : "text"}
            inputMode={definition.kind === "integer" ? "numeric" : undefined}
            aria-label="Range end"
            placeholder="To"
            value={node.rangeEnd}
            onChange={(event) => onChange(node.id, (current) => current.type === "predicate" ? { ...current, rangeEnd: event.target.value } : current)}
          /> : null}
        </div>
        <input
          type={definition.kind === "timestamp" ? "datetime-local" : "text"}
          inputMode={definition.kind === "integer" ? "numeric" : undefined}
          aria-label="Null fallback"
          placeholder="Null fallback (optional)"
          value={node.coalesceValue}
          onChange={(event) => onChange(node.id, (current) => current.type === "predicate" ? { ...current, coalesceValue: event.target.value } : current)}
        />
        {removable ? <IconButton icon={Trash2} label="Remove predicate" onClick={() => onRemove(node.id)} /> : null}
        {errors[node.id] ? <span className="query-node__error">{errors[node.id]}</span> : null}
      </div>
    );
  }

  if (node.type === "group") {
    return (
      <fieldset className={`query-node query-node--group ${errors[node.id] ? "query-node--invalid" : ""}`}>
        <legend>
          <IconButton icon={collapsed ? ChevronRight : ChevronDown} label={collapsed ? "Expand group" : "Collapse group"} onClick={() => setCollapsed((value) => !value)} />
          Match
          <select aria-label="Logical operator" value={node.operator} onChange={(event) => onChange(node.id, (current) => current.type === "group" ? { ...current, operator: event.target.value as GroupNode["operator"] } : current)}>
            <option value="LOGICAL_OPERATOR_AND">all (AND)</option>
            <option value="LOGICAL_OPERATOR_OR">any (OR)</option>
          </select>
          {removable ? <IconButton icon={Trash2} label="Remove group" onClick={() => onRemove(node.id)} /> : null}
        </legend>
        <div className="query-node__children" hidden={collapsed}>
          {node.children.map((child) => <QueryNodeEditor key={child.id} node={child} errors={errors} onChange={onChange} onRemove={onRemove} removable />)}
        </div>
        <div className="query-node__actions" hidden={collapsed}>
          <Button icon={Plus} variant="ghost" onClick={() => appendChild(node, newPredicate(node.scope), onChange)}>Rule</Button>
          <Button icon={Braces} variant="ghost" onClick={() => appendChild(node, newGroup(node.scope), onChange)}>Group</Button>
          <Button icon={Filter} variant="ghost" onClick={() => appendChild(node, { id: crypto.randomUUID(), type: "negated", scope: node.scope, child: newPredicate(node.scope) }, onChange)}>NOT</Button>
          {node.scope === "process" ? <Button icon={GitBranch} variant="ghost" onClick={() => appendChild(node, {
            id: crypto.randomUUID(), type: "relation", scope: "process", quantifier: "RELATION_QUANTIFIER_EXISTS", child: newGroup("event"),
          }, onChange)}>Event join</Button> : null}
        </div>
        {errors[node.id] ? <span className="query-node__error">{errors[node.id]}</span> : null}
      </fieldset>
    );
  }

  if (node.type === "negated") {
    return (
      <fieldset className={`query-node query-node--wrapper ${errors[node.id] ? "query-node--invalid" : ""}`}>
        <legend><IconButton icon={collapsed ? ChevronRight : ChevronDown} label={collapsed ? "Expand NOT expression" : "Collapse NOT expression"} onClick={() => setCollapsed((value) => !value)} /> NOT {removable ? <IconButton icon={Trash2} label="Remove NOT expression" onClick={() => onRemove(node.id)} /> : null}</legend>
        <div hidden={collapsed}><QueryNodeEditor node={node.child} errors={errors} onChange={onChange} onRemove={onRemove} removable={false} /></div>
        {errors[node.id] ? <span className="query-node__error">{errors[node.id]}</span> : null}
      </fieldset>
    );
  }

  return (
    <fieldset className={`query-node query-node--relation ${errors[node.id] ? "query-node--invalid" : ""}`}>
      <legend>
        <IconButton icon={collapsed ? ChevronRight : ChevronDown} label={collapsed ? "Expand event join" : "Collapse event join"} onClick={() => setCollapsed((value) => !value)} />
        Events
        <select aria-label="Event relation quantifier" value={node.quantifier} onChange={(event) => onChange(node.id, (current) => current.type === "relation" ? { ...current, quantifier: event.target.value as typeof current.quantifier } : current)}>
          <option value="RELATION_QUANTIFIER_EXISTS">has a matching event</option>
          <option value="RELATION_QUANTIFIER_NOT_EXISTS">has no matching event</option>
        </select>
        {removable ? <IconButton icon={Trash2} label="Remove event join" onClick={() => onRemove(node.id)} /> : null}
      </legend>
      <div hidden={collapsed}><QueryNodeEditor node={node.child} errors={errors} onChange={onChange} onRemove={onRemove} removable={false} /></div>
      {errors[node.id] ? <span className="query-node__error">{errors[node.id]}</span> : null}
    </fieldset>
  );
}

function PredicateTransformSelect({ node, kind, onChange }: {
  node: Extract<QueryNode, { type: "predicate" }>;
  kind: "string" | "integer" | "timestamp";
  onChange: (change: Partial<Extract<QueryNode, { type: "predicate" }>>) => void;
}) {
  if (kind === "integer") {
    return <select aria-label="Numeric transform" value={node.numericTransform} onChange={(event) => onChange({ numericTransform: event.target.value as NumericTransform })}>
      {numericTransforms.map((transform) => <option key={transform.value} value={transform.value}>{transform.label}</option>)}
    </select>;
  }
  if (kind === "timestamp") {
    return <select aria-label="Date transform" value={node.dateTransform} onChange={(event) => onChange({ dateTransform: event.target.value as DateTransform })}>
      {dateTransforms.map((transform) => <option key={transform.value} value={transform.value}>{transform.label}</option>)}
    </select>;
  }
  return <select aria-label="Text transform" value={node.textTransform} onChange={(event) => onChange({ textTransform: event.target.value as TextTransform })}>
    {textTransforms.map((transform) => <option key={transform.value} value={transform.value}>{transform.label}</option>)}
  </select>;
}

function SortTransformSelect({ sort, onChange }: {
  sort: SortDraft;
  onChange: (change: Partial<SortDraft>) => void;
}) {
  const kind = fieldDefinition("process", sort.field)?.kind;
  if (kind === "integer") {
    return <select aria-label="Sort numeric transform" value={sort.numericTransform} onChange={(event) => onChange({ numericTransform: event.target.value as NumericTransform })}>
      {numericTransforms.map((transform) => <option key={transform.value} value={transform.value}>{transform.label}</option>)}
    </select>;
  }
  if (kind === "timestamp") {
    return <select aria-label="Sort date transform" value={sort.dateTransform} onChange={(event) => onChange({ dateTransform: event.target.value as DateTransform })}>
      {dateTransforms.map((transform) => <option key={transform.value} value={transform.value}>{transform.label}</option>)}
    </select>;
  }
  return <select aria-label="Sort text transform" value={sort.textTransform} onChange={(event) => onChange({ textTransform: event.target.value as TextTransform })}>
    {textTransforms.map((transform) => <option key={transform.value} value={transform.value}>{transform.label}</option>)}
  </select>;
}

function appendChild(group: GroupNode, child: QueryNode, onChange: (id: string, change: (node: QueryNode) => QueryNode) => void) {
  onChange(group.id, (current) => current.type === "group" ? { ...current, children: [...current.children, child] } : current);
}

function display(value: string | number | undefined): string {
  return value === undefined || value === "" ? "Masked" : String(value);
}

function downloadQueryCsv(csv: string | undefined, scope: QueryScope | undefined): void {
  if (csv === undefined) return;
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `bpmp-process-query-${(scope ?? "export").replace("QUERY_SCOPE_", "").toLowerCase()}.csv`;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

function formatTimestamp(value: number | undefined): string {
  if (!value) return "Masked";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function formatDuration(value: number | undefined): string {
  if (value === undefined) return "Masked";
  if (value < 1_000) return `${value} ms`;
  if (value < 60_000) return `${(value / 1_000).toFixed(1)} s`;
  return `${(value / 60_000).toFixed(1)} min`;
}
