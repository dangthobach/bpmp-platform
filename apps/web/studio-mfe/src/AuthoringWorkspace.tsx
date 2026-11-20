import { useEffect, useRef, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import { Button, Paper, Stack, Typography } from "@ods/shared-ui";
import { commandKey, isRecord, rows, send } from "./api";
import { ArtifactWorkspace } from "./ArtifactWorkspace";

const starterBpmn = '<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="new-process"><bpmn:startEvent id="start"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="start-to-end" sourceRef="start" targetRef="end"/></bpmn:process></bpmn:definitions>';
type Notice = { kind: "status" | "alert"; text: string };

export function AuthoringWorkspace({ platform }: { platform: PlatformServices }): ReactNode {
  const [artifact, setArtifact] = useState<"process" | "decision" | "form">("process");
  return <>
    <Stack direction="row" spacing={1} sx={{ mt: 1 }}>
      <Button type="button" variant={artifact === "process" ? "contained" : "outlined"} onClick={() => setArtifact("process")}>BPMN process</Button>
      <Button type="button" variant={artifact === "decision" ? "contained" : "outlined"} onClick={() => setArtifact("decision")}>DMN decision</Button>
      <Button type="button" variant={artifact === "form" ? "contained" : "outlined"} onClick={() => setArtifact("form")}>Forms</Button>
    </Stack>
    {artifact === "process" ? <ProcessWorkspace platform={platform} /> : null}
    {artifact === "decision" ? <ArtifactWorkspace platform={platform} kind="decision" /> : null}
    {artifact === "form" ? <ArtifactWorkspace platform={platform} kind="form" /> : null}
  </>;
}

function ProcessWorkspace({ platform }: { platform: PlatformServices }): ReactNode {
  const [modelId, setModelId] = useState("");
  const [xml, setXml] = useState(starterBpmn);
  const [modelKey, setModelKey] = useState("");
  const [modelName, setModelName] = useState("");
  const [versionTag, setVersionTag] = useState("");
  const [summary, setSummary] = useState("");
  const [versionId, setVersionId] = useState("");
  const [deploymentId, setDeploymentId] = useState("");
  const [environment, setEnvironment] = useState("stage");
  const [notice, setNotice] = useState<Notice | null>(null);
  const retry = useRef(new Map<string, { digest: string; key: string }>());
  const models = useSWR(["studio-process-models"], async () => send(platform.callApi, "GET", "/v1/authoring/process-models?page_size=50"));
  const detail = useSWR(modelId ? ["studio-process-model", modelId] : null, async () => send(platform.callApi, "GET", `/v1/authoring/process-models/${encodeURIComponent(modelId)}`));
  const versions = useSWR(modelId ? ["studio-process-versions", modelId] : null, async () => send(platform.callApi, "GET", `/v1/authoring/process-models/${encodeURIComponent(modelId)}/versions?page_size=50`));
  const deployments = useSWR(modelId ? ["studio-process-deployments", modelId] : null, async () => send(platform.callApi, "GET", `/v1/authoring/process-models/${encodeURIComponent(modelId)}/deployments?page_size=50`));
  const deployment = useSWR(deploymentId ? ["studio-deployment", deploymentId] : null, async () => send(platform.callApi, "GET", `/v1/authoring/deployments/${encodeURIComponent(deploymentId)}`), {
    refreshInterval: (latest) => {
      if (!latest?.ok || !isRecord(latest.data)) return latest ? 0 : 2_000;
      const value = isRecord(latest.data.deployment) ? latest.data.deployment : latest.data;
      return ["ACTIVE", "FAILED", "ROLLED_BACK", "SUPERSEDED", "CANCELLED"].includes(String(value.status)) ? 0 : 2_000;
    },
  });
  const detailData = detail.data?.ok ? detail.data.data : undefined;
  const model = isRecord(detailData) && isRecord(detailData.model) ? detailData.model : {};
  const draft = isRecord(detailData) && isRecord(detailData.draft) ? detailData.draft : {};
  useEffect(() => { if (typeof draft.bpmn_xml === "string") setXml(draft.bpmn_xml); }, [modelId, draft.revision, draft.bpmn_xml]);

  async function run(name: string, method: "POST" | "PUT", path: string, body: unknown, stableKey?: string): Promise<boolean> {
    const fingerprint = JSON.stringify({ method, path, body });
    const previous = retry.current.get(path);
    const key = stableKey ?? (previous?.digest === fingerprint ? previous.key : commandKey());
    retry.current.set(path, { digest: fingerprint, key });
    const result = await send(platform.callApi, method, path, body, key);
    if (!result.ok) {
      setNotice({ kind: "alert", text: `${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}, request ${result.requestId}). ${result.problem.status === 409 ? "Reload the current revision before continuing." : "For uncertain timeouts, reconcile the latest resource state before retrying."}` });
      if (result.problem.status === 409) { retry.current.delete(path); await detail.mutate(); await models.mutate(); }
      return false;
    }
    retry.current.delete(path);
    setNotice({ kind: "status", text: `${name === "Deployment" ? "Deployment command accepted; activation is asynchronous" : `${name} succeeded`} (${result.requestId}).` });
    return true;
  }
  async function createModel(): Promise<void> {
    if (!modelKey.trim() || !modelName.trim()) return;
    const path = "/v1/authoring/process-models";
    const body = { model_key: modelKey.trim(), name: modelName.trim(), initial_bpmn_xml: xml };
    const fingerprint = JSON.stringify({ method: "POST", path, body });
    const previous = retry.current.get(path);
    const key = previous?.digest === fingerprint ? previous.key : commandKey();
    retry.current.set(path, { digest: fingerprint, key });
    const result = await send<{ model?: { id?: string } }>(platform.callApi, "POST", path, body, key);
    if (!result.ok) { setNotice({ kind: "alert", text: `${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}). ${result.problem.status === 409 ? "Reload before creating another model." : "Retrying unchanged input reuses the same command identity."}` }); if (result.problem.status === 409) retry.current.delete(path); return; }
    retry.current.delete(path);
    const createdId = result.data.model?.id;
    if (createdId) setModelId(createdId);
    setNotice({ kind: "status", text: `Process model created (${result.requestId}).` });
    await models.mutate();
  }
  async function saveDraft(): Promise<void> {
    const revision = Number(draft.revision);
    if (!modelId || !Number.isSafeInteger(revision) || revision < 1) return;
    const body = { expected_revision: revision, bpmn_xml: xml, ...(typeof draft.base_version_id === "string" && draft.base_version_id ? { base_version_id: draft.base_version_id } : {}) };
    const fingerprint = JSON.stringify(body);
    const previous = retry.current.get("save");
    const key = previous?.digest === fingerprint ? previous.key : commandKey();
    retry.current.set("save", { digest: fingerprint, key });
    if (await run("Draft save", "PUT", `/v1/authoring/process-models/${encodeURIComponent(modelId)}/draft`, body, key)) { retry.current.delete("save"); await detail.mutate(); await models.mutate(); }
  }
  async function validateDraft(): Promise<void> {
    if (await run("Validation", "POST", `/v1/authoring/process-models/${encodeURIComponent(modelId)}/draft/validate`, { expected_revision: Number(draft.revision) })) await detail.mutate();
  }
  async function checkin(): Promise<void> {
    const revision = Number(draft.revision);
    if (!modelId || !Number.isSafeInteger(revision) || !versionTag.trim() || !summary.trim()) return;
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(xml))), (byte) => byte.toString(16).padStart(2, "0")).join("");
    const body = { expected_revision: revision, revision_digest: digest, version_tag: versionTag.trim(), summary: summary.trim() };
    if (await run("Version check-in", "POST", `/v1/authoring/process-models/${encodeURIComponent(modelId)}/versions`, body)) { setVersionTag(""); setSummary(""); await Promise.all([detail.mutate(), versions.mutate(), models.mutate()]); }
  }
  async function deploy(): Promise<void> {
    if (!modelId || !versionId || !environment.trim()) return;
    if (await run("Deployment", "POST", `/v1/authoring/process-models/${encodeURIComponent(modelId)}/deployments`, { process_model_version_id: versionId, target_environment: environment.trim() })) await deployments.mutate();
  }
  async function rollback(): Promise<void> {
    if (!deploymentId) return;
    if (await run("Rollback", "POST", `/v1/authoring/deployments/${encodeURIComponent(deploymentId)}/rollback`, {})) await Promise.all([deployment.mutate(), deployments.mutate()]);
  }
  const modelRows = models.data?.ok ? rows(models.data.data, "models") : [];
  const versionRows = versions.data?.ok ? rows(versions.data.data, "versions") : [];
  const deploymentRows = deployments.data?.ok ? rows(deployments.data.data, "deployments") : [];
  return <Paper sx={{ p: 2, mt: 2 }}>
    <Typography variant="h2">Authoring & deployment</Typography>
    <Typography color="text.secondary">Draft edits use expected revisions. Only immutable checked-in versions can be deployed.</Typography>
    <Stack direction="row" spacing={1} sx={{ my: 1 }}><input aria-label="New model key" placeholder="Model key" value={modelKey} onChange={(event) => setModelKey(event.target.value)} /><input aria-label="New model name" placeholder="Name" value={modelName} onChange={(event) => setModelName(event.target.value)} /><Button type="button" onClick={() => void createModel()}>Create process model</Button></Stack>
    {models.isLoading ? <p>Loading models…</p> : null}{models.data && !models.data.ok ? <p role="alert">Could not load process models: {models.data.problem.code}</p> : null}
    <ul>{modelRows.map((item, index) => <li key={String(item.id ?? index)}><Button type="button" onClick={() => { setModelId(String(item.id ?? "")); setNotice(null); }}>{String(item.name ?? item.model_key ?? item.id)}</Button> — {String(item.id ?? "")}</li>)}</ul>
    {modelId ? <>
      <Typography variant="h3">Draft {String(model.model_key ?? modelId)} · revision {String(draft.revision ?? "—")}</Typography>
      {detail.isLoading ? <p>Loading draft…</p> : null}{detail.data && !detail.data.ok ? <p role="alert">Draft read failed: {detail.data.problem.code}</p> : null}
      <label>BPMN XML<textarea aria-label="BPMN XML draft" rows={12} value={xml} onChange={(event) => setXml(event.target.value)} /></label>
      <Stack direction="row" spacing={1}><Button type="button" disabled={!draft.revision} onClick={() => void saveDraft()}>Save draft</Button><Button type="button" disabled={!draft.revision} onClick={() => void validateDraft()}>Validate draft</Button><input aria-label="Version tag" placeholder="Version tag" value={versionTag} onChange={(event) => setVersionTag(event.target.value)} /><input aria-label="Version summary" placeholder="Version summary" value={summary} onChange={(event) => setSummary(event.target.value)} /><Button type="button" disabled={draft.validation_status !== "VALID" || !versionTag.trim() || !summary.trim()} onClick={() => void checkin()}>Check in version</Button></Stack>
      {isRecord(draft) && Array.isArray(draft.diagnostics) ? <pre>{JSON.stringify(draft.diagnostics, null, 2)}</pre> : null}
      <Typography variant="h3">Immutable versions</Typography><ul>{versionRows.map((v, index) => <li key={String(v.id ?? index)}>{String(v.version_tag ?? v.version_num ?? "Version")} · {String(v.id ?? "")} <Button type="button" onClick={() => setVersionId(String(v.id ?? ""))}>Select for deploy</Button></li>)}</ul>
      <Stack direction="row" spacing={1}><input aria-label="Target environment" value={environment} onChange={(event) => setEnvironment(event.target.value)} /><Button type="button" disabled={!versionId} onClick={() => void deploy()}>Deploy selected version</Button></Stack>
      <Typography variant="h3">Deployment history</Typography><ul>{deploymentRows.map((d, index) => <li key={String(d.id ?? index)}>{String(d.status ?? "unknown")} · {String(d.id ?? "")} <Button type="button" onClick={() => setDeploymentId(String(d.id ?? ""))}>Inspect deployment</Button></li>)}</ul>
      {deploymentId ? <section aria-label="Deployment detail"><Button type="button" onClick={() => void deployment.mutate()}>Refresh deployment status</Button>{deployment.data?.ok ? <pre>{JSON.stringify(deployment.data.data, null, 2)}</pre> : null}{deployment.data && !deployment.data.ok ? <p role="alert">Deployment detail failed: {deployment.data.problem.code}</p> : null}<Button type="button" onClick={() => void rollback()}>Rollback this deployment</Button></section> : null}
    </> : null}
    {notice ? <p role={notice.kind}>{notice.text}</p> : null}
  </Paper>;
}
