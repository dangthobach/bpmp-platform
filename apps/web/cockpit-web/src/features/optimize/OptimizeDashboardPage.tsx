import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ArrowLeft, ArrowRight, BookmarkPlus, CheckCircle2, Download, RefreshCw, Trash2, Workflow, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useApi } from "../../api/ApiContext";
import type { AggregatePage, AggregateQueryInput, ProcessField, QueryScope, QueryValue, SavedDashboard } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { AppLink } from "../../routing/router";
import { useRuntimeConfig } from "../../config/ConfigContext";

const workflowField = "PROCESS_FIELD_WORKFLOW_TYPE" as const;
const dateField = "PROCESS_FIELD_STARTED_AT" as const;
const durationField = "PROCESS_FIELD_DURATION_MS" as const;
const groupableFields: ProcessField[] = ["PROCESS_FIELD_WORKFLOW_TYPE", "PROCESS_FIELD_WORKFLOW_VERSION", "PROCESS_FIELD_STATUS", "PROCESS_FIELD_ACTIVE_NODE_ID", "PROCESS_FIELD_CONFIG_VERSION", "PROCESS_FIELD_POLICY_VERSION"];
type ExportScope = Extract<QueryScope, "QUERY_SCOPE_ACTIVE" | "QUERY_SCOPE_COMPLETED">;
type TimeRange = "ALL" | "7D" | "30D" | "90D";
const exportFieldCandidates: ProcessField[] = ["PROCESS_FIELD_INSTANCE_ID", "PROCESS_FIELD_WORKFLOW_TYPE", "PROCESS_FIELD_STATUS", "PROCESS_FIELD_STARTED_AT", "PROCESS_FIELD_UPDATED_AT", "PROCESS_FIELD_COMPLETED_AT"];
const aggregateInput = (scope: QueryScope, range: TimeRange, includeCycleTime: boolean, groupBy: ProcessField): AggregateQueryInput => ({
  scope,
  ...(range === "ALL" ? {} : {
    filter: { predicate: {
      field: dateField,
      operator: "COMPARISON_OPERATOR_BETWEEN" as const,
      values: [
        { timestamp_epoch_ms: Date.now() - Number(range.slice(0, -1)) * 24 * 60 * 60 * 1000 },
        { timestamp_epoch_ms: Date.now() },
      ],
    } },
  }),
  group_by: [groupBy],
  metrics: [
    { alias: "instances", operation: "AGGREGATE_OPERATION_COUNT" },
    ...(scope === "QUERY_SCOPE_COMPLETED" && includeCycleTime ? [{ alias: "average_duration_ms", operation: "AGGREGATE_OPERATION_AVG" as const, field: durationField }] : []),
  ],
  limit: 100,
});

function cellValue(value?: QueryValue): string {
  if (!value) return "";
  if ("string_value" in value) return value.string_value;
  if ("integer_value" in value) return String(value.integer_value);
  if ("decimal_value" in value) return value.decimal_value;
  if ("boolean_value" in value) return String(value.boolean_value);
  return new Date(value.timestamp_epoch_ms).toLocaleString();
}

function count(row: AggregatePage["rows"][number] | undefined): number {
  const raw = row?.metrics.find((metric) => metric.name === "instances")?.value;
  if (!raw) return 0;
  if ("integer_value" in raw) return raw.integer_value;
  if ("decimal_value" in raw) return Number(raw.decimal_value) || 0;
  return Number(cellValue(raw)) || 0;
}

function averageCycleTime(row: AggregatePage["rows"][number] | undefined): string {
  const raw = row?.metrics.find((metric) => metric.name === "average_duration_ms")?.value;
  const milliseconds = raw && "decimal_value" in raw ? Number(raw.decimal_value) : raw && "integer_value" in raw ? raw.integer_value : Number.NaN;
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return "—";
  const minutes = milliseconds / 60_000;
  if (minutes < 60) return `${Math.round(minutes)} min`;
  const hours = minutes / 60;
  if (hours < 48) return `${hours.toFixed(1)} hr`;
  return `${(hours / 24).toFixed(1)} days`;
}

