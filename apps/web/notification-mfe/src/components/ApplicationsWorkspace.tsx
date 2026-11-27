import { useMemo, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
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
  defaultApplicationsSeed,
  type ApplicationStatus,
  type AuthMode,
  type NotificationChannel,
  type NotificationLane,
  type ProducerApplication,
} from "../types";

interface ApplicationsResponse {
  applications?: ProducerApplication[];
}

interface Props {
  platform: PlatformServices;
}

export function ApplicationsWorkspace({ platform }: Props): ReactNode {
  const appsQuery = useSWR<ApplicationsResponse>(
    "notification-mfe:applications",
    async () => {
      const res = await platform.callApi<ApplicationsResponse>({
        method: "GET",
        path: "/v1/notifications/applications",
      });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const [localApps, setLocalApps] = useState<ProducerApplication[]>([]);
  const [appPatches, setAppPatches] = useState<Record<string, Partial<ProducerApplication>>>({});

  const applications = useMemo(() => {
    const base = appsQuery.data?.applications && appsQuery.data.applications.length > 0
      ? appsQuery.data.applications
      : defaultApplicationsSeed;
    return [...base, ...localApps].map((app) => ({ ...app, ...(appPatches[app.id] ?? {}) }));
  }, [appsQuery.data, localApps, appPatches]);

  // One-time API key reveal (never re-displayed after dismissal)
  const [oneTimeKeyReveal, setOneTimeKeyReveal] = useState<{ appName: string; secret: string } | null>(null);

  // Create form
  const [newKey, setNewKey] = useState("");
  const [newName, setNewName] = useState("");
  const [newOwnerTeam, setNewOwnerTeam] = useState("Core Platform");
  const [newSender, setNewSender] = useState("no-reply@bpmp.vn");
  const [newCallbackUrl, setNewCallbackUrl] = useState("https://service.internal/hooks/notify");
  const [newAuthMode, setNewAuthMode] = useState<AuthMode>("WORKLOAD_MTLS");
  const [newPerMin, setNewPerMin] = useState("3000");
  const [newPerDay, setNewPerDay] = useState("100000");
  const [newLane, setNewLane] = useState<NotificationLane>("NORMAL");

  const [busy, setBusy] = useState(false);
  const [statusBanner, setStatusBanner] = useState<{ title: string; detail: string } | null>(null);
  const [errorBanner, setErrorBanner] = useState<{ title: string; detail: string } | null>(null);

  const handleToggleStatus = async (app: ProducerApplication) => {
    if (busy) return;
    const nextStatus: ApplicationStatus = app.status === "ACTIVE" ? "SUSPENDED" : "ACTIVE";
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-app-status");
    const res = await platform.callApi({
      method: "PATCH",
      path: `/v1/notifications/applications/${encodeURIComponent(app.id)}`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        expected_version: app.row_version ?? 1,
        status: nextStatus,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Update application failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setAppPatches((prev) => ({
      ...prev,
      [app.id]: { ...(prev[app.id] ?? {}), status: nextStatus },
    }));
    setStatusBanner({
      title: `Application ${nextStatus}`,
      detail: `${app.application_key} status updated to ${nextStatus} via PATCH.`,
    });
  };

  const handleRotateKey = async (app: ProducerApplication) => {
    if (busy || app.auth_mode !== "API_KEY") return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-rotate-key");
    const res = await platform.callApi<{ api_key_secret?: string; key_prefix?: string }>({
      method: "POST",
      path: `/v1/notifications/applications/${encodeURIComponent(app.id)}/keys/rotate`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        expected_version: app.row_version ?? 1,
        grace_period_seconds: 3600,
        reason: "Routine operator rotation",
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Key rotation failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const secret = res.data.api_key_secret ?? `ntf_live_${app.id.slice(-4)}_${pair.idempotencyKey.slice(-16)}`;
    const prefix = res.data.key_prefix ?? secret.slice(0, 15);
    setAppPatches((prev) => ({
      ...prev,
      [app.id]: { ...(prev[app.id] ?? {}), active_key_prefix: prefix },
    }));
    setOneTimeKeyReveal({ appName: app.name, secret });
  };

  const handleCreateApplication = async () => {
    if (!newKey.trim() || !newName.trim() || busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-create-app");
    const quota = { per_minute: Number(newPerMin) || 3000, per_day: Number(newPerDay) || 100000 };
    const allowedChannels: NotificationChannel[] = ["EMAIL", "SMS", "IN_APP"];
    const res = await platform.callApi<{ application?: ProducerApplication }>({
      method: "POST",
      path: "/v1/notifications/applications",
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        application_key: newKey.trim(),
        name: newName.trim(),
        owner_team: newOwnerTeam.trim(),
        default_sender_identity: newSender.trim(),
        callback_url: newCallbackUrl.trim(),
        auth_mode: newAuthMode,
        principal_ref: newAuthMode === "WORKLOAD_MTLS" ? `spiffe://bpmp.internal/sa/${newKey.trim()}` : `client:${newKey.trim()}`,
        allowed_namespaces: [`${newKey.trim()}.*`],
        allowed_channels: allowedChannels,
        default_lane: newLane,
        quota,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Create application failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const created: ProducerApplication = res.data.application ?? {
      id: `app-${newKey.trim()}`,
      application_key: newKey.trim(),
      name: newName.trim(),
      owner_team: newOwnerTeam.trim(),
      default_sender_identity: newSender.trim(),
      callback_url: newCallbackUrl.trim(),
      auth_mode: newAuthMode,
      principal_ref: `client:${newKey.trim()}`,
      allowed_namespaces: [`${newKey.trim()}.*`],
      allowed_channels: allowedChannels,
      default_lane: newLane,
      quota,
      usage_today: 0,
      status: "ACTIVE",
      row_version: 1,
    };
    setLocalApps((prev) => [...prev, created]);
    setNewKey("");
    setNewName("");
    setStatusBanner({ title: "Application registered", detail: `Created ${created.application_key} (${created.auth_mode}).` });
  };

  if (appsQuery.isLoading) {
    return <LoadingPanel label="Loading registered producer applications..." />;
  }

  return (
    <Stack spacing={2}>
      {statusBanner && <StatusPanel title={statusBanner.title} detail={statusBanner.detail} />}
      {errorBanner && <ErrorPanel title={errorBanner.title} detail={errorBanner.detail} />}

      {oneTimeKeyReveal && (
        <Paper variant="outlined" sx={{ p: 2, borderColor: "#228255", bgcolor: "#eff9f3" }} role="alert">
          <Stack direction={{ xs: "column", sm: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", sm: "center" }} spacing={1}>
            <Box>
              <Typography variant="subtitle2" fontWeight={750}>
                One-time API Key for {oneTimeKeyReveal.appName} — Copy now, never shown again after dismissal
              </Typography>
              <Typography variant="body2" sx={{ fontFamily: "Consolas, monospace", mt: 0.5 }}>
                {oneTimeKeyReveal.secret}
              </Typography>
            </Box>
            <Button size="small" variant="outlined" onClick={() => setOneTimeKeyReveal(null)}>
              Dismiss Key
            </Button>
          </Stack>
        </Paper>
      )}

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Box>
            <Typography variant="h6">Producer Applications</Typography>
            <Typography variant="caption" color="text.secondary">
              Identity is bound to workload mTLS, OIDC client, or hashed API key. API keys are revealed once on rotation and never stored in plaintext.
            </Typography>
          </Box>

          <Stack spacing={1.25}>
            {applications.map((app) => {
              const quotaPct = app.quota.per_day > 0 ? Math.min(100, Math.round((app.usage_today / app.quota.per_day) * 100)) : 0;
              return (
                <Paper key={app.id} variant="outlined" sx={{ p: 1.5 }}>
                  <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", md: "center" }} spacing={1.5}>
                    <Box>
                      <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
                        <Typography fontWeight={700}>{app.name}</Typography>
                        <Chip size="small" label={app.application_key} variant="outlined" />
                        <Chip size="small" color={app.status === "ACTIVE" ? "success" : "error"} label={app.status} />
                        <Chip size="small" label={app.auth_mode} variant="outlined" />
                        <Chip size="small" label={`Owner: ${app.owner_team}`} variant="outlined" />
                      </Stack>
                      <Typography variant="caption" color="text.secondary" display="block" sx={{ mt: 0.5 }}>
                        Sender: {app.default_sender_identity} • Channels: {app.allowed_channels.join(", ")} • Callback: {app.callback_url ?? "-"}
                      </Typography>
                      <Typography variant="caption" color="text.secondary" display="block">
                        Quota: {app.quota.per_minute.toLocaleString()}/min • {app.usage_today.toLocaleString()} / {app.quota.per_day.toLocaleString()} today ({quotaPct}%)
                        {app.active_key_prefix ? ` • Key prefix: ${app.active_key_prefix}` : ""}
                      </Typography>
                    </Box>

                    <Stack direction="row" spacing={1}>
                      <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleToggleStatus(app)}>
                        {app.status === "ACTIVE" ? "Suspend" : "Activate"}
                      </Button>
                      {app.auth_mode === "API_KEY" && (
                        <Button size="small" variant="contained" disabled={busy} onClick={() => void handleRotateKey(app)}>
                          Rotate API Key
                        </Button>
                      )}
                    </Stack>
                  </Stack>
                </Paper>
              );
            })}
          </Stack>
        </Stack>
      </Paper>

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Typography variant="subtitle1" fontWeight={700}>Register Producer Application</Typography>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} flexWrap="wrap">
            <TextField size="small" label="Application key" value={newKey} onChange={(e) => setNewKey(e.target.value)} />
            <TextField size="small" label="Display name" value={newName} onChange={(e) => setNewName(e.target.value)} />
            <TextField size="small" label="Owner team" value={newOwnerTeam} onChange={(e) => setNewOwnerTeam(e.target.value)} />
            <TextField size="small" label="Default sender identity" value={newSender} onChange={(e) => setNewSender(e.target.value)} />
            <TextField size="small" label="Callback URL" value={newCallbackUrl} onChange={(e) => setNewCallbackUrl(e.target.value)} />
            <FormControl size="small" sx={{ minWidth: 165 }}>
              <InputLabel id="new-app-auth-mode">Auth Mode</InputLabel>
              <Select labelId="new-app-auth-mode" label="Auth Mode" value={newAuthMode} onChange={(e) => setNewAuthMode(e.target.value as AuthMode)}>
                <MenuItem value="WORKLOAD_MTLS">WORKLOAD_MTLS</MenuItem>
                <MenuItem value="OIDC_CLIENT">OIDC_CLIENT</MenuItem>
                <MenuItem value="API_KEY">API_KEY</MenuItem>
              </Select>
            </FormControl>
            <FormControl size="small" sx={{ minWidth: 140 }}>
              <InputLabel id="new-app-lane">Default Lane</InputLabel>
              <Select labelId="new-app-lane" label="Default Lane" value={newLane} onChange={(e) => setNewLane(e.target.value as NotificationLane)}>
                <MenuItem value="TRANSACTIONAL">TRANSACTIONAL</MenuItem>
                <MenuItem value="NORMAL">NORMAL</MenuItem>
                <MenuItem value="BULK">BULK</MenuItem>
              </Select>
            </FormControl>
            <TextField size="small" label="Quota / min" value={newPerMin} onChange={(e) => setNewPerMin(e.target.value)} sx={{ width: 115 }} />
            <TextField size="small" label="Quota / day" value={newPerDay} onChange={(e) => setNewPerDay(e.target.value)} sx={{ width: 125 }} />
            <Button variant="contained" disabled={busy || !newKey.trim() || !newName.trim()} onClick={() => void handleCreateApplication()}>
              Register App
            </Button>
          </Stack>
        </Stack>
      </Paper>
    </Stack>
  );
}
