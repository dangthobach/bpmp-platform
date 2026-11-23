import { useCallback, useEffect, useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Box, Button, ErrorPanel, Page, Paper, Stack, TextField, Typography } from "@ods/shared-ui";

interface CaseRecord {
  id: string;
  customer_ref: string;
  workflow_type: string;
  workflow_version: string;
  business_status: string;
  orchestration_status: string;
  workflow_instance_id?: string;
  version: number;
  created_at: string;
  updated_at: string;
}
interface SearchPage { cases: CaseRecord[]; next_created_at?: string; next_id?: string }
interface CaseView { case: CaseRecord; start_status: string; start_attempts: number; published_version: number; as_of: string }
interface TimelineEntry { event_id: string; aggregate_version: number; event_type: string; occurred_at: string; published_at?: string }
interface TimelinePage { entries: TimelineEntry[] }
interface CreateResponse { case: CaseRecord; duplicate: boolean }
interface CreateIntent { body: { customer_ref: string; workflow_type: string; workflow_version: string }; key: string; commandId: string }

function CaseWorkspace({ platform }: RemoteRouteProps): ReactNode {
  const [customerRef, setCustomerRef] = useState("");
  const [filter, setFilter] = useState("");
  const [workflowType, setWorkflowType] = useState("approval");
  const [workflowVersion, setWorkflowVersion] = useState("1");
  const [cursor, setCursor] = useState<{ at: string; id: string } | null>(null);
  const [list, setList] = useState<CaseRecord[]>([]);
  const [next, setNext] = useState<{ at: string; id: string } | null>(null);
  const [selectedID, setSelectedID] = useState("");
  const [view, setView] = useState<CaseView | null>(null);
  const [timeline, setTimeline] = useState<TimelineEntry[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [intent, setIntent] = useState<CreateIntent | null>(null);

  const refreshList = useCallback(async () => {
    const params = new URLSearchParams({ limit: "25" });
    if (filter) params.set("customer_ref", filter);
    if (cursor) { params.set("before_created_at", cursor.at); params.set("before_id", cursor.id); }
    const result = await platform.callApi<SearchPage>({ method: "GET", path: `/v1/customer-cases?${params}` });
    if (!result.ok) { setError(result.problem.title); return; }
    setList(result.data.cases ?? []);
    setNext(result.data.next_created_at && result.data.next_id ? { at: result.data.next_created_at, id: result.data.next_id } : null);
    setError("");
  }, [platform, filter, cursor]);

  const refreshDetail = useCallback(async (id: string) => {
    const [detail, events] = await Promise.all([
      platform.callApi<CaseView>({ method: "GET", path: `/v1/customer-cases/${encodeURIComponent(id)}` }),
      platform.callApi<TimelinePage>({ method: "GET", path: `/v1/customer-cases/${encodeURIComponent(id)}/timeline` }),
    ]);
    if (!detail.ok) { setError(detail.problem.title); return; }
    setView(detail.data);
    if (events.ok) setTimeline(events.data.entries ?? []);
    else setError(events.problem.title);
  }, [platform]);

  useEffect(() => { void refreshList(); }, [refreshList]);
  useEffect(() => { if (selectedID) void refreshDetail(selectedID); }, [selectedID, refreshDetail]);

  async function createCase(): Promise<void> {
    const pending = intent ?? {
      body: { customer_ref: customerRef.trim(), workflow_type: workflowType.trim(), workflow_version: workflowVersion.trim() },
      key: crypto.randomUUID(), commandId: crypto.randomUUID(),
    };
    if (!pending.body.customer_ref || !pending.body.workflow_type || !pending.body.workflow_version) { setError("All Case fields are required"); return; }
    setIntent(pending);
    setBusy(true);
    try {
      const result = await platform.callApi<CreateResponse, CreateIntent["body"]>({
        method: "POST", path: "/v1/customer-cases", body: pending.body,
        idempotencyKey: pending.key, commandId: pending.commandId,
      });
      if (!result.ok) { setError(result.problem.title); return; }
      setIntent(null);
      setCustomerRef("");
      setError("");
      setCursor(null);
      setSelectedID(result.data.case.id);
      await refreshList();
    } finally { setBusy(false); }
  }

  return <Page title="Customer Cases">
    <Stack spacing={2}>
      <Paper sx={{ p: 2 }}><Stack spacing={2}>
        <Typography variant="h6">Create Case</Typography>
        <Stack direction={{ xs: "column", md: "row" }} spacing={1}>
          <TextField label="Customer reference" value={customerRef} onChange={(event) => setCustomerRef(event.target.value)} disabled={busy || intent !== null} />
          <TextField label="Workflow type" value={workflowType} onChange={(event) => setWorkflowType(event.target.value)} disabled={busy || intent !== null} />
          <TextField label="Workflow version" value={workflowVersion} onChange={(event) => setWorkflowVersion(event.target.value)} disabled={busy || intent !== null} />
          <Button variant="contained" onClick={() => void createCase()} disabled={busy}>{intent ? "Retry same request" : "Create"}</Button>
        </Stack>
        {intent ? <Typography>Request outcome is unknown. Retry uses the same idempotency key and payload.</Typography> : null}
      </Stack></Paper>
      <Paper sx={{ p: 2 }}><Stack spacing={2}>
        <Typography variant="h6">Search Cases</Typography>
        <Stack direction="row" spacing={1}>
          <TextField label="Exact customer reference" value={filter} onChange={(event) => { setFilter(event.target.value); setCursor(null); }} />
          <Button onClick={() => void refreshList()}>Refresh</Button>
        </Stack>
        {list.length === 0 ? <Typography color="text.secondary">No Cases found.</Typography> : list.map((item) =>
          <Button key={item.id} onClick={() => setSelectedID(item.id)} variant="outlined" aria-pressed={selectedID === item.id}
            sx={{ justifyContent: "space-between", textAlign: "left", py: 1.5, px: 2, borderColor: selectedID === item.id ? "primary.main" : "divider", bgcolor: selectedID === item.id ? "primary.light" : "background.paper", gap: 2 }}>
            <Box sx={{ minWidth: 0 }}><Typography fontWeight={700} noWrap>{item.customer_ref}</Typography><Typography variant="caption" color="text.secondary">{new Date(item.created_at).toLocaleString()} · Version {item.version}</Typography></Box>
            <Box sx={{ flexShrink: 0, textAlign: "right" }}><Typography variant="caption" fontWeight={700}>{item.business_status}</Typography><Typography variant="caption" display="block" color="text.secondary">{item.orchestration_status}</Typography></Box>
          </Button>)}
        {next ? <Button onClick={() => setCursor(next)}>Next page</Button> : null}
      </Stack></Paper>
      {view ? <Paper sx={{ p: 2 }}><Stack spacing={1}>
        <Typography variant="h6">Case 360 · {view.case.customer_ref}</Typography>
        <Typography>Case ID: {view.case.id}</Typography>
        <Typography>Business: {view.case.business_status} · Workflow start: {view.start_status} ({view.start_attempts} attempts)</Typography>
        <Typography>Workflow: {view.case.workflow_type} / {view.case.workflow_version} · Instance: {view.case.workflow_instance_id || "pending"}</Typography>
        <Typography>Case version {view.case.version} · delivered event version {view.published_version} · as of {new Date(view.as_of).toLocaleString()}</Typography>
        {view.published_version < view.case.version ? <Typography color="warning.main">Event delivery is behind the Case record. Refresh to reconcile.</Typography> : null}
        <Button onClick={() => void refreshDetail(view.case.id)}>Refresh Case</Button>
        <Box><Typography variant="h6">Timeline</Typography>{timeline.map((entry) =>
          <Typography key={entry.event_id}>v{entry.aggregate_version} · {entry.event_type} · {new Date(entry.occurred_at).toLocaleString()} · {entry.published_at ? "delivered" : "delivery pending"}</Typography>)}</Box>
      </Stack></Paper> : null}
      {error ? <ErrorPanel title="Case request failed" detail={error} onRetry={() => { void refreshList(); if (selectedID) void refreshDetail(selectedID); }} /> : null}
    </Stack>
  </Page>;
}

export const contractVersion = "1.0";
export const route = { path: "/cases", title: "Cases", Component: CaseWorkspace };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
