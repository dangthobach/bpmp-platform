import { useMemo, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
  Divider,
  ErrorPanel,
  FormControl,
  InputLabel,
  LoadingPanel,
  MenuItem,
  Paper,
  Select,
  Stack,
  StatusPanel,
  TextField,
  Typography,
} from "@ods/shared-ui";
import {
  createCommandPair,
  defaultProvidersSeed,
  deriveProviderHealthStatus,
  PROVIDER_PRESETS,
  type DriverKind,
  type NotificationChannel,
  type ProviderProfile,
} from "../types";

interface ProvidersHealthResponse {
  providers?: ProviderProfile[];
}

interface Props {
  platform: PlatformServices;
}

export function ServiceProvidersWorkspace({ platform }: Props): ReactNode {
  const providersQuery = useSWR<ProvidersHealthResponse>(
    "notification-mfe:providers-health",
    async () => {
      const res = await platform.callApi<ProvidersHealthResponse>({
        method: "GET",
        path: "/v1/notifications/providers/health",
      });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const [localProviders, setLocalProviders] = useState<ProviderProfile[]>([]);
  const [providerPatches, setProviderPatches] = useState<Record<string, Partial<ProviderProfile>>>({});

  const providers = useMemo(() => {
    const base =
      providersQuery.data?.providers && providersQuery.data.providers.length > 0
        ? providersQuery.data.providers
        : defaultProvidersSeed;
    return [...base, ...localProviders].map((prv) => ({
      ...prv,
      ...(providerPatches[prv.provider_id] ?? {}),
    }));
  }, [providersQuery.data, localProviders, providerPatches]);

  // Preset picker / create form
  const [selectedPresetId, setSelectedPresetId] = useState<string>(PROVIDER_PRESETS[0]!.id);
  const activePreset = useMemo(
    () => PROVIDER_PRESETS.find((p) => p.id === selectedPresetId) ?? PROVIDER_PRESETS[0]!,
    [selectedPresetId],
  );
  const [newProviderKey, setNewProviderKey] = useState("");
  const [newProviderName, setNewProviderName] = useState("");
  const [newEndpoint, setNewEndpoint] = useState(PROVIDER_PRESETS[0]!.default_endpoint);
  const [newSenderIdentity, setNewSenderIdentity] = useState("no-reply@bpmp.vn");
  const [newTimeoutMs, setNewTimeoutMs] = useState("5000");
  const [newMaxRetries, setNewMaxRetries] = useState("3");

  // Edit revision & routing policy state
  const [editingProviderId, setEditingProviderId] = useState<string>(defaultProvidersSeed[0]!.provider_id);
  const editingProvider = useMemo(
    () => providers.find((p) => p.provider_id === editingProviderId) ?? providers[0],
    [providers, editingProviderId],
  );
  const [editTier, setEditTier] = useState<string>("1");
  const [editWeight, setEditWeight] = useState<string>("100");
  const [editTimeoutMs, setEditTimeoutMs] = useState<string>("5000");
  const [editMaxRetries, setEditMaxRetries] = useState<string>("3");

  const [busy, setBusy] = useState(false);
  const [statusBanner, setStatusBanner] = useState<{ title: string; detail: string } | null>(null);
  const [errorBanner, setErrorBanner] = useState<{ title: string; detail: string } | null>(null);

  const handlePresetChange = (presetId: string) => {
    setSelectedPresetId(presetId);
    const found = PROVIDER_PRESETS.find((p) => p.id === presetId);
    if (found) {
      setNewEndpoint(found.default_endpoint);
      if (!newProviderName.trim()) setNewProviderName(found.label);
    }
  };

  const handleSelectForEdit = (prv: ProviderProfile) => {
    setEditingProviderId(prv.provider_id);
    setEditTier(String(prv.routing_tier));
    setEditWeight(String(prv.routing_weight));
    setEditTimeoutMs(String(prv.timeout_ms ?? 5000));
    setEditMaxRetries(String(prv.max_retries ?? 3));
  };

  const handleCreateProvider = async () => {
    if (!newProviderKey.trim() || !newProviderName.trim() || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-create-prv");
    const res = await platform.callApi<{ provider?: ProviderProfile }>({
      method: "POST",
      path: "/v1/notifications/providers",
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        provider_key: newProviderKey.trim(),
        name: newProviderName.trim(),
        channel: activePreset.channel as NotificationChannel,
        driver_kind: activePreset.driver_kind as DriverKind,
        preset: activePreset.id,
        config: {
          endpoint: newEndpoint.trim(),
          sender_identity: newSenderIdentity.trim(),
          timeout_ms: Number(newTimeoutMs) || 5000,
          max_retries: Number(newMaxRetries) || 3,
        },
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Create provider failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const created: ProviderProfile = res.data.provider ?? {
      provider_id: `prv-${newProviderKey.trim()}`,
      provider_key: newProviderKey.trim(),
      name: newProviderName.trim(),
      channel: activePreset.channel,
      driver_kind: activePreset.driver_kind,
      preset: activePreset.id,
      endpoint: newEndpoint.trim(),
      sender_identity: newSenderIdentity.trim(),
      routing_tier: 1,
      routing_weight: 100,
      timeout_ms: Number(newTimeoutMs) || 5000,
      max_retries: Number(newMaxRetries) || 3,
      operational_state: "ACTIVE",
      breaker_state: "CLOSED",
      error_rate_bps: 0,
      window_requests: 0,
      window_failures: 0,
      avg_latency_ms: 0,
      success_rate_bps: 10000,
      sent_24h: 0,
      environment: "staging",
      row_version: 1,
    };
    setLocalProviders((prev) => [...prev, created]);
    setNewProviderKey("");
    setNewProviderName("");
    setStatusBanner({
      title: "Provider profile created",
      detail: `Registered ${created.provider_key} using preset ${activePreset.label} (${activePreset.driver_kind}).`,
    });
  };

  const handleToggleOperationalState = async (prv: ProviderProfile) => {
    if (busy) return;
    const nextState = prv.operational_state === "DISABLED" ? "ACTIVE" : "DISABLED";
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-prv-state");
    const res = await platform.callApi({
      method: "PUT",
      path: `/v1/notifications/providers/${encodeURIComponent(prv.provider_id)}/revisions`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        expected_version: prv.row_version ?? 1,
        driver_kind: prv.driver_kind,
        operational_state: nextState,
        config: { endpoint: prv.endpoint, timeout_ms: prv.timeout_ms ?? 5000, max_retries: prv.max_retries ?? 3 },
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "State toggle failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setProviderPatches((prev) => ({
      ...prev,
      [prv.provider_id]: { ...(prev[prv.provider_id] ?? {}), operational_state: nextState },
    }));
    setStatusBanner({
      title: `Provider ${nextState}`,
      detail: `Audited operational_state change for ${prv.provider_key} -> ${nextState} (D6).`,
    });
  };

  const handleTestConnection = async (prv: ProviderProfile) => {
    if (busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-prv-probe");
    const res = await platform.callApi<{ valid?: boolean; latency_ms?: number }>({
      method: "POST",
      path: `/v1/notifications/providers/${encodeURIComponent(prv.provider_id)}/test-connection`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: { expected_version: prv.row_version ?? 1 },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Connection test failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setStatusBanner({
      title: `Connection probe passed for ${prv.provider_key}`,
      detail: `Driver ${prv.driver_kind} reachable (${res.data.latency_ms ?? prv.avg_latency_ms ?? 95} ms).`,
    });
  };

  const handleSaveRevisionAndActivate = async () => {
    if (!editingProvider || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const revPair = createCommandPair("ntf-prv-rev");
    const revRes = await platform.callApi<{ revision_id?: string }>({
      method: "PUT",
      path: `/v1/notifications/providers/${encodeURIComponent(editingProvider.provider_id)}/revisions`,
      commandId: revPair.commandId,
      idempotencyKey: revPair.idempotencyKey,
      body: {
        expected_version: editingProvider.row_version ?? 1,
        driver_kind: editingProvider.driver_kind,
        config: {
          endpoint: editingProvider.endpoint,
          timeout_ms: Number(editTimeoutMs) || 5000,
          max_retries: Number(editMaxRetries) || 3,
        },
      },
    });
    if (!revRes.ok) {
      setBusy(false);
      setErrorBanner({ title: revRes.problem.title || "Save revision failed", detail: `Code: ${revRes.problem.code}` });
      return;
    }

    const revisionId = revRes.data.revision_id ?? `rev-${Date.now()}`;
    const actPair = createCommandPair("ntf-prv-act");
    const actRes = await platform.callApi({
      method: "POST",
      path: `/v1/notifications/providers/${encodeURIComponent(editingProvider.provider_id)}/revisions/${encodeURIComponent(revisionId)}/activate`,
      commandId: actPair.commandId,
      idempotencyKey: actPair.idempotencyKey,
      body: { expected_version: editingProvider.row_version ?? 1, reason: "Operator save revision & activate" },
    });
    if (!actRes.ok) {
      setBusy(false);
      setErrorBanner({ title: actRes.problem.title || "Activate revision failed", detail: `Code: ${actRes.problem.code}` });
      return;
    }

    // D6: routing priority/tier writes to routing-policies
    const policyPair = createCommandPair("ntf-routing-pol");
    const tierNum = Number(editTier) || 1;
    const weightNum = Number(editWeight) || 100;
    const policyRes = await platform.callApi({
      method: "PUT",
      path: `/v1/notifications/routing-policies/${encodeURIComponent(`policy-${editingProvider.channel.toLowerCase()}`)}`,
      commandId: policyPair.commandId,
      idempotencyKey: policyPair.idempotencyKey,
      body: {
        expected_version: 1,
        channel: editingProvider.channel,
        tiers: [{ provider_id: editingProvider.provider_id, tier: tierNum, weight: weightNum }],
      },
    });
    setBusy(false);
    if (!policyRes.ok) {
      setErrorBanner({ title: policyRes.problem.title || "Routing policy update failed", detail: `Code: ${policyRes.problem.code}` });
      return;
    }

    setProviderPatches((prev) => ({
      ...prev,
      [editingProvider.provider_id]: {
        ...(prev[editingProvider.provider_id] ?? {}),
        routing_tier: tierNum,
        routing_weight: weightNum,
        timeout_ms: Number(editTimeoutMs) || 5000,
        max_retries: Number(editMaxRetries) || 3,
      },
    }));
    setStatusBanner({
      title: "Provider revision activated & routing policy updated",
      detail: `${editingProvider.provider_key} activated (Tier ${tierNum}, Weight ${weightNum}%, timeout ${editTimeoutMs}ms).`,
    });
  };

  if (providersQuery.isLoading) {
    return <LoadingPanel label="Loading service providers and health metrics..." />;
  }

  return (
    <Stack spacing={2}>
      {statusBanner && <StatusPanel title={statusBanner.title} detail={statusBanner.detail} />}
      {errorBanner && <ErrorPanel title={errorBanner.title} detail={errorBanner.detail} />}

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Box>
            <Typography variant="h6">Service Providers & Routing Tiers</Typography>
            <Typography variant="caption" color="text.secondary">
              Health status is derived from Circuit Breaker state (OPEN → DOWN, HALF_OPEN → DEGRADED). Priority/tier edits write to routing policies (D6). Note: Apple APNs (JWT ES256 / HTTP/2) is tracked in Slice 2 backlog; use FCM v1 for mobile push.
            </Typography>
          </Box>

          <Box sx={{ overflowX: "auto" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                  <th style={{ padding: "8px 6px" }}>Provider / Preset</th>
                  <th style={{ padding: "8px 6px" }}>Channel & Driver</th>
                  <th style={{ padding: "8px 6px" }}>Routing Tier</th>
                  <th style={{ padding: "8px 6px" }}>Health</th>
                  <th style={{ padding: "8px 6px" }}>Avg Latency</th>
                  <th style={{ padding: "8px 6px" }}>Success Rate</th>
                  <th style={{ padding: "8px 6px" }}>Sent (24h)</th>
                  <th style={{ padding: "8px 6px" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {providers.map((prv) => {
                  const health = deriveProviderHealthStatus(prv);
                  const successPct = ((prv.success_rate_bps ?? (10000 - prv.error_rate_bps)) / 100).toFixed(2);
                  return (
                    <tr key={prv.provider_id} style={{ borderBottom: "1px solid #eff3f0" }}>
                      <td style={{ padding: "8px 6px" }}>
                        <Typography variant="body2" fontWeight={700}>{prv.name}</Typography>
                        <Typography variant="caption" color="text.secondary">
                          {prv.provider_key} • Preset: {prv.preset ?? "custom"}
                        </Typography>
                      </td>
                      <td style={{ padding: "8px 6px" }}>
                        <Chip size="small" variant="outlined" label={`${prv.channel} · ${prv.driver_kind}`} />
                      </td>
                      <td style={{ padding: "8px 6px" }}>
                        Tier {prv.routing_tier} ({prv.routing_weight}%)
                      </td>
                      <td style={{ padding: "8px 6px" }}>
                        <Chip
                          size="small"
                          color={health === "HEALTHY" ? "success" : health === "DEGRADED" ? "warning" : "error"}
                          label={`${health} (${prv.breaker_state})`}
                        />
                      </td>
                      <td style={{ padding: "8px 6px" }}>{prv.avg_latency_ms ?? 0} ms</td>
                      <td style={{ padding: "8px 6px" }}>{successPct}%</td>
                      <td style={{ padding: "8px 6px" }}>{(prv.sent_24h ?? 0).toLocaleString()}</td>
                      <td style={{ padding: "8px 6px" }}>
                        <Stack direction="row" spacing={0.5} flexWrap="wrap">
                          <Button size="small" variant="text" onClick={() => handleSelectForEdit(prv)}>Configure</Button>
                          <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleTestConnection(prv)}>Test</Button>
                          <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleToggleOperationalState(prv)}>
                            {prv.operational_state === "DISABLED" ? "Enable" : "Disable"}
                          </Button>
                        </Stack>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </Box>
        </Stack>
      </Paper>

      {/* Save Revision & Activate + Routing Policy */}
      {editingProvider && (
        <Paper variant="outlined" sx={{ p: 2 }}>
          <Stack spacing={1.5}>
            <Typography variant="subtitle1" fontWeight={700}>
              Edit Revision & Routing Policy: {editingProvider.name} ({editingProvider.provider_key})
            </Typography>
            <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} alignItems="center">
              <TextField size="small" label="Routing Tier (1=Primary, 2=Fallback)" value={editTier} onChange={(e) => setEditTier(e.target.value)} sx={{ width: 230 }} />
              <TextField size="small" label="Routing Weight (%)" value={editWeight} onChange={(e) => setEditWeight(e.target.value)} sx={{ width: 150 }} />
              <TextField size="small" label="Timeout (ms)" value={editTimeoutMs} onChange={(e) => setEditTimeoutMs(e.target.value)} sx={{ width: 130 }} />
              <TextField size="small" label="Max Retries" value={editMaxRetries} onChange={(e) => setEditMaxRetries(e.target.value)} sx={{ width: 120 }} />
              <Button variant="contained" disabled={busy} onClick={() => void handleSaveRevisionAndActivate()}>
                Save Revision & Activate
              </Button>
            </Stack>
          </Stack>
        </Paper>
      )}

      {/* Preset Picker */}
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Box>
            <Typography variant="subtitle1" fontWeight={700}>Add Service Provider from Preset</Typography>
            <Typography variant="caption" color="text.secondary">
              Select a curated vendor preset (AWS SES, SendGrid, SMTP Internal, FCM v1, VNPT SMS, Viettel Brandname, Slack Webhook, Zalo ZNS Mock, In-App). APNs JWT ES256 is planned for Slice 2.
            </Typography>
          </Box>
          <Divider />
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} flexWrap="wrap" alignItems="center">
            <FormControl size="small" sx={{ minWidth: 200 }}>
              <InputLabel id="prv-preset-picker">Vendor Preset</InputLabel>
              <Select labelId="prv-preset-picker" label="Vendor Preset" value={selectedPresetId} onChange={(e) => handlePresetChange(e.target.value)}>
                {PROVIDER_PRESETS.map((preset) => (
                  <MenuItem key={preset.id} value={preset.id}>
                    {preset.label} ({preset.channel} · {preset.driver_kind})
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
            <TextField size="small" label="Provider key" placeholder="viettel-sms-fallback" value={newProviderKey} onChange={(e) => setNewProviderKey(e.target.value)} />
            <TextField size="small" label="Display name" placeholder="Viettel Brandname Fallback" value={newProviderName} onChange={(e) => setNewProviderName(e.target.value)} />
            <TextField size="small" label="Endpoint" value={newEndpoint} onChange={(e) => setNewEndpoint(e.target.value)} sx={{ minWidth: 240 }} />
            <TextField size="small" label="Sender identity" value={newSenderIdentity} onChange={(e) => setNewSenderIdentity(e.target.value)} />
            <TextField size="small" label="Timeout (ms)" value={newTimeoutMs} onChange={(e) => setNewTimeoutMs(e.target.value)} sx={{ width: 110 }} />
            <TextField size="small" label="Max retries" value={newMaxRetries} onChange={(e) => setNewMaxRetries(e.target.value)} sx={{ width: 105 }} />
            <Button variant="contained" disabled={busy || !newProviderKey.trim() || !newProviderName.trim()} onClick={() => void handleCreateProvider()}>
              Create Provider
            </Button>
          </Stack>
        </Stack>
      </Paper>
    </Stack>
  );
}
