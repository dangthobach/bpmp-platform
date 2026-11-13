import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Code2, Plus, RefreshCw, Save, Search, ShieldAlert, Sparkles, Tag } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useApi } from "../../api/ApiContext";
import { ApiError } from "../../api/client";
import type { CheckinProcessModelInput, CreateProcessModelInput, ProcessModel, SaveProcessModelDraftInput, ValidateProcessModelDraftInput } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { useRuntimeConfig } from "../../config/ConfigContext";
import { BpmnCanvas } from "./VisualBpmnCanvas";
import { DecisionModeler } from "./DecisionModeler";
import { SourceFileActions } from "./SourceFileActions";
import { VersionHistoryPanel } from "./VersionHistoryPanel";
import { FormModeler } from "./FormModeler";

const initialBpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="new-process">
    <bpmn:startEvent id="start" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="start-to-end" sourceRef="start" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`;

export function ModelerPage() {
  const api = useApi();
  const queryClient = useQueryClient();
  const { identity } = useAuth();
  const config = useRuntimeConfig();
  const [search, setSearch] = useState("");
  const [activeMode, setActiveMode] = useState<"bpmn" | "dmn" | "forms">("bpmn");
  const [selectedId, setSelectedId] = useState("");
  const [bpmnXml, setBpmnXml] = useState("");
  const [baseVersionId, setBaseVersionId] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [checkinOpen, setCheckinOpen] = useState(false);
  const [versionTag, setVersionTag] = useState("");
  const [versionSummary, setVersionSummary] = useState("");
  const [form, setForm] = useState<CreateProcessModelInput>({ model_key: "", name: "", category: "", description: "", initial_bpmn_xml: initialBpmn });
  const retryCreate = useRef<{ input: CreateProcessModelInput; idempotencyKey: string } | null>(null);
  const retrySave = useRef<{ input: SaveProcessModelDraftInput; idempotencyKey: string } | null>(null);
  const retryValidate = useRef<{ input: ValidateProcessModelDraftInput; idempotencyKey: string } | null>(null);
  const retryCheckin = useRef<{ input: CheckinProcessModelInput; idempotencyKey: string } | null>(null);

  const models = useQuery({ queryKey: ["process-models", identity?.tenantId, search], queryFn: () => api.listProcessModels(search, "", config.defaultPageSize), enabled: Boolean(identity), staleTime: config.staleTimeMs });
  const detail = useQuery({ queryKey: ["process-model", identity?.tenantId, selectedId], queryFn: () => api.getProcessModel(selectedId), enabled: Boolean(identity && selectedId), staleTime: config.staleTimeMs });

  useEffect(() => {
    if (detail.data) { setBpmnXml(detail.data.draft.bpmn_xml); setBaseVersionId(detail.data.draft.base_version_id); }
  }, [detail.data?.draft.process_model_id, detail.data?.draft.revision]);

  const create = useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: CreateProcessModelInput; idempotencyKey: string }) => api.createProcessModel(input, idempotencyKey),
    onSuccess: async (result) => {
      retryCreate.current = null;
      setCreateOpen(false);
      setSelectedId(result.model.id);
      await queryClient.invalidateQueries({ queryKey: ["process-models", identity?.tenantId] });
    },
  });
  const save = useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: SaveProcessModelDraftInput; idempotencyKey: string }) => api.saveProcessModelDraft(selectedId, input, idempotencyKey),
    onSuccess: async () => {
      retrySave.current = null;
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["process-model", identity?.tenantId, selectedId] }),
        queryClient.invalidateQueries({ queryKey: ["process-models", identity?.tenantId] }),
      ]);
    },
  });
  const validate = useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: ValidateProcessModelDraftInput; idempotencyKey: string }) => api.validateProcessModelDraft(selectedId, input, idempotencyKey),
    onSuccess: async () => {
      retryValidate.current = null;
      await queryClient.invalidateQueries({ queryKey: ["process-model", identity?.tenantId, selectedId] });
    },
  });
  const checkin = useMutation({
    mutationFn: ({ input, idempotencyKey }: { input: CheckinProcessModelInput; idempotencyKey: string }) => api.checkinProcessModel(selectedId, input, idempotencyKey),
    onSuccess: async () => {
      retryCheckin.current = null;
      setCheckinOpen(false);
      setVersionTag("");
      setVersionSummary("");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["process-model", identity?.tenantId, selectedId] }),
        queryClient.invalidateQueries({ queryKey: ["process-models", identity?.tenantId] }),
        queryClient.invalidateQueries({ queryKey: ["authoring-version-history", "bpmn", identity?.tenantId, selectedId] }),
      ]);
    },
  });

  const unavailable = (models.error instanceof ApiError && (models.error.status === 404 || models.error.status === 503));
  const currentModel = detail.data?.model;
  const currentDraft = detail.data?.draft;
  const canSave = Boolean(currentDraft && bpmnXml.length > 0 && bpmnXml.length <= 1_048_576 && (bpmnXml !== currentDraft.bpmn_xml || baseVersionId !== currentDraft.base_version_id));
  const latestVersionSourceRevision = checkin.data && checkin.data.version.process_model_id === currentModel?.id
    ? checkin.data.version.source_revision
    : currentModel?.latest_version_source_revision;
  const canCheckin = Boolean(currentDraft?.validation_status === "VALID" && bpmnXml === currentDraft.bpmn_xml && baseVersionId === currentDraft.base_version_id && latestVersionSourceRevision !== currentDraft.revision);

  return <>
    <header className="page-header">
      <div><h1>Process Modeler</h1><p>Manage process models and BPMN drafts. Check-in records a validated immutable version; deployment and activation are separate.</p></div>
      <div className="header-actions">
        <Button onClick={() => setActiveMode("bpmn")} disabled={activeMode === "bpmn"}>BPMN</Button>
        <Button onClick={() => setActiveMode("dmn")} disabled={activeMode === "dmn"}>DMN</Button>
        <Button onClick={() => setActiveMode("forms")} disabled={activeMode === "forms"}>Forms</Button>
        {activeMode === "bpmn" ? <>
        <Button icon={RefreshCw} onClick={() => { void models.refetch(); if (selectedId) void detail.refetch(); }} disabled={models.isFetching || detail.isFetching}>Refresh</Button>
        <Button icon={Plus} variant="primary" onClick={() => { setCreateOpen((value) => !value); create.reset(); }}>New process model</Button>
        </> : null}
      </div>
    </header>

    {activeMode === "dmn" ? <DecisionModeler /> : activeMode === "forms" ? <FormModeler /> : <>

    {createOpen ? <section className="table-surface modeler-create">
      <header className="portal-section-heading"><div><h2>Create process model</h2><p>Creates a tenant-owned model with its first draft. The workflow is not activated.</p></div></header>
      <form className="configuration-form" onSubmit={(event) => { event.preventDefault(); const intent = { input: { ...form }, idempotencyKey: crypto.randomUUID() }; retryCreate.current = intent; create.mutate(intent); }}>
        <label>Model key<input required maxLength={256} value={form.model_key} onChange={(event) => setForm({ ...form, model_key: event.target.value })} /></label>
        <label>Name<input required maxLength={512} value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
        <label>Category<input maxLength={256} value={form.category} onChange={(event) => setForm({ ...form, category: event.target.value })} /></label>
        <label>Description<input maxLength={4096} value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} /></label>
        <label className="modeler-create__xml">Initial BPMN XML<textarea required maxLength={1_048_576} rows={8} spellCheck={false} value={form.initial_bpmn_xml} onChange={(event) => setForm({ ...form, initial_bpmn_xml: event.target.value })} /></label>
        <div className="configuration-form__actions"><Button type="submit" icon={Plus} disabled={create.isPending || !form.model_key.trim() || !form.name.trim() || form.initial_bpmn_xml.length === 0}>{create.isPending ? "Creating…" : "Create draft"}</Button><Button type="button" onClick={() => { setCreateOpen(false); create.reset(); retryCreate.current = null; }}>Cancel</Button></div>
      </form>
      {create.isError ? <ErrorState message={create.error.message} retry={() => { if (retryCreate.current) create.mutate(retryCreate.current); }} /> : null}
    </section> : null}

    {unavailable ? <section className="capability-notice" role="status"><ShieldAlert size={18} /><div><strong>Authoring service is not connected</strong><p>The Modeler API is not configured in this Gateway. No local sample or simulated save is shown.</p></div></section> : null}

    <div className="modeler-workspace">
      <section className="table-surface modeler-catalog">
        <header className="portal-section-heading"><div><h2>Process models</h2><p>{models.data?.models.length ?? 0} loaded</p></div></header>
        <label className="modeler-search"><Search size={15} /><input aria-label="Search process models" placeholder="Search by name or key" value={search} onChange={(event) => setSearch(event.target.value)} /></label>
        {models.isPending ? <LoadingRows /> : null}
        {models.isError && !unavailable ? <ErrorState message={models.error.message} retry={() => void models.refetch()} /> : null}
        {models.isSuccess && models.data.models.length === 0 ? <EmptyState title="No process models" detail="Create a model to start a tenant-owned BPMN draft." /> : null}
        {models.data?.models.map((model: ProcessModel) => <button type="button" key={model.id} className={`modeler-model ${selectedId === model.id ? "is-selected" : ""}`} onClick={() => { setSelectedId(model.id); save.reset(); validate.reset(); checkin.reset(); setCheckinOpen(false); }}>
          <span className="modeler-model__icon"><Code2 size={16} /></span><span className="modeler-model__content"><strong>{model.name}</strong><small>{model.model_key} · {model.category || "Uncategorized"}</small></span><span className="modeler-model__status">{model.status}</span>
        </button>)}
      </section>

      <section className="table-surface modeler-editor">
        {!selectedId ? <EmptyState title="Select a process model" detail="Choose a model to inspect and edit its current BPMN draft." /> : null}
        {selectedId && detail.isPending ? <LoadingRows /> : null}
        {selectedId && detail.isError ? <ErrorState message={detail.error.message} retry={() => void detail.refetch()} /> : null}
        {currentModel && currentDraft ? <>
          <header className="portal-section-heading modeler-editor__heading"><div><h2>{currentModel.name}</h2><p><span className="mono">{currentModel.model_key}</span> · Draft revision {currentDraft.revision} · {currentDraft.validation_status}</p></div><div className="modeler-editor__actions">
            <SourceFileActions source={bpmnXml} format="bpmn" fileName={currentModel.model_key} onImport={setBpmnXml} />
            <Button icon={Check} disabled={!currentDraft || save.isPending || validate.isPending || !bpmnXml} onClick={() => { const intent = { input: { expected_revision: currentDraft.revision }, idempotencyKey: crypto.randomUUID() }; retryValidate.current = intent; validate.mutate(intent); }}>{validate.isPending ? "Validating…" : "Validate saved draft"}</Button>
            <Button icon={Save} variant="primary" disabled={!canSave || save.isPending} onClick={() => { const intent = { input: { expected_revision: currentDraft.revision, base_version_id: baseVersionId, bpmn_xml: bpmnXml }, idempotencyKey: crypto.randomUUID() }; retrySave.current = intent; save.mutate(intent); }}>{save.isPending ? "Saving…" : "Save draft"}</Button>
            <Button icon={Tag} disabled={!canCheckin || checkin.isPending} onClick={() => setCheckinOpen((open) => !open)}>Check in version</Button>
          </div></header>
          {checkinOpen ? <form className="configuration-form modeler-checkin" onSubmit={async (event) => {
            event.preventDefault();
            if (!currentDraft || !canCheckin) return;
            const revisionDigest = await digestHex(currentDraft.bpmn_xml);
            const intent = { input: { expected_revision: currentDraft.revision, revision_digest: revisionDigest, version_tag: versionTag.trim(), summary: versionSummary.trim() }, idempotencyKey: crypto.randomUUID() };
            retryCheckin.current = intent;
            checkin.mutate(intent);
          }}>
            <label>Version tag<input required maxLength={128} value={versionTag} onChange={(event) => setVersionTag(event.target.value)} placeholder="e.g. release-1" /></label>
            <label>Summary<input required maxLength={1000} value={versionSummary} onChange={(event) => setVersionSummary(event.target.value)} placeholder="Describe this immutable version" /></label>
            <div className="configuration-form__actions"><Button type="submit" icon={Tag} disabled={!canCheckin || checkin.isPending || !versionTag.trim() || !versionSummary.trim()}>{checkin.isPending ? "Checking in…" : "Create immutable version"}</Button><Button type="button" onClick={() => setCheckinOpen(false)}>Cancel</Button></div>
            <p className="field__hint">Only a persisted, compiler-validated draft can be checked in. This records a version; it does not deploy or activate it.</p>
          </form> : null}
          <div className="modeler-editor__meta"><span><strong>Status</strong>{currentModel.status}</span><span><strong>Updated</strong>{new Date(currentDraft.updated_at_epoch_ms).toLocaleString()}</span><span><strong>Draft size</strong>{new TextEncoder().encode(bpmnXml).length.toLocaleString()} bytes / 1 MiB</span></div>
          <BpmnCanvas source={bpmnXml} onChange={setBpmnXml} />
          <label className="modeler-source"><span><Code2 size={15} /> BPMN 2.0 source</span><textarea aria-label="BPMN draft source" spellCheck={false} maxLength={1_048_576} value={bpmnXml} onChange={(event) => setBpmnXml(event.target.value)} /></label>
          {save.isError ? <ErrorState message={save.error.message} retry={() => { if (retrySave.current) save.mutate(retrySave.current); }} /> : null}
          {save.isSuccess ? <p role="status" className="success-message">Draft saved at revision {save.data.draft.revision}{save.data.replayed ? " (idempotent retry)" : ""}. Save again before validating edits.</p> : null}
          {validate.isError ? <ErrorState message={validate.error.message} retry={() => { if (retryValidate.current) validate.mutate(retryValidate.current); }} /> : null}
          {validate.data ? <section className={`modeler-diagnostics ${validate.data.valid ? "is-valid" : "has-errors"}`}><h3><Sparkles size={16} /> Revision {validate.data.revision}: {validate.data.valid ? "Valid" : "Needs changes"}</h3>{validate.data.diagnostics.length ? <ul>{validate.data.diagnostics.map((item, index) => <li key={`${item.code}-${item.line}-${index}`}><strong>{item.code}</strong> {item.message} <span>Line {item.line}, column {item.column}</span></li>)}</ul> : <p>No compiler diagnostics.</p>}</section> : null}
          {checkin.isError ? <ErrorState message={checkin.error.message} retry={() => { if (retryCheckin.current) checkin.mutate(retryCheckin.current); }} /> : null}
          {checkin.data ? <p role="status" className="success-message">Version {checkin.data.version.version_num} ({checkin.data.version.version_tag}) checked in from draft revision {checkin.data.version.source_revision}{checkin.data.replayed ? " (idempotent retry)" : ""}. Deployment and activation remain separate.</p> : null}
        </> : null}
      </section>
    </div>
    {currentModel && identity ? <VersionHistoryPanel key={`${currentModel.id}-bpmn-${currentModel.latest_version_source_revision}`} tenantId={identity.tenantId} modelId={currentModel.id} modelKey={currentModel.model_key} format="bpmn"
      listVersions={async (token) => api.listProcessModelVersions(currentModel.id, token, config.defaultPageSize)}
      getVersion={async (versionId) => { const version = await api.getProcessModelVersion(currentModel.id, versionId); return { ...version, source: version.bpmn_xml }; }}
      hasUnsavedEditorChanges={Boolean(currentDraft && bpmnXml!==currentDraft.bpmn_xml)} onLoadIntoEditor={(source, versionId) => { setBpmnXml(source); setBaseVersionId(versionId); }} /> : null}
    </>}
  </>;
}

async function digestHex(source: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(source));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
