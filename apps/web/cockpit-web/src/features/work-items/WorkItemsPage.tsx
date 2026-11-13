import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  Eye,
  RefreshCw,
  Search,
  Send,
  Users,
  UserCheck,
  UserMinus,
} from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { executeBatch } from "../../api/batch";
import { useApi } from "../../api/ApiContext";
import type { FormVersion, WorkItemListScope, WorkflowVariableOutput, WorkItem } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { Dialog } from "../../components/Dialog";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { StatusBadge } from "../../components/StatusBadge";
import { useRuntimeConfig } from "../../config/ConfigContext";

type BatchAction = "complete" | "delegate" | "claim" | "unclaim" | null;

export function WorkItemsPage() {
  const api = useApi();
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const queryClient = useQueryClient();
  const [pageTokens, setPageTokens] = useState<string[]>([""]);
  const pageToken = pageTokens.at(-1) ?? "";
  const [listScope, setListScope] = useState<WorkItemListScope>("ALL");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [search, setSearch] = useState("");
  const [batchAction, setBatchAction] = useState<BatchAction>(null);
  const [detailId, setDetailId] = useState("");
  const [decision, setDecision] = useState("");
  const [assignee, setAssignee] = useState("");
  const [candidateGroup, setCandidateGroup] = useState("");
  const [progress, setProgress] = useState<{ processed: number; total: number } | null>(null);
  const [batchSummary, setBatchSummary] = useState<string | null>(null);
  const [formValues, setFormValues] = useState<Record<string, WorkflowVariableOutput>>({});
  const [detailDecision, setDetailDecision] = useState("APPROVE");

  const query = useQuery({
    queryKey: ["work-items", identity?.tenantId, listScope, pageToken, config.defaultPageSize],
    queryFn: () => api.listWorkItems(pageToken, config.defaultPageSize, listScope),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
    placeholderData: keepPreviousData,
  });
  const assignmentGroups = useQuery({
    queryKey: ["access-queues", identity?.tenantId],
    queryFn: () => api.listAccessQueues(),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
  });
  const detail = useQuery({
    queryKey: ["work-item-detail", identity?.tenantId, detailId],
    queryFn: () => api.getWorkItem(detailId),
    enabled: Boolean(identity && detailId),
  });
  const taskForm = useQuery({
    queryKey: ["published-form", identity?.tenantId, detail.data?.work_item.form_key],
    queryFn: () => api.getPublishedFormByKey(detail.data!.work_item.form_key),
    enabled: Boolean(identity && detail.data?.work_item.form_key),
    staleTime: config.staleTimeMs,
  });
  const completeDetail = useMutation({
    mutationFn: () => {
      if (!detail.data) throw new Error("Work item detail is unavailable");
      const formKey = detail.data.work_item.form_key;
      const published = taskForm.data?.published_version;
      if (formKey && (!published || !formOutputIsValid(published, formValues))) throw new Error("Complete every required form field with a valid value before submitting.");
      return api.completeWorkItem({
        workItemId: detail.data.work_item.work_item_id,
        decision: detailDecision,
        expectedVersion: detail.data.work_item.version,
        ...(formKey && published ? { formVersionId: published.id, outputs: formValues } : {}),
        idempotencyKey: `complete:${detail.data.work_item.work_item_id}:${detail.data.work_item.version}:${crypto.randomUUID()}`,
      });
    },
    onSuccess: async () => {
      await Promise.all([queryClient.invalidateQueries({ queryKey: ["work-items"] }), queryClient.invalidateQueries({ queryKey: ["work-item-detail"] })]);
      setBatchSummary("Task completion accepted. The queue has been refreshed.");
      setDetailId("");
      setFormValues({});
    },
  });
  useEffect(() => { setFormValues({}); }, [detail.data?.work_item.work_item_id, taskForm.data?.published_version.id]);

  const visible = useMemo(() => {
    const normalized = search.trim().toLowerCase();
    if (!normalized) return query.data?.work_items ?? [];
    return (query.data?.work_items ?? []).filter((item) =>
      [
        item.work_item_id,
        item.instance_id,
        item.workflow_type,
        item.task_type,
        item.assignee_id,
        item.candidate_group,
      ].some((value) => value.toLowerCase().includes(normalized)),
    );
  }, [query.data, search]);

  const selectedItems = useMemo(
    () => (query.data?.work_items ?? []).filter((item) => selected.has(item.work_item_id)),
    [query.data, selected],
  );

  const batchMutation = useMutation({
    mutationFn: async ({ action, items }: { action: Exclude<BatchAction, null>; items: WorkItem[] }) => {
      setProgress({ processed: 0, total: items.length });
      return executeBatch(
        items,
        {
          chunkSize: config.batchChunkSize,
          concurrency: config.batchConcurrency,
        },
        async (item) => {
          const idempotencyKey = `${action}:${item.work_item_id}:${item.version}`;
          if (action === "complete") {
            await api.completeWorkItem({
              workItemId: item.work_item_id,
              decision,
              expectedVersion: item.version,
              idempotencyKey,
            });
          } else if (action === "claim") {
            await api.claimWorkItem({
              workItemId: item.work_item_id,
              expectedVersion: item.version,
              idempotencyKey,
            });
          } else if (action === "unclaim") {
            await api.unclaimWorkItem({
              workItemId: item.work_item_id,
              expectedVersion: item.version,
              candidateGroup,
              idempotencyKey,
            });
          } else {
            await api.delegateWorkItem({
              workItemId: item.work_item_id,
              expectedVersion: item.version,
              ...(assignee ? { assigneeId: assignee } : { candidateGroup }),
              idempotencyKey,
            });
          }
        },
        (processed, total) => setProgress({ processed, total }),
      );
    },
    onSuccess(result) {
      setBatchSummary(`${result.succeeded.length} succeeded, ${result.failed.length} failed`);
      setSelected(new Set());
      setBatchAction(null);
      void queryClient.invalidateQueries({ queryKey: ["work-items"] });
      void queryClient.invalidateQueries({ queryKey: ["work-item-detail"] });
    },
    onSettled() {
      setProgress(null);
    },
  });

  function toggle(id: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleVisible() {
    setSelected((current) => {
      const next = new Set(current);
      const allSelected = visible.length > 0 && visible.every((item) => next.has(item.work_item_id));
      for (const item of visible) {
        if (allSelected) next.delete(item.work_item_id);
        else next.add(item.work_item_id);
      }
      return next;
    });
  }

  function submitBatch(event: FormEvent) {
    event.preventDefault();
    if (!batchAction || selectedItems.length === 0) return;
    if (batchAction === "complete" && selectedItems.some((item) => item.form_key)) {
      setBatchSummary("Tasks with a form must be completed individually so each published form can be validated and submitted.");
      return;
    }
    batchMutation.mutate({ action: batchAction, items: selectedItems });
  }

  const allVisibleSelected =
    visible.length > 0 && visible.every((item) => selected.has(item.work_item_id));

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Work queue</h1>
          <p>{query.data?.work_items.length ?? 0} items on this page</p>
        </div>
        <div className="header-actions">
          <Button icon={Users} variant={listScope === "ALL" ? "primary" : "secondary"} onClick={() => { setListScope("ALL"); setPageTokens([""]); setSelected(new Set()); }}>All visible</Button>
          <Button icon={UserCheck} variant={listScope === "ASSIGNED_TO_ME" ? "primary" : "secondary"} onClick={() => { setListScope("ASSIGNED_TO_ME"); setPageTokens([""]); setSelected(new Set()); }}>My tasks</Button>
          <Button icon={Users} variant={listScope === "GROUP_QUEUE" ? "primary" : "secondary"} onClick={() => { setListScope("GROUP_QUEUE"); setPageTokens([""]); setSelected(new Set()); }}>Group queue</Button>
          <IconButton icon={RefreshCw} label="Refresh work items" onClick={() => void query.refetch()} disabled={query.isFetching} />
        </div>
      </header>

      <section className="toolbar" aria-label="Work item tools">
        <div className="search-input">
          <Search size={16} aria-hidden="true" />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter current page"
            aria-label="Filter current page"
          />
        </div>
        <div className="toolbar__spacer" />
        <Button
          icon={CheckCircle2}
          onClick={() => setBatchAction("complete")}
          disabled={selectedItems.length === 0}
        >
          Complete
        </Button>
        <Button
          icon={Users}
          onClick={() => setBatchAction("delegate")}
          disabled={selectedItems.length === 0}
        >
          Delegate
        </Button>
        <Button
          icon={UserCheck}
          onClick={() => setBatchAction("claim")}
          disabled={selectedItems.length === 0}
        >
          Claim
        </Button>
        <Button
          icon={UserMinus}
          onClick={() => setBatchAction("unclaim")}
          disabled={selectedItems.length === 0}
        >
          Return to group
        </Button>
      </section>

      {batchSummary ? (
        <div className="inline-notice" role="status">
          <CheckCircle2 size={16} />
          <span>{batchSummary}</span>
          <button onClick={() => setBatchSummary(null)}>Dismiss</button>
        </div>
      ) : null}

      <section className="table-surface">
        {query.isPending ? <LoadingRows /> : null}
        {query.isError ? (
          <ErrorState message="Work items could not be loaded" retry={() => void query.refetch()} />
        ) : null}
        {query.isSuccess && visible.length === 0 ? (
          <EmptyState title="No work items" detail={search.trim() ? "No items match the current page filter." : listScope === "ASSIGNED_TO_ME" ? "No active work items are assigned directly to you." : listScope === "GROUP_QUEUE" ? "No unclaimed work items are available in your candidate groups." : "No active work items are assigned to you or your groups."} />
        ) : null}
        {visible.length > 0 ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th className="selection-cell">
                    <input
                      type="checkbox"
                      checked={allVisibleSelected}
                      onChange={toggleVisible}
                      aria-label="Select all visible work items"
                    />
                  </th>
                  <th>Work item</th>
                  <th>Process</th>
                  <th>Task</th>
                  <th>Assignment</th>
                  <th>SLA</th>
                  <th>Status</th>
                  <th>Details</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((item) => (
                  <tr key={item.work_item_id} className={selected.has(item.work_item_id) ? "is-selected" : ""}>
                    <td className="selection-cell">
                      <input
                        type="checkbox"
                        checked={selected.has(item.work_item_id)}
                        onChange={() => toggle(item.work_item_id)}
                        aria-label={`Select ${item.work_item_id}`}
                      />
                    </td>
                    <td>
                      <strong>{item.work_item_id}</strong>
                      <span className="cell-secondary">{item.instance_id}</span>
                    </td>
                    <td>
                      <span>{item.workflow_type}</span>
                      <span className="cell-secondary">v{item.workflow_version}</span>
                    </td>
                    <td>
                      <span>{item.task_type}</span>
                      <span className="cell-secondary">{item.node_id}</span>
                    </td>
                    <td>{item.assignee_id || item.candidate_group || "Unassigned"}</td>
                    <td>{formatDeadline(item.sla_deadline_epoch_ms)}</td>
                    <td><StatusBadge status={item.status} /></td>
                    <td><Button icon={Eye} onClick={() => setDetailId(item.work_item_id)}>View</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        <footer className="pagination">
          <span>{selectedItems.length} selected</span>
          <div>
            <IconButton
              icon={ArrowLeft}
              label="Previous page"
              disabled={pageTokens.length === 1}
              onClick={() => {
                setPageTokens((tokens) => tokens.slice(0, -1));
                setSelected(new Set());
              }}
            />
            <span>Page {pageTokens.length}</span>
            <IconButton
              icon={ArrowRight}
              label="Next page"
              disabled={!query.data?.next_page_token}
              onClick={() => {
                const next = query.data?.next_page_token;
                if (next) setPageTokens((tokens) => [...tokens, next]);
                setSelected(new Set());
              }}
            />
          </div>
        </footer>
      </section>

      <Dialog
        open={batchAction !== null}
        title={{ complete: "Complete work items", delegate: "Delegate work items", claim: "Claim work items", unclaim: "Return work items to a group" }[batchAction ?? "complete"]}
        description={`${selectedItems.length} selected items`}
        onClose={() => setBatchAction(null)}
        footer={
          <>
            <Button onClick={() => setBatchAction(null)}>Cancel</Button>
            <Button
              variant="primary"
              icon={batchAction === "complete" ? CheckCircle2 : batchAction === "claim" ? UserCheck : batchAction === "unclaim" ? UserMinus : Send}
              type="submit"
              form="batch-form"
              disabled={
                batchMutation.isPending ||
                (batchAction === "complete" ? !decision.trim() : batchAction === "claim" ? false : batchAction === "unclaim" ? (!candidateGroup || assignmentGroups.isPending || assignmentGroups.isError || assignmentGroups.data?.length === 0) : (!assignee.trim() && !candidateGroup.trim()))
              }
            >
              {batchMutation.isPending ? "Processing" : "Apply"}
            </Button>
          </>
        }
      >
        <form id="batch-form" onSubmit={submitBatch}>
          {batchAction === "complete" ? (
            <div className="field">
              <label htmlFor="decision">Decision</label>
              <input id="decision" value={decision} onChange={(event) => setDecision(event.target.value)} required />
            </div>
          ) : batchAction === "claim" ? (
            <p>The authenticated actor will be assigned by the server. The backend validates assignment and expected version.</p>
          ) : batchAction === "unclaim" ? (
            <div className="field">
              <label htmlFor="group">Return to candidate group</label>
              <select id="group" value={candidateGroup} onChange={(event) => setCandidateGroup(event.target.value)} required disabled={assignmentGroups.isPending || assignmentGroups.isError || !assignmentGroups.data?.length}>
                <option value="">Select an available assignment group</option>
                {(assignmentGroups.data ?? []).map((group) => <option key={group.group_id} value={group.group_id}>{group.group_id}</option>)}
              </select>
              {assignmentGroups.isPending ? <span className="field__hint" role="status">Loading active candidate groups…</span> : null}
              {assignmentGroups.isError ? <span className="field__hint" role="alert">Could not load candidate groups. Retry after refreshing the work queue.</span> : null}
              {assignmentGroups.isSuccess && assignmentGroups.data.length === 0 ? <span className="field__hint" role="status">No active candidate groups are available.</span> : null}
            </div>
          ) : (
            <div className="field-grid">
              <div className="field">
                <label htmlFor="assignee">Assignee ID</label>
                <input id="assignee" value={assignee} onChange={(event) => {
                  setAssignee(event.target.value);
                  if (event.target.value) setCandidateGroup("");
                }} />
              </div>
              <div className="field">
                <label htmlFor="group">Candidate group</label>
                <select id="group" value={candidateGroup} onChange={(event) => {
                  setCandidateGroup(event.target.value);
                  if (event.target.value) setAssignee("");
                }}>
                  <option value="">Select an available assignment group</option>
                  {(assignmentGroups.data ?? []).map((group) => <option key={group.group_id} value={group.group_id}>{group.group_id}</option>)}
                </select>
                {assignmentGroups.isError ? <span className="field__hint" role="status">Could not load candidate groups. You can still delegate to an assignee ID.</span> : null}
                {assignmentGroups.isPending ? <span className="field__hint" role="status">Loading active candidate groups…</span> : null}
              </div>
            </div>
          )}
          {progress ? (
            <div className="batch-progress" role="status">
              <div style={{ width: `${(progress.processed / progress.total) * 100}%` }} />
              <span>{progress.processed} / {progress.total}</span>
            </div>
          ) : null}
        </form>
      </Dialog>
      <Dialog
        open={Boolean(detailId)}
        title="Work item details"
        description={`Live Human Runtime details for ${detailId}`}
        onClose={() => setDetailId("")}
      >
        {detail.isPending ? <LoadingRows /> : null}
        {detail.isError ? <ErrorState message={detail.error.message} retry={() => void detail.refetch()} /> : null}
        {detail.data ? <section className="case-grid" aria-label="Work item fields">
          <article><span>Work item</span><strong>{detail.data.work_item.work_item_id}</strong></article>
          <article><span>Process instance</span><strong>{detail.data.work_item.instance_id}</strong></article>
          <article><span>Workflow</span><strong>{detail.data.work_item.workflow_type} · v{detail.data.work_item.workflow_version}</strong></article>
          <article><span>Task</span><strong>{detail.data.work_item.task_type}</strong><small>{detail.data.work_item.node_id}</small></article>
          <article><span>Assignee</span><strong>{detail.data.work_item.assignee_id || "Unassigned"}</strong></article>
          <article><span>Candidate group</span><strong>{detail.data.work_item.candidate_group || "None"}</strong></article>
          <article><span>Status</span><StatusBadge status={detail.data.work_item.status} /></article>
          <article><span>SLA deadline</span><strong>{formatDeadline(detail.data.work_item.sla_deadline_epoch_ms)}</strong></article>
          <article><span>Concurrency version</span><strong>{detail.data.work_item.version}</strong></article>
          <article><span>Form key</span><strong>{detail.data.work_item.form_key || "None"}</strong></article>
        </section> : null}
        {detail.data?.work_item.form_key ? <section className="work-item-form" aria-label="Task form">
          <header className="portal-section-heading"><div><h2>Task form</h2><p>Published version is loaded from Authoring and revalidated by Gateway at submission.</p></div></header>
          {taskForm.isPending ? <LoadingRows /> : null}
          {taskForm.isError ? <ErrorState message={taskForm.error.message} retry={() => void taskForm.refetch()} /> : null}
          {taskForm.data ? <DynamicTaskForm version={taskForm.data.published_version} values={formValues} onChange={setFormValues} /> : null}
        </section> : null}
        {detail.data ? <div className="work-item-form__submit">
          <label>Decision<select value={detailDecision} onChange={(event) => setDetailDecision(event.target.value)}><option value="APPROVE">Approve</option><option value="REJECT">Reject</option><option value="RETURN">Return</option></select></label>
          <Button icon={CheckCircle2} variant="primary" onClick={() => completeDetail.mutate()} disabled={completeDetail.isPending || !detailDecision.trim() || Boolean(detail.data.work_item.form_key && (taskForm.isPending || !taskForm.data || !formOutputIsValid(taskForm.data.published_version, formValues)))}>{completeDetail.isPending ? "Submitting…" : "Complete task"}</Button>
          {completeDetail.isError ? <p className="form-error" role="alert">{completeDetail.error.message}</p> : null}
        </div> : null}
      </Dialog>
    </>
  );
}

interface FormFieldSchema { type: "string" | "integer" | "boolean" | "array"; title?: string; description?: string; enum?: Array<string | number | boolean>; items?: { type?: "string" | "integer" | "boolean" }; minLength?: number; maxLength?: number; minimum?: number; maximum?: number; minItems?: number; maxItems?: number }
interface TaskFormSchema { type: "object"; properties: Record<string, FormFieldSchema>; required?: string[]; additionalProperties?: boolean }
interface TaskFormUIField { widget?: "default" | "textarea"; width?: "full" | "half"; placeholder?: string; help_text?: string }
interface TaskFormUISchema { order?: string[]; fields?: Record<string, TaskFormUIField> }

function DynamicTaskForm({version,values,onChange}:{version:FormVersion;values:Record<string,WorkflowVariableOutput>;onChange:(next:Record<string,WorkflowVariableOutput>)=>void}) {
  let schema:TaskFormSchema|undefined;
  let ui:TaskFormUISchema|undefined;
  try {
    schema=JSON.parse(version.schema_json) as TaskFormSchema;
    ui=JSON.parse(version.ui_schema_json||"{}") as TaskFormUISchema;
  } catch { schema=undefined;ui=undefined; }
  if (!schema || schema.type!=="object" || !schema.properties || !ui || !isSupportedTaskFormUISchema(schema,ui)) return <p role="alert" className="form-error">The published form definition is invalid and cannot be rendered.</p>;
  const names=[...new Set([...(ui.order??[]),...Object.keys(schema.properties).filter(name=>!(ui.order??[]).includes(name))])];
  if(names.some(name=>!schema?.properties[name])||Object.keys(ui.fields??{}).some(name=>!schema?.properties[name]))return <p role="alert" className="form-error">The published form UI configuration references an unknown field.</p>;
  return <div className="work-item-form__fields">{names.map(name=>{
    const field=schema.properties[name];if(!field)return null;const uiField=ui?.fields?.[name]??{};const value=values[name];const required=(schema?.required??[]).includes(name);const label=field.title||name;const inputPlaceholder=uiField.placeholder;
    const help=uiField.help_text||field.description;
    const className=`work-item-form__field${uiField.width==="full"?" is-full":""}`;
    const helpNode=help?<small>{help}</small>:null;
    if(field.type==="boolean")return <label className={`work-item-form__check ${className}`} key={name}><input type="checkbox" checked={Boolean(value)} onChange={event=>onChange({...values,[name]:event.target.checked})}/><span>{label}{required?" *":""}</span>{helpNode}</label>;
    if(field.type==="array")return <label className={className} key={name}>{label}{required?" *":""}<input value={Array.isArray(value)?value.join(", "):""} placeholder={inputPlaceholder||"Enter comma-separated values"} onChange={event=>{const raw=event.target.value;const tokens=raw===""?[]:raw.split(",").map(item=>item.trim());const valuesParsed=tokens.map(token=>field.items?.type==="integer"?Number(token):field.items?.type==="boolean"?token.toLowerCase()==="true":token);onChange({...values,[name]:valuesParsed})}}/>{helpNode}</label>;
    if(field.enum?.length)return <label className={className} key={name}>{label}{required?" *":""}<select value={String(value??"")} onChange={event=>{const selected=field.enum?.find(item=>String(item)===event.target.value);if(selected!==undefined)onChange({...values,[name]:selected})}}><option value="">Select…</option>{field.enum.map(item=><option key={String(item)} value={String(item)}>{String(item)}</option>)}</select>{helpNode}</label>;
    const setStringValue=(raw:string)=>{if(raw===""){const next={...values};delete next[name];onChange(next)}else onChange({...values,[name]:field.type==="integer"?Number(raw):raw})};
    if(field.type==="string"&&uiField.widget==="textarea")return <label className={className} key={name}>{label}{required?" *":""}<textarea rows={4} maxLength={field.maxLength} minLength={field.minLength} placeholder={inputPlaceholder} value={value===undefined?"":String(value)} onChange={event=>setStringValue(event.target.value)}/>{helpNode}</label>;
    return <label className={className} key={name}>{label}{required?" *":""}<input type={field.type==="integer"?"number":"text"} step={field.type==="integer"?"1":undefined} min={field.minimum} max={field.maximum} minLength={field.minLength} maxLength={field.maxLength} placeholder={inputPlaceholder} value={value===undefined?"":String(value)} onChange={event=>setStringValue(event.target.value)}/>{helpNode}</label>;
  })}</div>;
}

function formOutputIsValid(version:FormVersion,values:Record<string,WorkflowVariableOutput>):boolean{
  try{const schema=JSON.parse(version.schema_json) as TaskFormSchema;const ui=JSON.parse(version.ui_schema_json||"{}") as TaskFormUISchema;if(!isSupportedTaskFormUISchema(schema,ui)||schema.additionalProperties!==false)return false;const keys=Object.keys(schema.properties??{});if(Object.keys(values).some(key=>!keys.includes(key)))return false;for(const name of schema.required??[])if(values[name]===undefined||values[name]==="")return false;for(const [name,value]of Object.entries(values)){const field=schema.properties[name];if(!field)return false;const valid=field.type==="string"?typeof value==="string":field.type==="integer"?typeof value==="number"&&Number.isSafeInteger(value):field.type==="boolean"?typeof value==="boolean":field.type==="array"&&Array.isArray(value)&&value.every(item=>typeof item===field.items?.type);if(!valid)return false}return true}catch{return false}
}

function isSupportedTaskFormUISchema(schema:TaskFormSchema,ui:TaskFormUISchema):boolean{
  if(!ui||typeof ui!=="object"||Array.isArray(ui)||Object.keys(ui).some(key=>key!=="order"&&key!=="fields"))return false;
  if(ui.order!==undefined&&(!Array.isArray(ui.order)||ui.order.some(name=>typeof name!=="string"||!schema.properties[name])||new Set(ui.order).size!==ui.order.length))return false;
  if(ui.fields!==undefined&&(!ui.fields||typeof ui.fields!=="object"||Array.isArray(ui.fields)))return false;
  for(const [name,field]of Object.entries(ui.fields??{})){
    if(!schema.properties[name]||!field||typeof field!=="object"||Array.isArray(field))return false;
    if(Object.keys(field).some(key=>!["widget","width","placeholder","help_text"].includes(key)))return false;
    if(field.widget!==undefined&&field.widget!=="default"&&field.widget!=="textarea")return false;
    if(field.widget==="textarea"&&(schema.properties[name].type!=="string"||schema.properties[name].enum?.length))return false;
    if(field.width!==undefined&&field.width!=="full"&&field.width!=="half")return false;
    if(field.placeholder!==undefined&&(typeof field.placeholder!=="string"||field.placeholder.length>256))return false;
    if(field.help_text!==undefined&&(typeof field.help_text!=="string"||field.help_text.length>512))return false;
  }
  return true;
}

function formatDeadline(epochMs: number): string {
  if (!epochMs) return "None";
  const difference = epochMs - Date.now();
  const minutes = Math.round(Math.abs(difference) / 60_000);
  return difference < 0 ? `${minutes}m overdue` : `in ${minutes}m`;
}
