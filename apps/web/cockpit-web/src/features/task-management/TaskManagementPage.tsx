import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, ClipboardList, RefreshCw, ShieldCheck, Users } from "lucide-react";
import { useRef, useState } from "react";
import { useApi } from "../../api/ApiContext";
import type { AssignmentPolicy } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { useRuntimeConfig } from "../../config/ConfigContext";
import { AppLink } from "../../routing/router";

type PolicyForm = Omit<AssignmentPolicy, "version" | "is_deleted"> & { expected_version: number };
const emptyForm = (): PolicyForm => ({ policy_ref: "", workflow_type: "", workflow_version: "", node_id: "", assignee_id: "", candidate_group: "", sla_duration_ms: 0, escalation_policy_ref: "", config_version: "", expected_version: 0 });

export function TaskManagementPage() {
  const api = useApi();
  const queryClient = useQueryClient();
  const { identity } = useAuth();
  const config = useRuntimeConfig();
  const [form, setForm] = useState<PolicyForm>(emptyForm);
  const [policyPageToken, setPolicyPageToken] = useState("");
  const [previousPolicyTokens, setPreviousPolicyTokens] = useState<string[]>([]);
  const retryInput = useRef<{ input: PolicyForm; idempotencyKey: string } | null>(null);
  const groups = useQuery({ queryKey: ["access-queues", identity?.tenantId], queryFn: () => api.listAccessQueues(), enabled: Boolean(identity), staleTime: config.staleTimeMs });
  const workItems = useQuery({ queryKey: ["work-items", identity?.tenantId, "task-management", config.defaultPageSize], queryFn: () => api.listWorkItems("", config.defaultPageSize), enabled: Boolean(identity), staleTime: config.staleTimeMs });
  const policies = useQuery({ queryKey: ["assignment-policies", identity?.tenantId, policyPageToken], queryFn: () => api.listAssignmentPolicies(policyPageToken, config.defaultPageSize), enabled: Boolean(identity), staleTime: config.staleTimeMs });
  const save = useMutation({ mutationFn: ({ input, idempotencyKey }: { input: PolicyForm; idempotencyKey: string }) => api.upsertAssignmentPolicy(input, idempotencyKey), onSuccess: async () => { retryInput.current = null; setForm(emptyForm()); setPolicyPageToken(""); setPreviousPolicyTokens([]); await Promise.all([queryClient.invalidateQueries({ queryKey: ["assignment-policies", identity?.tenantId] }), queryClient.invalidateQueries({ queryKey: ["access-queues", identity?.tenantId] })]); } });
  const refreshing = groups.isFetching || workItems.isFetching || policies.isFetching;
  const items = workItems.data?.work_items ?? [];
  const groupCounts = new Map<string, number>();
  for (const item of items) if (item.candidate_group) groupCounts.set(item.candidate_group, (groupCounts.get(item.candidate_group) ?? 0) + 1);
  const editPolicy = (policy: AssignmentPolicy) => setForm({ policy_ref: policy.policy_ref, workflow_type: policy.workflow_type, workflow_version: policy.workflow_version, node_id: policy.node_id, assignee_id: policy.assignee_id, candidate_group: policy.candidate_group, sla_duration_ms: policy.sla_duration_ms, escalation_policy_ref: policy.escalation_policy_ref, config_version: policy.config_version, expected_version: policy.version });

  return <>
    <header className="page-header"><div><h1>Task Management</h1><p>Assignment policies owned by Human Runtime; queue access remains governed by the published Authz catalog.</p></div><div className="header-actions"><Button icon={RefreshCw} onClick={() => { void groups.refetch(); void workItems.refetch(); void policies.refetch(); }} disabled={refreshing}>Refresh</Button><AppLink className="button button--secondary" to="/tasklist"><ClipboardList size={15} /> Open tasklist</AppLink><AppLink className="button button--primary" to="/access"><ShieldCheck size={15} /> Queue access policy</AppLink></div></header>
    <section className="portal-summary" aria-label="Task management summary"><article className="portal-summary__card"><span className="portal-summary__icon"><Users size={18} /></span><div><span>Assignment groups</span><strong>{groups.data?.length ?? (groups.isPending ? "—" : 0)}</strong></div></article><article className="portal-summary__card"><span className="portal-summary__icon"><ClipboardList size={18} /></span><div><span>Active assignment policies</span><strong>{policies.data?.policies.length ?? (policies.isPending ? "—" : 0)}</strong></div></article><AppLink className="portal-summary__action" to="/access"><span><strong>Configure supported queue scopes</strong><small>Draft, validate, and publish Authz policy</small></span><ArrowRight size={18} /></AppLink></section>
    <div className="identity-directory-grid">
      <section className="table-surface"><header className="portal-section-heading"><div><h2>Assignment groups</h2><p>Groups referenced by active Human Runtime assignment policies</p></div></header>{groups.isPending ? <LoadingRows /> : null}{groups.isError ? <ErrorState message={groups.error.message} retry={() => void groups.refetch()} /> : null}{groups.isSuccess && groups.data.length === 0 ? <EmptyState title="No assignment groups" detail="No active candidate groups were returned." /> : null}{groups.data?.length ? <div className="table-scroll"><table><thead><tr><th>Group</th><th>Policy grants</th><th>Work items on this page</th></tr></thead><tbody>{groups.data.map((group) => <tr key={group.group_id}><td className="mono">{group.group_id}</td><td>{group.policy_count}</td><td>{groupCounts.get(group.group_id) ?? 0}</td></tr>)}</tbody></table></div> : null}</section>
      <section className="table-surface"><header className="portal-section-heading"><div><h2>Assignment policies</h2><p>Create or update using optimistic version checks; each policy targets one workflow node and one actor or candidate group.</p></div></header>{policies.isPending ? <LoadingRows /> : null}{policies.isError ? <ErrorState message={policies.error.message} retry={() => void policies.refetch()} /> : null}{policies.data?.policies.length ? <div className="table-scroll"><table><thead><tr><th>Policy</th><th>Workflow / node</th><th>Assignment</th><th>SLA</th><th>Version</th><th></th></tr></thead><tbody>{policies.data.policies.map((policy) => <tr key={`${policy.policy_ref}:${policy.workflow_type}:${policy.workflow_version}:${policy.node_id}`}><td className="mono">{policy.policy_ref}</td><td>{policy.workflow_type} · {policy.workflow_version}<br /><span className="muted">{policy.node_id}</span></td><td>{policy.assignee_id || policy.candidate_group}</td><td>{policy.sla_duration_ms} ms</td><td>{policy.version}</td><td><Button onClick={() => editPolicy(policy)}>Edit</Button></td></tr>)}</tbody></table></div> : null}{policies.data ? <div className="configuration-form__actions"><Button disabled={!previousPolicyTokens.length || policies.isFetching} onClick={() => { const previous = [...previousPolicyTokens]; const token = previous.pop() ?? ""; setPreviousPolicyTokens(previous); setPolicyPageToken(token); }}>Previous</Button><span>Page {previousPolicyTokens.length + 1}</span><Button disabled={!policies.data.next_page_token || policies.isFetching} onClick={() => { setPreviousPolicyTokens([...previousPolicyTokens, policyPageToken]); setPolicyPageToken(policies.data.next_page_token); }}>Next</Button></div> : null}</section>
    </div>
    <section className="table-surface task-policy-editor"><header className="portal-section-heading"><div><h2>{form.expected_version ? `Edit policy · version ${form.expected_version}` : "Create assignment policy"}</h2><p>Exactly one of assignee or candidate group is required. A create uses expected version 0; updates use the version shown above.</p></div></header>
      <form className="configuration-form" onSubmit={(event) => { event.preventDefault(); if ((form.assignee_id.trim() === "") === (form.candidate_group.trim() === "")) return; retryInput.current = { input: { ...form }, idempotencyKey: crypto.randomUUID() }; save.mutate(retryInput.current); }}>
        <label>Policy reference<input required value={form.policy_ref} disabled={form.expected_version > 0} onChange={(event) => setForm({ ...form, policy_ref: event.target.value })} /></label>
        <label>Workflow type<input required value={form.workflow_type} disabled={form.expected_version > 0} onChange={(event) => setForm({ ...form, workflow_type: event.target.value })} /></label>
        <label>Workflow version<input required value={form.workflow_version} disabled={form.expected_version > 0} onChange={(event) => setForm({ ...form, workflow_version: event.target.value })} /></label>
        <label>Node ID<input required value={form.node_id} disabled={form.expected_version > 0} onChange={(event) => setForm({ ...form, node_id: event.target.value })} /></label>
        <label>Assignee ID<input value={form.assignee_id} onChange={(event) => setForm({ ...form, assignee_id: event.target.value, candidate_group: event.target.value ? "" : form.candidate_group })} /></label>
        <label>Candidate group<select value={form.candidate_group} onChange={(event) => setForm({ ...form, candidate_group: event.target.value, assignee_id: event.target.value ? "" : form.assignee_id })}><option value="">No group</option>{groups.data?.map((group) => <option key={group.group_id} value={group.group_id}>{group.group_id}</option>)}</select></label>
        <label>SLA duration (ms)<input type="number" min="0" max="9007199254740" step="1" value={form.sla_duration_ms} onChange={(event) => setForm({ ...form, sla_duration_ms: Number(event.target.value) })} /></label>
        <label>Escalation policy reference<input value={form.escalation_policy_ref} onChange={(event) => setForm({ ...form, escalation_policy_ref: event.target.value })} /></label>
        <label>Config version<input required value={form.config_version} onChange={(event) => setForm({ ...form, config_version: event.target.value })} /></label>
        <div className="configuration-form__actions"><Button type="submit" disabled={save.isPending || ((form.assignee_id.trim() === "") === (form.candidate_group.trim() === ""))}>{save.isPending ? "Saving…" : "Save policy"}</Button><Button type="button" onClick={() => { retryInput.current = null; setForm(emptyForm()); save.reset(); }}>Clear</Button></div>
      </form>{save.isError ? <div className="task-management-error"><ErrorState message={save.error.message} retry={() => { if (retryInput.current) save.mutate(retryInput.current); }} /></div> : null}{save.isSuccess ? <p role="status" className="success-message">Policy {save.data.duplicate ? "already saved (idempotent retry)" : "saved"} at version {save.data.policy.version}.</p> : null}
    </section>
    {workItems.isError ? <div className="task-management-error"><ErrorState message={workItems.error.message} retry={() => void workItems.refetch()} /></div> : null}
  </>;
}
