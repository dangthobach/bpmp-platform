import { useEffect, useRef, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import { Button, Paper, Stack, Typography } from "@ods/shared-ui";
import { commandKey, isRecord, rows, send } from "./api";

type ArtifactKind = "decision" | "form";
const definitions = {
  decision: { title: "DMN decision models", list: "/v1/authoring/decision-models?page_size=50", collection: "/v1/authoring/decision-models", xmlField: "dmn_xml", initialField: "initial_dmn_xml", initial: '<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="decisions"/>' },
  form: { title: "Forms", list: "/v1/authoring/forms?page_size=50", collection: "/v1/authoring/forms", xmlField: "schema_json", initialField: "schema_json", initial: '{\n  "type": "object",\n  "properties": {}\n}' },
} as const;
const emptyUISchema = '{\n  "type": "VerticalLayout",\n  "elements": []\n}';

export function ArtifactWorkspace({ platform, kind }: { platform: PlatformServices; kind: ArtifactKind }): ReactNode {
  const definition = definitions[kind];
  const [artifactId, setArtifactId] = useState("");
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [editor, setEditor] = useState<string>(definition.initial);
  const [uiSchema, setUISchema] = useState(emptyUISchema);
  const [tag, setTag] = useState("");
  const [summary, setSummary] = useState("");
  const [notice, setNotice] = useState("");
  const [failure, setFailure] = useState("");
  const retries = useRef(new Map<string, { fingerprint: string; key: string }>());
  const list = useSWR(["studio-artifacts", kind], async () => send(platform.callApi, "GET", definition.list));
  const detailPath = kind === "decision" ? `${definition.collection}/${encodeURIComponent(artifactId)}` : `${definition.collection}/${encodeURIComponent(artifactId)}`;
  const detail = useSWR(artifactId ? ["studio-artifact", kind, artifactId] : null, async () => send(platform.callApi, "GET", detailPath));
  const versions = useSWR(artifactId ? ["studio-artifact-versions", kind, artifactId] : null, async () => send(platform.callApi, "GET", `${detailPath}/versions?page_size=50`));
  const detailRecord = detail.data?.ok && isRecord(detail.data.data) ? detail.data.data : {};
  const artifact = isRecord(detailRecord[kind === "decision" ? "model" : "form"]) ? detailRecord[kind === "decision" ? "model" : "form"] as Record<string, unknown> : {};
  const draft = isRecord(detailRecord.draft) ? detailRecord.draft : {};
  const expectedRevision = Number(kind === "form" ? artifact.revision : draft.revision);
  const items = list.data?.ok ? rows(list.data.data, kind === "decision" ? "models" : "forms") : [];
  const versionItems = versions.data?.ok ? rows(versions.data.data, "versions") : [];
  useEffect(() => {
    const source = kind === "decision" ? draft.dmn_xml : draft.schema_json;
    if (typeof source === "string") setEditor(source);
    if (kind === "form" && typeof draft.ui_schema_json === "string") setUISchema(draft.ui_schema_json);
    if (typeof artifact.name === "string") setName(artifact.name);
  }, [kind, artifactId, draft.revision, draft.dmn_xml, draft.schema_json, draft.ui_schema_json]);

  async function command(method: "POST" | "PUT", path: string, body: unknown, label: string): Promise<boolean> {
    setNotice(""); setFailure("");
    const fingerprint = JSON.stringify({ method, path, body });
    const old = retries.current.get(path);
    const requestKey = old?.fingerprint === fingerprint ? old.key : commandKey();
    retries.current.set(path, { fingerprint, key: requestKey });
    const result = await send(platform.callApi, method, path, body, requestKey);
    if (!result.ok) {
      if (result.problem.status === 409) retries.current.delete(path);
      setFailure(`${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}, request ${result.requestId}). ${result.problem.status === 409 ? "Reload the latest revision before editing." : "Retry unchanged input to reuse the same command identity."}`);
      if (result.problem.status === 409) { await detail.mutate(); await list.mutate(); }
      return false;
    }
    retries.current.delete(path);
    setNotice(`${label} succeeded (${result.requestId}).`);
    return true;
  }
  async function create(): Promise<void> {
    if (!key.trim() || !name.trim()) return;
    const path = definition.collection;
    const body = kind === "decision"
      ? { model_key: key.trim(), name: name.trim(), initial_dmn_xml: editor }
      : { form_key: key.trim(), name: name.trim(), schema_json: editor, ui_schema_json: uiSchema };
    const fingerprint = JSON.stringify({ method: "POST", path, body });
    const previous = retries.current.get(path);
    const requestKey = previous?.fingerprint === fingerprint ? previous.key : commandKey();
    retries.current.set(path, { fingerprint, key: requestKey });
    const result = await send<Record<string, unknown>>(platform.callApi, "POST", path, body, requestKey);
    if (!result.ok) { if (result.problem.status === 409) retries.current.delete(path); setFailure(`${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}).`); return; }
    retries.current.delete(path);
    const record = isRecord(result.data[kind === "decision" ? "model" : "form"]) ? result.data[kind === "decision" ? "model" : "form"] as Record<string, unknown> : {};
    if (typeof record.id === "string") setArtifactId(record.id);
    setNotice(`${kind === "decision" ? "Decision model" : "Form"} created (${result.requestId}).`);
    await list.mutate();
  }
  async function save(): Promise<void> {
    const revision = expectedRevision;
    if (!artifactId || !Number.isSafeInteger(revision) || revision < 1) return;
    if (kind === "form") {
      try { JSON.parse(editor); JSON.parse(uiSchema); } catch { setFailure("Schema and UI schema must both be valid JSON before saving."); return; }
    }
    const path = `${detailPath}/draft`;
    const body = kind === "decision"
      ? { expected_revision: revision, dmn_xml: editor }
      : { expected_revision: revision, name: name.trim() || String(artifact.name ?? ""), schema_json: editor, ui_schema_json: uiSchema };
    if (await command("PUT", path, body, "Draft save")) await Promise.all([detail.mutate(), list.mutate()]);
  }
  async function validate(): Promise<void> {
    const revision = expectedRevision;
    if (!artifactId || !Number.isSafeInteger(revision) || revision < 1) return;
    if (kind === "form") { try { JSON.parse(editor); JSON.parse(uiSchema); } catch { setFailure("Schema and UI schema must both be valid JSON before validation."); return; } }
    if (await command("POST", `${detailPath}/draft/validate`, { expected_revision: revision }, "Draft validation")) await detail.mutate();
  }
  async function checkin(): Promise<void> {
    const revision = expectedRevision;
    if (!artifactId || !Number.isSafeInteger(revision) || revision < 1 || !tag.trim() || !summary.trim()) return;
    const digestText = kind === "decision" ? editor : `${editor}\n${uiSchema}`;
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(digestText))), (byte) => byte.toString(16).padStart(2, "0")).join("");
    const body = { expected_revision: revision, revision_digest: digest, version_tag: tag.trim(), summary: summary.trim() };
    if (await command("POST", `${detailPath}/versions`, body, kind === "decision" ? "Version check-in" : "Form publish")) { setTag(""); setSummary(""); await Promise.all([detail.mutate(), versions.mutate(), list.mutate()]); }
  }

  return <Paper sx={{ p: 2, mt: 2 }}>
    <Typography variant="h2">{definition.title}</Typography>
    <Typography color="text.secondary">Owner validation is authoritative. Draft writes use expected revisions; versions are immutable after check-in/publish.</Typography>
    <Stack direction="row" spacing={1} sx={{ my: 1 }}><input aria-label={`${kind} key`} placeholder={kind === "form" ? "Form key" : "Decision key"} value={key} onChange={(event) => setKey(event.target.value)} /><input aria-label={`${kind} name`} placeholder="Name" value={name} onChange={(event) => setName(event.target.value)} /><Button type="button" onClick={() => void create()}>Create {kind === "form" ? "form" : "decision model"}</Button></Stack>
    {list.isLoading ? <p>Loading…</p> : null}{list.data && !list.data.ok ? <p role="alert">List request failed: {list.data.problem.code}</p> : null}
    <ul>{items.map((item, index) => <li key={String(item.id ?? index)}><Button type="button" onClick={() => setArtifactId(String(item.id ?? ""))}>{String(item.name ?? item.form_key ?? item.model_key ?? item.id)}</Button> · {String(item.id ?? "")}</li>)}</ul>
    {artifactId ? <>
      <Typography variant="h3">Draft {String(artifact.name ?? artifact.model_key ?? artifact.form_key ?? artifactId)} · revision {String(expectedRevision || "—")}</Typography>
      {kind === "form" ? <><label>Form name<input aria-label="Artifact display name" value={name} onChange={(event) => setName(event.target.value)} /></label><label>JSON Schema<textarea aria-label="Form schema JSON" rows={10} value={editor} onChange={(event) => setEditor(event.target.value)} /></label><label>UI schema JSON<textarea aria-label="Form UI schema JSON" rows={6} value={uiSchema} onChange={(event) => setUISchema(event.target.value)} /></label></> : <label>DMN XML<textarea aria-label="DMN XML draft" rows={12} value={editor} onChange={(event) => setEditor(event.target.value)} /></label>}
      <Stack direction="row" spacing={1}><Button type="button" disabled={!expectedRevision} onClick={() => void save()}>Save draft</Button><Button type="button" disabled={!expectedRevision} onClick={() => void validate()}>Validate draft</Button><input aria-label="Artifact version tag" placeholder="Version tag" value={tag} onChange={(event) => setTag(event.target.value)} /><input aria-label="Artifact version summary" placeholder="Summary" value={summary} onChange={(event) => setSummary(event.target.value)} /><Button type="button" disabled={draft.validation_status !== "VALID" || !tag.trim() || !summary.trim()} onClick={() => void checkin()}>{kind === "form" ? "Publish form version" : "Check in decision version"}</Button></Stack>
      {Array.isArray(draft.diagnostics) ? <pre>{JSON.stringify(draft.diagnostics, null, 2)}</pre> : null}
      <Typography variant="h3">Immutable versions</Typography><pre>{JSON.stringify(versionItems, null, 2)}</pre>
    </> : null}
    {failure ? <p role="alert">{failure}</p> : null}{notice ? <p role="status">{notice}</p> : null}
  </Paper>;
}
