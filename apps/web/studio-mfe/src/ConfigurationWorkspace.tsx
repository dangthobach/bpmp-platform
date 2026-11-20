import { useEffect, useRef, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import { Button, Paper, Stack, Typography } from "@ods/shared-ui";
import { commandKey, isRecord, rows, send } from "./api";

const owners = ["ENGINE", "API_GATEWAY", "HUMAN_RUNTIME", "PROJECTION", "GOVERNANCE", "CONFIGURATION_SERVICE", "COCKPIT_GATEWAY", "AUTHZ_CONTROL_PLANE"] as const;
const scopeTypes = ["PLATFORM", "ENVIRONMENT", "TENANT", "WORKFLOW_TYPE", "WORKFLOW_VERSION", "APPROVED_INSTANCE_OVERRIDE"] as const;
type ConfigInput = { name: string; owner: string; scopeType: string; scopeReference: string; schemaVersion: string; policyVersion: string; reason: string; values: string };
const initial: ConfigInput = { name: "", owner: "ENGINE", scopeType: "TENANT", scopeReference: "", schemaVersion: "1", policyVersion: "", reason: "", values: "{}" };

export function ConfigurationWorkspace({ platform }: { platform: PlatformServices }): ReactNode {
  const [profileId, setProfileId] = useState("");
  const [versionId, setVersionId] = useState("");
  const [fromVersion, setFromVersion] = useState("");
  const [toVersion, setToVersion] = useState("");
  const [form, setForm] = useState<ConfigInput>(initial);
  const [notice, setNotice] = useState("");
  const [failure, setFailure] = useState("");
  const [formDirty, setFormDirty] = useState(false);
  const retries = useRef(new Map<string, { fingerprint: string; key: string }>());
  const profiles = useSWR(["studio-config-profiles"], async () => send(platform.callApi, "GET", "/v1/configuration/profiles?page_size=50"));
  const detail = useSWR(profileId ? ["studio-config-profile", profileId] : null, async () => send(platform.callApi, "GET", `/v1/configuration/profiles/${encodeURIComponent(profileId)}`));
  const diff = useSWR(profileId && fromVersion && toVersion ? ["studio-config-diff", profileId, fromVersion, toVersion] : null, async () => send(platform.callApi, "GET", `/v1/configuration/profiles/${encodeURIComponent(profileId)}/diff?from_version=${encodeURIComponent(fromVersion)}&to_version=${encodeURIComponent(toVersion)}`));
  const profile = detail.data?.ok && isRecord(detail.data.data) && isRecord(detail.data.data.profile) ? detail.data.data.profile : {};
  const profileVersions = detail.data?.ok && isRecord(detail.data.data) ? rows(detail.data.data, "versions") : [];
  const aggregateVersion = Number(profile.aggregate_version ?? 0);
  useEffect(() => {
    if (!profileId || formDirty || !detail.data?.ok || !isRecord(detail.data.data) || !isRecord(detail.data.data.profile)) return;
    const loadedProfile = detail.data.data.profile;
    const latestVersion = rows(detail.data.data, "versions").at(-1);
    const scope = isRecord(loadedProfile.scope) ? loadedProfile.scope : {};
    setForm((current) => ({
      ...current,
      name: typeof loadedProfile.name === "string" ? loadedProfile.name : current.name,
      owner: typeof loadedProfile.owner === "string" ? loadedProfile.owner : current.owner,
      scopeType: typeof scope.type === "string" ? scope.type : current.scopeType,
      scopeReference: typeof scope.reference === "string" ? scope.reference : current.scopeReference,
      schemaVersion: latestVersion && typeof latestVersion.schema_version === "number" ? String(latestVersion.schema_version) : current.schemaVersion,
      policyVersion: latestVersion && typeof latestVersion.policy_version === "string" ? latestVersion.policy_version : current.policyVersion,
      values: latestVersion && isRecord(latestVersion.values) ? JSON.stringify(latestVersion.values, null, 2) : current.values,
    }));
  }, [profileId, detail.data, formDirty]);
  async function command(path: string, body: unknown) {
    const fingerprint = JSON.stringify({ method: "POST", path, body });
    const previous = retries.current.get(path);
    const key = previous?.fingerprint === fingerprint ? previous.key : commandKey();
    retries.current.set(path, { fingerprint, key });
    const result = await send<Record<string, unknown>>(platform.callApi, "POST", path, body, key);
    if (result.ok || (!result.ok && result.problem.status === 409)) retries.current.delete(path);
    return result;
  }
  async function mutate(path: string, body: unknown, label: string): Promise<void> {
    setNotice(""); setFailure("");
    const result = await command(path, body);
    if (!result.ok) { setFailure(`${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}, request ${result.requestId}). ${result.problem.status === 409 ? "Refresh and review the latest aggregate version." : "Reconcile current profile state before retrying."}`); if (result.problem.status === 409) { retries.current.delete(path); await Promise.all([detail.mutate(), profiles.mutate()]); } return; }
    retries.current.delete(path);
    setNotice(`${label} succeeded (${result.requestId}).`); await Promise.all([detail.mutate(), profiles.mutate()]);
  }
  async function values(): Promise<Record<string, unknown> | undefined> { try { const parsed: unknown = JSON.parse(form.values); if (!isRecord(parsed)) throw new Error("Values must be a JSON object."); return parsed; } catch (error) { setFailure(error instanceof Error ? error.message : "Values JSON is invalid."); return undefined; } }
  async function create(): Promise<void> {
    const parsed = await values(); if (!parsed || !form.name.trim() || !form.reason.trim() || !form.scopeReference.trim() || !form.policyVersion.trim() || !Number.isSafeInteger(Number(form.schemaVersion))) return;
    const result = await command("/v1/configuration/profiles", { name: form.name.trim(), owner: form.owner, scope: { type: form.scopeType, reference: form.scopeReference.trim() }, schema_version: Number(form.schemaVersion), policy_version: form.policyVersion.trim(), reason: form.reason.trim(), values: parsed });
    if (!result.ok) { setFailure(`${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}).`); return; }
    const newId = typeof result.data.id === "string" ? result.data.id : ""; if (newId) setProfileId(newId);
    setNotice(`Configuration profile created (${result.requestId}).`); await profiles.mutate();
  }
  async function addDraft(): Promise<void> {
    const parsed = await values(); if (!parsed || !profileId || !form.reason.trim() || !form.policyVersion.trim() || !Number.isSafeInteger(aggregateVersion) || aggregateVersion < 1) return;
    const result = await command(`/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions`, { expected_version: aggregateVersion, schema_version: Number(form.schemaVersion), policy_version: form.policyVersion.trim(), reason: form.reason.trim(), values: parsed });
    if (!result.ok) { setFailure(`${result.problem.title} (${result.problem.code}, HTTP ${result.problem.status}).`); if (result.problem.status === 409) await detail.mutate(); return; }
    setNotice(`Configuration draft created (${result.requestId}).`); await Promise.all([detail.mutate(), profiles.mutate()]);
  }
  async function transition(action: "publish" | "rollback" | "restore"): Promise<void> {
    if (!versionId || !profileId || !form.reason.trim()) return;
    const body: Record<string, unknown> = { expected_version: aggregateVersion, reason: form.reason.trim() };
    if (action !== "publish") body.policy_version = form.policyVersion.trim();
    await mutate(`/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/${action}`, body, `Configuration ${action}`);
  }
  async function retire(): Promise<void> { if (profileId && form.reason.trim()) await mutate(`/v1/configuration/profiles/${encodeURIComponent(profileId)}/retire`, { expected_version: aggregateVersion, reason: form.reason.trim() }, "Configuration retire"); }
  function update(field: keyof ConfigInput, value: string): void { setFormDirty(true); setForm((current) => ({ ...current, [field]: value })); }
  return <Paper sx={{ p: 2, mt: 2 }}>
    <Typography variant="h2">Configuration profiles</Typography><Typography color="text.secondary">Version transitions are owned by Configuration Service and use aggregate-version checks. Values must conform to the selected owner policy schema.</Typography>
    {profiles.isLoading ? <p>Loading configuration profiles…</p> : null}{profiles.data && !profiles.data.ok ? <p role="alert">Configuration API unavailable: {profiles.data.problem.code}</p> : null}
    <ul>{(profiles.data?.ok ? rows(profiles.data.data, "profiles") : []).map((item, index) => <li key={String(item.id ?? index)}><Button type="button" onClick={() => { setFormDirty(false); setProfileId(String(item.id ?? "")); setVersionId(""); }}>{String(item.name ?? item.id)} · v{String(item.aggregate_version)}</Button></li>)}</ul>
    <Stack direction="row" spacing={1} sx={{ my: 1 }}><input aria-label="Profile name" placeholder="Profile name" value={form.name} onChange={(e) => update("name", e.target.value)} /><select aria-label="Owner" value={form.owner} onChange={(e) => update("owner", e.target.value)}>{owners.map((v) => <option key={v}>{v}</option>)}</select><select aria-label="Scope type" value={form.scopeType} onChange={(e) => update("scopeType", e.target.value)}>{scopeTypes.map((v) => <option key={v}>{v}</option>)}</select><input aria-label="Scope reference" placeholder="Scope reference" value={form.scopeReference} onChange={(e) => update("scopeReference", e.target.value)} /></Stack>
    <label>Policy version<input aria-label="Policy version" value={form.policyVersion} onChange={(e) => update("policyVersion", e.target.value)} /></label><label>Schema version<input aria-label="Schema version" type="number" min="1" value={form.schemaVersion} onChange={(e) => update("schemaVersion", e.target.value)} /></label>
    <label>Values JSON<textarea aria-label="Configuration values JSON" rows={8} value={form.values} onChange={(e) => update("values", e.target.value)} /></label><label>Reason<input aria-label="Configuration reason" value={form.reason} onChange={(e) => update("reason", e.target.value)} /></label>
    <Stack direction="row" spacing={1}><Button type="button" disabled={!form.name.trim() || !form.reason.trim() || !form.policyVersion.trim()} onClick={() => void create()}>Create profile</Button><Button type="button" disabled={!profileId || !aggregateVersion || !form.reason.trim() || !form.policyVersion.trim()} onClick={() => void addDraft()}>Add draft version</Button></Stack>
    {profileId ? <>
      <Typography variant="h3">Profile versions · aggregate {aggregateVersion}</Typography>{detail.isLoading ? <p>Loading profile detail…</p> : null}{detail.data && !detail.data.ok ? <p role="alert">Profile detail failed: {detail.data.problem.code}</p> : null}
      <ul>{profileVersions.map((v, i) => <li key={String(v.id ?? i)}>{String(v.config_version ?? v.ordinal)} · {String(v.status)} · {String(v.id)} <Button type="button" onClick={() => setVersionId(String(v.id ?? ""))}>Select version</Button></li>)}</ul>
      <Stack direction="row" spacing={1}><Button type="button" disabled={!versionId || !form.reason.trim()} onClick={() => void transition("publish")}>Publish selected</Button><Button type="button" disabled={!versionId || !form.reason.trim() || !form.policyVersion.trim()} onClick={() => void transition("rollback")}>Rollback to selected</Button><Button type="button" disabled={!versionId || !form.reason.trim() || !form.policyVersion.trim()} onClick={() => void transition("restore")}>Restore selected</Button><Button type="button" disabled={!form.reason.trim() || !aggregateVersion} onClick={() => void retire()}>Retire profile</Button></Stack>
      <Stack direction="row" spacing={1}><select aria-label="Diff from version" value={fromVersion} onChange={(e) => setFromVersion(e.target.value)}><option value="">Diff from</option>{profileVersions.map((v) => <option key={String(v.id)} value={String(v.id)}>{String(v.config_version ?? v.ordinal)}</option>)}</select><select aria-label="Diff to version" value={toVersion} onChange={(e) => setToVersion(e.target.value)}><option value="">Diff to</option>{profileVersions.map((v) => <option key={String(v.id)} value={String(v.id)}>{String(v.config_version ?? v.ordinal)}</option>)}</select></Stack>
      {diff.data?.ok ? <pre>{JSON.stringify(diff.data.data, null, 2)}</pre> : null}{diff.data && !diff.data.ok ? <p role="alert">Diff failed: {diff.data.problem.code}</p> : null}
    </> : null}
    {failure ? <p role="alert">{failure}</p> : null}{notice ? <p role="status">{notice}</p> : null}
  </Paper>;
}
