import { keepPreviousData, useQuery } from "@tanstack/react-query";
import {
  ArrowLeft, ArrowRight, Braces, ChevronDown, ChevronRight, Filter, GitBranch, ListFilter, Plus,
  RefreshCw, Search, Trash2,
} from "lucide-react";
import { useMemo, useState } from "react";
import { useApi } from "../../api/ApiContext";
import type { CompletedProcessQueryInput } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { StatusBadge } from "../../components/StatusBadge";
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
  request: CompletedProcessQueryInput;
  signature: string;
}

export function ProcessQueryPage() {
  const api = useApi();
  const { identity } = useAuth();
  const [root, setRoot] = useState<QueryNode>(() => newGroup("process"));
  const [sorts, setSorts] = useState<SortDraft[]>([
    { id: crypto.randomUUID(), field: "PROCESS_FIELD_COMPLETED_AT", direction: "SORT_DIRECTION_DESCENDING", textTransform: "TEXT_TRANSFORM_NONE", numericTransform: "NUMERIC_TRANSFORM_NONE", dateTransform: "DATE_TRANSFORM_NONE", coalesceValue: "" },
  ]);
  const [pageSize, setPageSize] = useState(50);
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [executed, setExecuted] = useState<ExecutedQuery | null>(null);
  const [pageTokens, setPageTokens] = useState<string[]>([""]);
  const [submitErrors, setSubmitErrors] = useState<Record<string, string>>({});
  const pageToken = pageTokens.at(-1) ?? "";
  const draft = useMemo(() => compileQuery(root), [root]);

  const query = useQuery({
    queryKey: ["completed-process-query", identity?.tenantId, executed?.signature, pageToken],
    queryFn: () => api.queryCompletedProcesses({
      ...executed!.request,
      ...(pageToken ? { page_token: pageToken } : {}),
    }),
    enabled: Boolean(identity && executed),
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
        field: sort.field,
        direction: sort.direction,
        text_transform: sort.textTransform,
        numeric_transform: sort.numericTransform,
        date_transform: sort.dateTransform,
        ...(coalesce.value ? { coalesce_value: coalesce.value } : {}),
      });
    }
    const normalizedSortCount = sorts.length + (seen.has("PROCESS_FIELD_INSTANCE_ID") ? 0 : 1);
    if (normalizedSortCount > browserQueryLimits.maxSortFields) {
      nextErrors.sort = `At most ${browserQueryLimits.maxSortFields} executed sort fields are allowed, including the automatic Instance ID tie-breaker.`;
    }
    if (!Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) {
      nextErrors.pageSize = "Page size must be between 1 and 100.";
    }
    setSubmitErrors(nextErrors);
    if (!draft.expression || Object.keys(nextErrors).length > 0) return;
    const request: CompletedProcessQueryInput = {
      filter: draft.expression,
      sort: compiledSorts,
      include_deleted: includeDeleted,
      page_size: pageSize,
    };
    setPageTokens([""]);
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
          <span>Depth {draft.summary.depth}/{browserQueryLimits.maxDepth}</span>
          <span>Rules {draft.summary.predicates}/{browserQueryLimits.maxPredicates}</span>
          <span>Joins {draft.summary.relations}/{browserQueryLimits.maxRelations}</span>
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
                disabled={sorts.length >= browserQueryLimits.maxSortFields}
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
              Page size
              <input type="number" min={1} max={100} value={pageSize} onChange={(event) => setPageSize(Number(event.target.value))} />
            </label>
            <label className="checkbox-label">
              <input type="checkbox" checked={includeDeleted} onChange={(event) => setIncludeDeleted(event.target.checked)} />
              Include deleted projections
            </label>
            {submitErrors.pageSize ? <span className="query-node__error">{submitErrors.pageSize}</span> : null}
            <span className="query-policy-note">Tenant policy and field authorization are revalidated by the Projection Service.</span>
          </div>
        </div>
      </section>

      <section className="table-surface query-results" aria-live="polite">
        <header>
          <div>
            <ListFilter size={17} aria-hidden="true" />
            <strong>Results</strong>
            <span>{query.data?.processes.length ?? 0} rows on this page</span>
          </div>
          <IconButton icon={RefreshCw} label="Refresh query" disabled={!executed || query.isFetching} onClick={() => void query.refetch()} />
        </header>
        {query.isPending && executed ? <LoadingRows /> : null}
        {query.isError ? <ErrorState message={query.error instanceof Error ? query.error.message : "Query failed"} retry={() => void query.refetch()} /> : null}
        {!executed ? <EmptyState title="Query not run" detail="Complete the filter tree, then run the query." /> : null}
        {query.isSuccess && query.data.processes.length === 0 ? <EmptyState title="No completed processes" detail="No authorized rows match this filter." /> : null}
        {query.data && query.data.processes.length > 0 ? (
          <div className="table-scroll">
            <table className="process-results-table">
              <thead><tr><th>Instance</th><th>Workflow</th><th>Status</th><th>Completed</th><th>Duration</th><th>Events</th><th>Initiator</th></tr></thead>
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
