import { useRef, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import { Button, Paper, Stack, Typography } from "@ods/shared-ui";
import { commandKey, isRecord, rows, send } from "./api";

export function AccessWorkspace({ platform }: { platform: PlatformServices }): ReactNode {
  const [selectedId, setSelectedId] = useState("");
  const [snapshotJson, setSnapshotJson] = useState('{\n  "schema_version": 1,\n  "entries": [],\n  "policy_grants": []\n}');
  const [reason, setReason] = useState("");
  const [notice, setNotice] = useState("");
  const [failure, setFailure] = useState("");
  const retries = useRef(new Map<string, { fingerprint: string; key: string }>());
  const [policy, setPolicy] = useState({ policy_ref: "", workflow_type: "", workflow_version: "", node_id: "", assignee_id: "", candidate_group: "", sla_duration_ms: 0, escalation_policy_ref: "", config_version: "", expected_version: 0 });
  const versions = useSWR(["studio-access-versions"], async () => send(platform.callApi, "GET", "/v1/access/catalog/versions"));
  const entries = useSWR(["studio-access-entries"], async () => send(platform.callApi, "GET", "/v1/access/catalog/entries"));
  const audit = useSWR(["studio-access-audit"], async () => send(platform.callApi, "GET", "/v1/access/catalog/audit?limit=100"));
  const roles = useSWR(["studio-access-roles"], async () => send(platform.callApi, "GET", "/v1/access/catalog/roles"));
  const users = useSWR(["studio-access-users"], async () => send(platform.callApi, "GET", "/v1/access/users?limit=50"));
  const queues = useSWR(["studio-access-queues"], async () => send(platform.callApi, "GET", "/v1/access/queues"));
  const policies = useSWR(["studio-assignment-policies"], async () => send(platform.callApi, "GET", "/v1/assignment-policies?page_size=50"));
  const selected = versions.data?.ok ? rows(versions.data.data).find((v) => v.id === selectedId) : undefined;
  const auditRows = audit.data?.ok ? rows(audit.data.data) : [];
  const entryRows = entries.data?.ok ? rows(entries.data.data) : [];
  async function mutate(method: "POST" | "PUT", path: string, body: unknown, label: string): Promise<void> {
    setFailure(""); setNotice("");
    const fingerprint = JSON.stringify({ method, path, body });
    const previous = retries.current.get(path);
    const key = previous?.fingerprint === fingerprint ? previous.key : commandKey();
    retries.current.set(path, { fingerprint, key });
    const result = await send(platform.callApi, method, path, body, key);
    if (!result.ok) { setFailure(`${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}, request ${result.requestId}). ${result.problem.status === 409 ? "Reload latest revision before editing." : "Review current version before retrying."}`); if (result.problem.status === 409) { retries.current.delete(path); await versions.mutate(); } return; }
    retries.current.delete(path);
    setNotice(`${label} succeeded (${result.requestId}).`); await versions.mutate(); await audit.mutate();
  }
  function parseSnapshot(): Record<string, unknown> | undefined {
    try { const parsed: unknown = JSON.parse(snapshotJson); if (!isRecord(parsed) || !Array.isArray(parsed.entries) || !Array.isArray(parsed.policy_grants)) throw new Error("Snapshot must contain entries and policy_grants arrays."); return parsed; }
    catch (error) { setFailure(error instanceof Error ? error.message : "Snapshot JSON is invalid."); return undefined; }
  }
  async function createDraft(): Promise<void> { const snapshot = parseSnapshot(); if (!snapshot || !reason.trim()) return; await mutate("POST", "/v1/access/catalog/versions", { snapshot, reason: reason.trim() }, "Access draft created"); }
  async function saveDraft(): Promise<void> {
    if (!selected || selected.state !== "DRAFT") return;
    const snapshot = parseSnapshot(); if (!snapshot || !reason.trim()) return;
    await mutate("PUT", `/v1/access/catalog/versions/${encodeURIComponent(selectedId)}`, { expected_draft_revision: Number(selected.draft_revision), snapshot, reason: reason.trim() }, "Access draft updated");
  }
  async function validate(): Promise<void> { if (selected) await mutate("POST", `/v1/access/catalog/versions/${encodeURIComponent(selectedId)}/validate`, {}, "Access catalog validation"); }
  async function publish(): Promise<void> { if (selected?.state === "VALIDATED" && reason.trim()) await mutate("POST", `/v1/access/catalog/versions/${encodeURIComponent(selectedId)}/publish`, { expected_version_num: Number(selected.version_num), reason: reason.trim() }, "Access catalog publish"); }
  async function loadSelected(id: string): Promise<void> {
    setSelectedId(id); const item = versions.data?.ok ? rows(versions.data.data).find((v) => v.id === id) : undefined;
    if (item && isRecord(item.snapshot)) setSnapshotJson(JSON.stringify(item.snapshot, null, 2));
  }
  async function savePolicy(): Promise<void> {
    if (!policy.policy_ref.trim() || !policy.workflow_type.trim() || !policy.workflow_version.trim() || !policy.node_id.trim() || !policy.config_version.trim() || ((policy.assignee_id.trim() === "") === (policy.candidate_group.trim() === ""))) {
      setFailure("Assignment policy requires its workflow/node/config fields and exactly one assignee or candidate group."); return;
    }
    await mutate("PUT", "/v1/assignment-policies", policy, "Assignment policy saved");
    await Promise.all([policies.mutate(), queues.mutate()]);
  }
  function editPolicy(item: Record<string, unknown>): void {
    setPolicy({
      policy_ref: String(item.policy_ref ?? ""), workflow_type: String(item.workflow_type ?? ""), workflow_version: String(item.workflow_version ?? ""), node_id: String(item.node_id ?? ""),
      assignee_id: String(item.assignee_id ?? ""), candidate_group: String(item.candidate_group ?? ""), sla_duration_ms: Number(item.sla_duration_ms ?? 0),
      escalation_policy_ref: String(item.escalation_policy_ref ?? ""), config_version: String(item.config_version ?? ""), expected_version: Number(item.version ?? 0),
    });
  }
  return <Paper sx={{ p: 2, mt: 2 }}>
    <Typography variant="h2">Access catalog</Typography><Typography color="text.secondary">Catalog mutations are owner-authorized; role and grant semantics remain in the Authz service.</Typography>
    {versions.isLoading ? <p>Loading catalog versions…</p> : null}{versions.data && !versions.data.ok ? <p role="alert">Access API unavailable: {versions.data.problem.code}</p> : null}
    <ul>{(versions.data?.ok ? rows(versions.data.data) : []).map((item, i) => <li key={String(item.id ?? i)}><Button type="button" onClick={() => void loadSelected(String(item.id ?? ""))}>v{String(item.version_num)} · {String(item.state)} · {String(item.id)}</Button></li>)}</ul>
    <label>Catalog snapshot JSON<textarea aria-label="Catalog snapshot JSON" rows={12} value={snapshotJson} onChange={(event) => setSnapshotJson(event.target.value)} /></label>
    <label>Reason<input aria-label="Access change reason" value={reason} onChange={(event) => setReason(event.target.value)} /></label>
    <Stack direction="row" spacing={1}><Button type="button" disabled={!reason.trim()} onClick={() => void createDraft()}>Create draft</Button><Button type="button" disabled={!selected || selected.state !== "DRAFT" || !reason.trim()} onClick={() => void saveDraft()}>Save draft</Button><Button type="button" disabled={!selected || selected.state !== "DRAFT"} onClick={() => void validate()}>Validate draft</Button><Button type="button" disabled={!selected || selected.state !== "VALIDATED" || !reason.trim()} onClick={() => void publish()}>Publish catalog</Button></Stack>
    {failure ? <p role="alert">{failure}</p> : null}{notice ? <p role="status">{notice}</p> : null}
    {entries.data && !entries.data.ok ? <p role="alert">Owner catalog entries unavailable: {entries.data.problem.code}</p> : null}
    {audit.data && !audit.data.ok ? <p role="alert">Access audit unavailable: {audit.data.problem.code}</p> : null}
    <details><summary>Owner capability entries ({entryRows.length})</summary><pre>{JSON.stringify(entries.data?.ok ? entries.data.data : null, null, 2)}</pre></details>
    <details><summary>Audit history ({auditRows.length})</summary><pre>{JSON.stringify(audit.data?.ok ? audit.data.data : null, null, 2)}</pre></details>
    <details><summary>Roles and users</summary>{roles.data?.ok ? <pre>{JSON.stringify(roles.data.data, null, 2)}</pre> : roles.data ? <p role="alert">Role catalog unavailable: {roles.data.problem.code}</p> : <p>Loading roles…</p>}{users.data?.ok ? <pre>{JSON.stringify(users.data.data, null, 2)}</pre> : users.data ? <p role="alert">User directory unavailable: {users.data.problem.code}</p> : <p>Loading users…</p>}</details>
    <section aria-label="Assignment policy management">
      <Typography variant="h3">Assignment policies · Human Runtime</Typography>
      {policies.data && !policies.data.ok ? <p role="alert">Assignment policies unavailable: {policies.data.problem.code}</p> : null}
      <ul>{(policies.data?.ok ? rows(policies.data.data, "policies") : []).map((item, index) => <li key={String(item.policy_ref ?? index)}><Button type="button" onClick={() => editPolicy(item)}>{String(item.policy_ref)} · v{String(item.version)}</Button></li>)}</ul>
      <Stack direction="row" spacing={1} sx={{ flexWrap: "wrap" }}>
        <input aria-label="Policy reference" value={policy.policy_ref} disabled={policy.expected_version > 0} onChange={(e) => setPolicy({ ...policy, policy_ref: e.target.value })} />
        <input aria-label="Workflow type" value={policy.workflow_type} disabled={policy.expected_version > 0} onChange={(e) => setPolicy({ ...policy, workflow_type: e.target.value })} />
        <input aria-label="Workflow version" value={policy.workflow_version} disabled={policy.expected_version > 0} onChange={(e) => setPolicy({ ...policy, workflow_version: e.target.value })} />
        <input aria-label="Workflow node" value={policy.node_id} disabled={policy.expected_version > 0} onChange={(e) => setPolicy({ ...policy, node_id: e.target.value })} />
        <input aria-label="Assignee ID" value={policy.assignee_id} onChange={(e) => setPolicy({ ...policy, assignee_id: e.target.value, candidate_group: e.target.value ? "" : policy.candidate_group })} />
        <select aria-label="Candidate group" value={policy.candidate_group} onChange={(e) => setPolicy({ ...policy, candidate_group: e.target.value, assignee_id: e.target.value ? "" : policy.assignee_id })}><option value="">No candidate group</option>{(queues.data?.ok ? rows(queues.data.data, "groups") : []).map((g) => <option key={String(g.group_id)} value={String(g.group_id)}>{String(g.group_id)}</option>)}</select>
        <input aria-label="Policy SLA milliseconds" type="number" min="0" value={policy.sla_duration_ms} onChange={(e) => setPolicy({ ...policy, sla_duration_ms: Number(e.target.value) })} />
        <input aria-label="Escalation policy reference" value={policy.escalation_policy_ref} onChange={(e) => setPolicy({ ...policy, escalation_policy_ref: e.target.value })} />
        <input aria-label="Assignment policy config version" value={policy.config_version} onChange={(e) => setPolicy({ ...policy, config_version: e.target.value })} />
        <Button type="button" onClick={() => void savePolicy()}>Save assignment policy</Button>
        <Button type="button" onClick={() => setPolicy({ policy_ref: "", workflow_type: "", workflow_version: "", node_id: "", assignee_id: "", candidate_group: "", sla_duration_ms: 0, escalation_policy_ref: "", config_version: "", expected_version: 0 })}>New policy</Button>
      </Stack>
    </section>
    {selectedId ? <Acknowledgements platform={platform} versionId={selectedId} /> : null}
  </Paper>;
}

function Acknowledgements({ platform, versionId }: { platform: PlatformServices; versionId: string }): ReactNode {
  const result = useSWR(["studio-access-acknowledgements", versionId], async () => send(platform.callApi, "GET", `/v1/access/catalog/acknowledgements?catalog_version_id=${encodeURIComponent(versionId)}&limit=500`));
  return <details><summary>Consumer acknowledgements</summary>{result.data?.ok ? <pre>{JSON.stringify(result.data.data, null, 2)}</pre> : result.data ? <p role="alert">Acknowledgements unavailable: {result.data.problem.code}</p> : <p>Loading acknowledgements…</p>}</details>;
}