export function OptimizeDashboardPage() {
  const api = useApi();
  const { identity } = useAuth();
  const config = useRuntimeConfig();
  const queryClient = useQueryClient();
  const [exportId, setExportId] = useState("");
  const [exportScope, setExportScope] = useState<ExportScope>("QUERY_SCOPE_COMPLETED");
  const [timeRange, setTimeRange] = useState<TimeRange>("30D");
  const [groupBy, setGroupBy] = useState<ProcessField>(workflowField);
  const [dashboardName, setDashboardName] = useState("");
  const [loadedDashboard, setLoadedDashboard] = useState<SavedDashboard | null>(null);
  const [dashboardPageTokens, setDashboardPageTokens] = useState<string[]>([""]);
  const dashboardPageToken = dashboardPageTokens.at(-1) ?? "";
  const dashboards = useQuery({
    queryKey: ["saved-dashboards", identity?.tenantId, dashboardPageToken],
    queryFn: () => api.listDashboards(dashboardPageToken, config.defaultPageSize),
    enabled: Boolean(identity),
  });
  const capabilities = useQuery({
    queryKey: ["process-query-capabilities", identity?.tenantId],
    queryFn: () => api.getProcessQueryCapabilities(),
    enabled: Boolean(identity),
    staleTime: 60_000,
  });
  const startedAtFilter = capabilities.data?.fields.find((field) => field.field === dateField);
  const canFilterByStartedAt = Boolean(startedAtFilter?.filterable && startedAtFilter.operators.includes("COMPARISON_OPERATOR_BETWEEN"));
  const canReadCycleTime = Boolean(capabilities.data?.fields.some((field) => field.field === durationField && field.selectable));
  const groupFields = capabilities.data?.fields.filter((field) => field.selectable && groupableFields.includes(field.field)) ?? [];
  const effectiveGroupBy = groupFields.some((field) => field.field === groupBy) ? groupBy : groupFields[0]?.field ?? workflowField;
  const canGroup = groupFields.length > 0;
  const effectiveTimeRange = canFilterByStartedAt ? timeRange : "ALL";
  const active = useQuery({
    queryKey: ["optimize-aggregate", identity?.tenantId, "active", effectiveTimeRange, effectiveGroupBy],
    queryFn: () => api.aggregateProcesses(aggregateInput("QUERY_SCOPE_ACTIVE", effectiveTimeRange, false, effectiveGroupBy)),
    enabled: Boolean(identity && canGroup && capabilities.data?.scopes.includes("QUERY_SCOPE_ACTIVE")),
  });
  const completed = useQuery({
    queryKey: ["optimize-aggregate", identity?.tenantId, "completed", effectiveTimeRange, effectiveGroupBy, canReadCycleTime],
    queryFn: () => api.aggregateProcesses(aggregateInput("QUERY_SCOPE_COMPLETED", effectiveTimeRange, canReadCycleTime, effectiveGroupBy)),
    enabled: Boolean(identity && canGroup && capabilities.data?.scopes.includes("QUERY_SCOPE_COMPLETED")),
  });
  const exportFields = capabilities.data?.fields.filter((field) => field.selectable && exportFieldCandidates.includes(field.field)).map((field) => field.field) ?? [];
  const availableExportScopes = capabilities.data?.scopes.filter((scope): scope is ExportScope => scope === "QUERY_SCOPE_ACTIVE" || scope === "QUERY_SCOPE_COMPLETED") ?? [];
  useEffect(() => { if (availableExportScopes.length && !availableExportScopes.includes(exportScope)) setExportScope(availableExportScopes[0]!); }, [availableExportScopes, exportScope]);
  const createExport = useMutation({ mutationFn: () => api.createQueryExport({ scope: exportScope, select: exportFields }, 10_000), onSuccess: (result) => setExportId(result.id) });
  const exportStatus = useQuery({ queryKey: ["optimize-export", identity?.tenantId, exportId], queryFn: () => api.getQueryExport(exportId), enabled: Boolean(identity && exportId), refetchInterval: (query) => query.state.data?.status === "QUERY_EXPORT_STATUS_PENDING" ? 2_000 : false });
  const cancelExport = useMutation({ mutationFn: () => api.cancelQueryExport(exportId), onSuccess: (result) => { if (result.status === "QUERY_EXPORT_STATUS_CANCELLED") setExportId(""); } });
  const saveDashboard = useMutation({
    mutationFn: () => api.saveDashboard({
      id: loadedDashboard?.id ?? crypto.randomUUID(),
      name: dashboardName.trim(),
      expected_version: loadedDashboard?.version ?? 0,
      config_json: JSON.stringify({ schema_version: 1, widgets: [{ id: "workflow-throughput", type: "workflow-throughput", time_range: timeRange, group_by: effectiveGroupBy, include_cycle_time: canReadCycleTime }] }),
    }),
    onSuccess: async (saved) => { setLoadedDashboard(saved); setDashboardName(saved.name); await queryClient.invalidateQueries({ queryKey: ["saved-dashboards", identity?.tenantId] }); },
  });
  const deleteDashboard = useMutation({
    mutationFn: (dashboard: SavedDashboard) => api.deleteDashboard(dashboard.id, dashboard.version),
    onSuccess: async (_, dashboard) => { if (loadedDashboard?.id === dashboard.id) { setLoadedDashboard(null); setDashboardName(""); } await queryClient.invalidateQueries({ queryKey: ["saved-dashboards", identity?.tenantId] }); },
  });
  const isRefreshing = capabilities.isFetching || active.isFetching || completed.isFetching || exportStatus.isFetching;
  const refresh = () => {
    void capabilities.refetch();
    if (capabilities.data?.scopes.includes("QUERY_SCOPE_ACTIVE")) void active.refetch();
    if (capabilities.data?.scopes.includes("QUERY_SCOPE_COMPLETED")) void completed.refetch();
    if (exportId) void exportStatus.refetch();
  };
  const activeAvailable = Boolean(capabilities.data?.scopes.includes("QUERY_SCOPE_ACTIVE"));
  const completedAvailable = Boolean(capabilities.data?.scopes.includes("QUERY_SCOPE_COMPLETED"));
  const activeRows = activeAvailable ? active.data?.rows ?? [] : [];
  const completedRows = completedAvailable ? completed.data?.rows ?? [] : [];
  const activeByDimension = new Map(activeRows.map((row) => [cellValue(row.dimensions[0]?.value), count(row)]));
  const completedByDimension = new Map(completedRows.map((row) => [cellValue(row.dimensions[0]?.value), count(row)]));
  const completedRowByDimension = new Map(completedRows.map((row) => [cellValue(row.dimensions[0]?.value), row]));
  const dimensionValues = [...new Set([...activeByDimension.keys(), ...completedByDimension.keys()])];
  const rows = dimensionValues.map((dimension) => ({
    dimension,
    active: activeAvailable ? activeByDimension.get(dimension) ?? 0 : null,
    completed: completedAvailable ? completedByDimension.get(dimension) ?? 0 : null,
  })).sort((left, right) => ((right.active ?? 0) + (right.completed ?? 0)) - ((left.active ?? 0) + (left.completed ?? 0)));
  const activeTotal = rows.reduce((sum, row) => sum + (row.active ?? 0), 0);
  const completedTotal = rows.reduce((sum, row) => sum + (row.completed ?? 0), 0);
  const queryError = capabilities.error ?? active.error ?? completed.error;
  const aggregatesReady = Boolean(capabilities.data && canGroup && (activeAvailable || completedAvailable)
    && (!activeAvailable || active.data) && (!completedAvailable || completed.data));
  const exportCSV = () => {
    const csv = exportStatus.data?.csv;
    if (exportStatus.data?.status !== "QUERY_EXPORT_STATUS_COMPLETED" || !csv) return;
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `bpmp-process-export-${exportScope === "QUERY_SCOPE_ACTIVE" ? "active" : "completed"}.csv`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <>
      <header className="page-header">
        <div><h1>Optimize</h1><p>Live process counts grouped by workflow type. Values come from Projection Service aggregates.</p></div>
        <div className="header-actions">
          <Button icon={RefreshCw} onClick={refresh} disabled={isRefreshing}>{isRefreshing ? "Refreshing" : "Refresh"}</Button>
          <AppLink className="button button--secondary" to="/processes">Process explorer <ArrowRight size={14} /></AppLink>
        </div>
      </header>

      <section className="table-surface optimize-export">
        <header className="portal-section-heading"><div><h2>Saved dashboards</h2><p>Save this aggregate view as actor-owned configuration. Opening it reruns aggregates under the current query policy.</p></div></header>
        <form className="header-actions" onSubmit={(event) => { event.preventDefault(); if (dashboardName.trim()) saveDashboard.mutate(); }}>
          <label className="optimize-export__scope">Dashboard name<input value={dashboardName} maxLength={256} onChange={(event) => setDashboardName(event.target.value)} placeholder="e.g. Monthly operations" /></label>
          <Button icon={BookmarkPlus} type="submit" disabled={!dashboardName.trim() || saveDashboard.isPending}>{saveDashboard.isPending ? "Saving…" : loadedDashboard ? "Update dashboard" : "Save dashboard"}</Button>
        </form>
        {saveDashboard.isError ? <ErrorState message={saveDashboard.error.message} retry={() => saveDashboard.mutate()} /> : null}
        {dashboards.isPending ? <LoadingRows /> : null}
        {dashboards.isError ? <ErrorState message={dashboards.error.message} retry={() => void dashboards.refetch()} /> : null}
        {dashboards.isSuccess && dashboards.data.dashboards.length === 0 ? <EmptyState title="No saved dashboards" detail="Save the current aggregate view to add a personal dashboard." /> : null}
        {dashboards.data?.dashboards.length ? <div className="table-scroll"><table><thead><tr><th>Name</th><th>Updated</th><th>Version</th><th>Actions</th></tr></thead><tbody>{dashboards.data.dashboards.map((dashboard) => { const settings = dashboardSettings(dashboard.config_json); return <tr key={dashboard.id}><td>{dashboard.name}</td><td>{new Date(dashboard.updated_at_epoch_ms).toLocaleString()}</td><td>{dashboard.version}</td><td><div className="header-actions"><Button icon={Workflow} onClick={() => { if (!settings) return; setLoadedDashboard(dashboard); setDashboardName(dashboard.name); setTimeRange(settings.timeRange); setGroupBy(settings.groupBy); }} disabled={!settings}>Open</Button><Button icon={Trash2} disabled={deleteDashboard.isPending} onClick={() => deleteDashboard.mutate(dashboard)} aria-label={`Delete ${dashboard.name}`}>Delete</Button></div></td></tr>; })}</tbody></table></div> : null}
        {dashboards.data ? <footer className="pagination"><span>Dashboard page {dashboardPageTokens.length}</span><div><Button icon={ArrowLeft} disabled={dashboardPageTokens.length === 1 || dashboards.isFetching} onClick={() => setDashboardPageTokens((tokens) => tokens.slice(0, -1))}>Previous</Button><Button icon={ArrowRight} disabled={!dashboards.data.next_page_token || dashboards.isFetching} onClick={() => { const next = dashboards.data.next_page_token; if (next) setDashboardPageTokens((tokens) => [...tokens, next]); }}>Next</Button></div></footer> : null}
        {deleteDashboard.isError ? <ErrorState message={deleteDashboard.error.message} retry={() => { const dashboard = deleteDashboard.variables; if (dashboard) deleteDashboard.mutate(dashboard); }} /> : null}
      </section>

      {capabilities.isPending ? <LoadingRows /> : null}
      {queryError ? <ErrorState message={queryError instanceof Error ? queryError.message : "Could not load authorized process aggregates"} retry={refresh} /> : null}
      {capabilities.data && (!activeAvailable || !completedAvailable) ? <div className="inline-notice" role="status">Some aggregate scopes are hidden by tenant query policy. Only authorized scopes are shown.</div> : null}
      {capabilities.data && !activeAvailable && !completedAvailable ? <div className="inline-notice" role="status">This tenant's query policy does not expose active or completed aggregate scopes.</div> : null}
      {capabilities.data && !canGroup ? <div className="inline-notice" role="status">This tenant's query policy does not expose any groupable process fields.</div> : null}
      {capabilities.data && groupBy !== effectiveGroupBy ? <div className="inline-notice" role="status">A saved grouping field is no longer readable. The dashboard is using the first currently authorized field.</div> : null}
      {capabilities.data && !canFilterByStartedAt && timeRange !== "ALL" ? <div className="inline-notice" role="status">This tenant's query policy does not allow filtering by start time. Aggregate results are shown without a time window.</div> : null}

      {!queryError && aggregatesReady ? (
        <>
          <section className="portal-summary optimize-summary" aria-label="Process summary">
            {activeAvailable ? <article className="portal-summary__card"><span className="portal-summary__icon"><Activity size={18} /></span><div><span>Active instances</span><strong>{activeTotal.toLocaleString()}</strong></div></article> : null}
            {completedAvailable ? <article className="portal-summary__card"><span className="portal-summary__icon"><CheckCircle2 size={18} /></span><div><span>Completed instances</span><strong>{completedTotal.toLocaleString()}</strong></div></article> : null}
            <article className="portal-summary__card"><span className="portal-summary__icon"><Workflow size={18} /></span><div><span>Groups</span><strong>{rows.length.toLocaleString()}</strong></div></article>
          </section>

          <section className="table-surface optimize-table">
            <header className="portal-section-heading"><div><h2>Aggregate breakdown</h2><p>Counts and cycle time are grouped and authorized by the Projection Service.</p></div><div className="header-actions"><label className="optimize-export__scope">Group by<select value={effectiveGroupBy} onChange={(event) => setGroupBy(event.target.value as ProcessField)} disabled={!groupFields.length}>{groupFields.map((field) => <option key={field.field} value={field.field}>{fieldLabel(field.field)}</option>)}</select></label><label className="optimize-export__scope">Started within<select value={canFilterByStartedAt ? timeRange : "ALL"} onChange={(event) => setTimeRange(event.target.value as TimeRange)}><option value="7D">7 days</option><option value="30D">30 days</option><option value="90D">90 days</option><option value="ALL">All time</option></select></label></div></header>
            {rows.length === 0 ? <EmptyState title="No process aggregates" detail="No authorized process rows matched the current tenant scopes." /> : (
              <div className="table-scroll"><table>
                <thead><tr><th>{fieldLabel(effectiveGroupBy)}</th>{activeAvailable ? <th>Active</th> : null}{completedAvailable ? <th>Completed</th> : null}{canReadCycleTime && completedAvailable ? <th>Avg cycle time</th> : null}{activeAvailable && completedAvailable ? <th>Total</th> : null}{effectiveGroupBy === workflowField ? <th>Explore</th> : null}</tr></thead>
                <tbody>{rows.map((row) => {
                  const completedRow = completedRowByDimension.get(row.dimension);
                  return <tr key={row.dimension}><td>{row.dimension || "(empty / not set)"}</td>{activeAvailable ? <td>{(row.active ?? 0).toLocaleString()}</td> : null}{completedAvailable ? <td>{(row.completed ?? 0).toLocaleString()}</td> : null}{canReadCycleTime && completedAvailable ? <td>{averageCycleTime(completedRow)}</td> : null}{activeAvailable && completedAvailable ? <td><strong>{((row.active ?? 0) + (row.completed ?? 0)).toLocaleString()}</strong></td> : null}{effectiveGroupBy === workflowField ? <td><div className="header-actions">{activeAvailable && row.active ? <AppLink className="button button--ghost" to={`/processes?workflow_type=${encodeURIComponent(row.dimension)}&scope=QUERY_SCOPE_ACTIVE`}>Active</AppLink> : null}{completedAvailable && row.completed ? <AppLink className="button button--ghost" to={`/processes?workflow_type=${encodeURIComponent(row.dimension)}&scope=QUERY_SCOPE_COMPLETED`}>Completed</AppLink> : null}</div></td> : null}</tr>;
                })}</tbody>
              </table></div>
            )}
          </section>
          <section className="table-surface optimize-export">
            <header className="portal-section-heading"><div><h2>Process data export</h2><p>Projection Service runs a bounded, authorization-checked export using only selectable fields and scopes.</p></div></header>
            <div className="header-actions">
              <label className="optimize-export__scope">Scope<select value={exportScope} onChange={(event) => setExportScope(event.target.value as ExportScope)} disabled={createExport.isPending || Boolean(exportId)}>{availableExportScopes.map((scope) => <option key={scope} value={scope}>{scope === "QUERY_SCOPE_ACTIVE" ? "Active" : "Completed"}</option>)}</select></label>
              <Button icon={Download} onClick={() => createExport.mutate()} disabled={!availableExportScopes.includes(exportScope) || exportFields.length === 0 || createExport.isPending || Boolean(exportId)}>{createExport.isPending ? "Submitting…" : "Create CSV export"}</Button>
            </div>
            {exportFields.length === 0 ? <p className="inline-notice">This tenant's query policy does not expose selectable process fields for export.</p> : null}
            {createExport.isError ? <p role="alert" className="task-management-error">{createExport.error.message}. Review the error before starting a new export.</p> : null}
            {exportId && exportStatus.isPending ? <LoadingRows /> : null}
            {exportStatus.isError ? <ErrorState message={exportStatus.error.message} retry={() => void exportStatus.refetch()} /> : null}
            {exportStatus.data?.status === "QUERY_EXPORT_STATUS_PENDING" ? <div className="inline-notice" role="status">Export is processing. The page checks its status every two seconds.<Button icon={X} onClick={() => cancelExport.mutate()} disabled={cancelExport.isPending}>{cancelExport.isPending ? "Cancelling…" : "Cancel export"}</Button></div> : null}
            {exportStatus.data?.status === "QUERY_EXPORT_STATUS_COMPLETED" ? <div className="inline-notice" role="status">Export ready · {exportStatus.data.row_count?.toLocaleString() ?? 0} rows · expires {new Date(exportStatus.data.expires_at_epoch_ms).toLocaleString()} <Button icon={Download} onClick={exportCSV}>Download CSV</Button></div> : null}
            {exportStatus.data?.status === "QUERY_EXPORT_STATUS_FAILED" ? <ErrorState message={`Export failed (${exportStatus.data.error_code || "unknown error"})`} retry={() => { setExportId(""); createExport.mutate(); }} /> : null}
            {exportStatus.data?.status === "QUERY_EXPORT_STATUS_CANCELLED" ? <p role="status" className="inline-notice">Export cancelled.</p> : null}
            {cancelExport.isError ? <ErrorState message={cancelExport.error.message} retry={() => cancelExport.mutate()} /> : null}
          </section>
        </>
      ) : null}
    </>
  );
}

function dashboardSettings(configJSON: string): { timeRange: TimeRange; groupBy: ProcessField } | null {
  try {
    const document = JSON.parse(configJSON) as { schema_version?: number; widgets?: { id?: string; time_range?: string; group_by?: string }[] };
    if (document.schema_version !== 1 || !Array.isArray(document.widgets) || document.widgets.length !== 1) return null;
    const widget = document.widgets.find((item) => item.id === "workflow-throughput");
    if (!widget || !["ALL", "7D", "30D", "90D"].includes(widget.time_range ?? "")) return null;
    const groupBy = (widget.group_by ?? workflowField) as ProcessField;
    return { timeRange: widget.time_range as TimeRange, groupBy };
  } catch {
    return null;
  }
}

function fieldLabel(field: ProcessField): string {
  const labels: Partial<Record<ProcessField, string>> = {
    PROCESS_FIELD_WORKFLOW_TYPE: "Workflow type",
    PROCESS_FIELD_WORKFLOW_VERSION: "Workflow version",
    PROCESS_FIELD_STATUS: "Status",
    PROCESS_FIELD_ACTIVE_NODE_ID: "Active node",
    PROCESS_FIELD_STARTED_AT: "Started at",
    PROCESS_FIELD_UPDATED_AT: "Updated at",
    PROCESS_FIELD_COMPLETED_AT: "Completed at",
    PROCESS_FIELD_CONFIG_VERSION: "Configuration version",
    PROCESS_FIELD_POLICY_VERSION: "Policy version",
    PROCESS_FIELD_INITIATOR_ACTOR_ID: "Initiator",
  };
  return labels[field] ?? field.replace("PROCESS_FIELD_", "").replaceAll("_", " ").toLowerCase();
}
