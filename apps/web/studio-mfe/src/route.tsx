import { useState, type ReactNode } from "react";
import useSWR from "swr";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Button, LoadingPanel, Page, Paper, Stack, Typography } from "@ods/shared-ui";

type Area = "authoring" | "access" | "configuration";
type ApiEnvelope = { ok: true; data: unknown } | { ok: false; problem: { title: string; detail?: string; code: string; status: number } };
const paths: Record<Area, string> = {
  authoring: "/v1/authoring/process-models?page_size=50",
  access: "/v1/access/catalog/versions?page_size=50",
  configuration: "/v1/configuration/profiles?page_size=50",
};
const labels: Record<Area, string> = { authoring: "Authoring & deployment", access: "Access", configuration: "Configuration" };

function StudioRoute({ platform }: RemoteRouteProps): ReactNode {
  const [area, setArea] = useState<Area>("authoring");
  const [modelId, setModelId] = useState("");
  const [versionId, setVersionId] = useState("");
  const [environment, setEnvironment] = useState("stage");
  const [commandMessage, setCommandMessage] = useState("");
  const { data, error, isLoading, mutate } = useSWR<ApiEnvelope>(["studio", area], async () => {
    const response = await platform.callApi<unknown>({ method: "GET", path: paths[area] });
    return response.ok ? { ok: true, data: response.data } : { ok: false, problem: response.problem };
  });
  const value = data?.ok ? data.data : undefined;
  const rows = asRows(value);
  const versions = useSWR<ApiEnvelope>(modelId ? ["studio-model-versions", modelId] : null, async () => {
    const response = await platform.callApi<unknown>({ method: "GET", path: `/v1/authoring/process-models/${encodeURIComponent(modelId)}/versions?page_size=50` });
    return response.ok ? { ok: true, data: response.data } : { ok: false, problem: response.problem };
  });
  const deployments = useSWR<ApiEnvelope>(modelId && area === "authoring" ? ["studio-deployments", modelId] : null, async () => {
    const response = await platform.callApi<unknown>({ method: "GET", path: `/v1/authoring/process-models/${encodeURIComponent(modelId)}/deployments?page_size=50` });
    return response.ok ? { ok: true, data: response.data } : { ok: false, problem: response.problem };
  });
  async function deploy(): Promise<void> {
    if (!modelId || !versionId.trim() || !environment.trim()) return;
    const response = await platform.callApi<unknown, { process_model_version_id: string; target_environment: string }>({
      method: "POST", path: `/v1/authoring/process-models/${encodeURIComponent(modelId)}/deployments`,
      body: { process_model_version_id: versionId.trim(), target_environment: environment.trim() },
      idempotencyKey: crypto.randomUUID().replaceAll("-", ""),
    });
    if (!response.ok) {
      setCommandMessage(`${response.problem.title} (${response.problem.code}, HTTP ${response.problem.status}). Review the deployment list before retrying.`);
      return;
    }
    setCommandMessage(`Deployment command accepted (${response.requestId}); activation is asynchronous.`);
    await deployments.mutate();
  }
  return <Page title="BPMP Studio">
    <Stack direction="row" spacing={1}>
      {(Object.keys(labels) as Area[]).map((item) => <Button key={item} type="button" variant={area === item ? "contained" : "outlined"} onClick={() => setArea(item)}>{labels[item]}</Button>)}
      <Button type="button" onClick={() => void mutate()}>Refresh</Button>
    </Stack>
    <Paper sx={{ p: 2, mt: 2 }}>
      <Typography variant="h2">{labels[area]}</Typography>
      <Typography color="text.secondary">Live read through ODS Gateway. Commands remain owned by Authoring, Authz and Configuration services.</Typography>
      {isLoading ? <LoadingPanel label={`Loading ${labels[area]}`} /> : null}
      {error ? <p role="alert">Gateway request failed: {error.message}</p> : null}
      {data && isFailure(data) ? <p role="alert">{data.problem.title} ({data.problem.code}, HTTP {data.problem.status}){data.problem.detail ? `: ${data.problem.detail}` : ""}</p> : null}
      {data?.ok ? <>
        {rows.length ? <ol>{rows.map((row, index) => <li key={stableKey(row, index)}><code>{stableKey(row, index)}</code>{area === "authoring" ? <Button type="button" onClick={() => { setModelId(stableKey(row, index)); setVersionId(""); setCommandMessage(""); }}>Manage deployments</Button> : null}<pre>{JSON.stringify(row, null, 2)}</pre></li>)}</ol> : <p>No records returned by the service.</p>}
        {area === "authoring" && modelId ? <section aria-label="Runtime deployments">
          <Typography variant="h3">Versions and deployments for {modelId}</Typography>
          <Button type="button" onClick={() => void versions.mutate()}>Refresh versions</Button>
          {versions.data?.ok ? <pre>{JSON.stringify(versions.data.data, null, 2)}</pre> : null}
          {versions.data && isFailure(versions.data) ? <p role="alert">Version lookup failed: {versions.data.problem.code}</p> : null}
          <label>Immutable version ID <input value={versionId} onChange={(event) => setVersionId(event.target.value)} /></label>
          <label>Target environment <input value={environment} onChange={(event) => setEnvironment(event.target.value)} /></label>
          <Button type="button" disabled={!versionId.trim() || !environment.trim()} onClick={() => void deploy()}>Deploy version</Button>
          {commandMessage ? <p role="status">{commandMessage}</p> : null}
          {deployments.data?.ok ? <><Typography variant="h3">Deployment history</Typography><pre>{JSON.stringify(deployments.data.data, null, 2)}</pre></> : null}
          {deployments.data && isFailure(deployments.data) ? <p role="alert">Deployment lookup failed: {deployments.data.problem.code}</p> : null}
        </section> : null}
        <details><summary>Gateway response</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>
      </> : null}
    </Paper>
  </Page>;
}

function asRows(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  if (value === null || typeof value !== "object") return [];
  const record = value as Record<string, unknown>;
  for (const key of ["models", "versions", "profiles", "entries", "deployments"]) if (Array.isArray(record[key])) return record[key];
  return [];
}
function isFailure(value: ApiEnvelope): value is Extract<ApiEnvelope, { ok: false }> { return value.ok === false; }
function stableKey(value: unknown, index: number): string {
  if (value && typeof value === "object") {
    const record = value as Record<string, unknown>;
    for (const key of ["id", "profile_id", "version_id", "process_model_id", "name"]) if (typeof record[key] === "string") return record[key] as string;
  }
  return `record-${index + 1}`;
}
export const contractVersion = "1.0";
export const route = { path: "/studio", title: "BPMP Studio", Component: StudioRoute };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
