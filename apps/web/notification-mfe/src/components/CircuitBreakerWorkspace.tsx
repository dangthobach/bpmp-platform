import { useEffect, useMemo, useState, type ReactNode } from "react";
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
  defaultBreakerTransitionsSeed,
  defaultProvidersSeed,
  type BreakerOverrideMode,
  type BreakerTransitionRecord,
  type CommandPair,
  type ProviderProfile,
} from "../types";

interface ProvidersHealthResponse {
  providers?: ProviderProfile[];
}

interface TransitionsResponse {
  transitions?: BreakerTransitionRecord[];
}

interface OverrideIntent {
  providerId: string;
  mode: BreakerOverrideMode;
  reason: string;
  ttlMs: number;
  pair: CommandPair;
}

interface Props {
  platform: PlatformServices;
  onNonClosedCountChange?: ((count: number) => void) | undefined;
}

const MAX_TTL_MS = 86_400_000;

export function CircuitBreakerWorkspace({ platform, onNonClosedCountChange }: Props): ReactNode {
  const providersQuery = useSWR<ProvidersHealthResponse>(
    "notification-mfe:circuit-breakers",
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

  const [providerPatches, setProviderPatches] = useState<Record<string, Partial<ProviderProfile>>>({});
  const providers = useMemo(() => {
    const base =
      providersQuery.data?.providers && providersQuery.data.providers.length > 0
        ? providersQuery.data.providers
        : defaultProvidersSeed;
    return base.map((p) => ({ ...p, ...(providerPatches[p.provider_id] ?? {}) }));
  }, [providersQuery.data, providerPatches]);

  useEffect(() => {
    onNonClosedCountChange?.(providers.filter((p) => p.breaker_state !== "CLOSED").length);
  }, [providers, onNonClosedCountChange]);

  const [selectedProviderId, setSelectedProviderId] = useState<string>("prv-http-sms");
  const selectedProvider = useMemo(
    () => providers.find((p) => p.provider_id === selectedProviderId) ?? providers[0],
    [providers, selectedProviderId],
  );

  const transitionsQuery = useSWR<TransitionsResponse>(
    selectedProvider ? `notification-mfe:breaker-transitions:${selectedProvider.provider_id}` : null,
    async () => {
      if (!selectedProvider) return {};
      const res = await platform.callApi<TransitionsResponse>({
        method: "GET",
        path: `/v1/notifications/providers/${encodeURIComponent(selectedProvider.provider_id)}/circuit-breaker/transitions`,
      });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const [localTransitions, setLocalTransitions] = useState<Record<string, BreakerTransitionRecord[]>>({});
  const transitions = useMemo(() => {
    if (!selectedProvider) return [];
    const fromApi = transitionsQuery.data?.transitions;
    const base =
      fromApi && fromApi.length > 0
        ? fromApi
        : defaultBreakerTransitionsSeed[selectedProvider.provider_id] ?? [];
    return [...(localTransitions[selectedProvider.provider_id] ?? []), ...base];
  }, [selectedProvider, transitionsQuery.data, localTransitions]);

  const [overrideMode, setOverrideMode] = useState<BreakerOverrideMode>("FORCE_OPEN");
  const [overrideReason, setOverrideReason] = useState<string>("");
  const [overrideTtlMs, setOverrideTtlMs] = useState<string>("300000");
  const [overrideIntent, setOverrideIntent] = useState<OverrideIntent | null>(null);
  const [busy, setBusy] = useState(false);
  const [statusBanner, setStatusBanner] = useState<{ title: string; detail: string } | null>(null);
  const [errorBanner, setErrorBanner] = useState<{ title: string; detail: string; onRetry?: () => void } | null>(null);

  const parsedTtlMs = Number(overrideTtlMs);
  const overrideFormValid =
    Boolean(selectedProvider) &&
    overrideReason.trim().length > 0 &&
    Number.isFinite(parsedTtlMs) &&
    parsedTtlMs > 0 &&
    parsedTtlMs <= MAX_TTL_MS;

  const appendTransition = (providerId: string, record: BreakerTransitionRecord) => {
    setLocalTransitions((prev) => ({
      ...prev,
      [providerId]: [record, ...(prev[providerId] ?? [])],
    }));
  };

  const handleApplyOverride = async (intentToRun?: OverrideIntent) => {
    if (!selectedProvider || busy) return;
    const activeIntent = intentToRun ?? overrideIntent ?? {
      providerId: selectedProvider.provider_id,
      mode: overrideMode,
      reason: overrideReason.trim(),
      ttlMs: parsedTtlMs,
      pair: createCommandPair("ntf-breaker"),
    };
    if (!activeIntent.reason || activeIntent.ttlMs <= 0 || activeIntent.ttlMs > MAX_TTL_MS) return;

    setOverrideIntent(activeIntent);
    setBusy(true);
    setErrorBanner(null);
    const res = await platform.callApi<{ provider?: ProviderProfile }>({
      method: "POST",
      path: `/v1/notifications/providers/${encodeURIComponent(activeIntent.providerId)}/circuit-breaker/override`,
      commandId: activeIntent.pair.commandId,
      idempotencyKey: activeIntent.pair.idempotencyKey,
      body: {
        override_mode: activeIntent.mode,
        reason: activeIntent.reason,
        ttl_ms: activeIntent.ttlMs,
      },
    });
    setBusy(false);
    if (!res.ok) {
      if (!res.problem.retryable) setOverrideIntent(null);
      setErrorBanner({
        title: res.problem.title || "Breaker override failed",
        detail: `Code: ${res.problem.code}`,
        ...(res.problem.retryable ? { onRetry: () => { void handleApplyOverride(activeIntent); } } : {}),
      });
      return;
    }

    const nextState =
      activeIntent.mode === "FORCE_OPEN" ? "OPEN" : activeIntent.mode === "FORCE_CLOSE" ? "CLOSED" : "HALF_OPEN";
    setProviderPatches((prev) => ({
      ...prev,
      [activeIntent.providerId]: {
        ...(prev[activeIntent.providerId] ?? {}),
        breaker_state: nextState,
        active_override: activeIntent.mode,
        override_expires_at_epoch_ms: Date.now() + activeIntent.ttlMs,
      },
    }));
    appendTransition(activeIntent.providerId, {
      id: `trn-${Date.now()}`,
      from_state: selectedProvider.breaker_state,
      to_state: nextState,
      reason: `${activeIntent.mode} (${activeIntent.ttlMs}ms): ${activeIntent.reason}`,
      occurred_at: new Date().toISOString(),
    });
    setOverrideIntent(null);
    setOverrideReason("");
    setStatusBanner({
      title: `Circuit breaker override applied: ${activeIntent.mode}`,
      detail: `Provider ${activeIntent.providerId} overridden for ${activeIntent.ttlMs} ms (reason: "${activeIntent.reason}").`,
    });
  };

  const handleProbeHalfOpen = async (prv: ProviderProfile) => {
    if (busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-breaker-probe");
    const res = await platform.callApi({
      method: "POST",
      path: `/v1/notifications/providers/${encodeURIComponent(prv.provider_id)}/circuit-breaker/probe`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: { reason: "Operator half-open health probe" },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Probe failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setProviderPatches((prev) => ({
      ...prev,
      [prv.provider_id]: {
        ...(prev[prv.provider_id] ?? {}),
        breaker_state: "HALF_OPEN",
        active_override: undefined,
      },
    }));
    appendTransition(prv.provider_id, {
      id: `trn-probe-${Date.now()}`,
      from_state: prv.breaker_state,
      to_state: "HALF_OPEN",
      reason: "Manual probe cleared override and entered HALF_OPEN.",
      occurred_at: new Date().toISOString(),
    });
    setStatusBanner({
      title: "Probe (half-open) triggered",
      detail: `Cleared manual override and transitioned ${prv.provider_key} to HALF_OPEN.`,
    });
  };

  const handleSimulateFailure = async (prv: ProviderProfile) => {
    if (busy || prv.driver_kind !== "mock" || prv.environment === "production") return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-breaker-sim");
    const res = await platform.callApi({
      method: "POST",
      path: `/v1/notifications/providers/${encodeURIComponent(prv.provider_id)}/circuit-breaker/simulate-failure`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: { failure_rate_bps: 8000, duration_ms: 60000, reason: "Non-prod mock failure spike simulation" },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Simulate failure failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setProviderPatches((prev) => ({
      ...prev,
      [prv.provider_id]: {
        ...(prev[prv.provider_id] ?? {}),
        breaker_state: "OPEN",
        error_rate_bps: 8000,
        cooldown_remaining_ms: 60000,
      },
    }));
    appendTransition(prv.provider_id, {
      id: `trn-sim-${Date.now()}`,
      from_state: prv.breaker_state,
      to_state: "OPEN",
      reason: "Simulated mock failure spike (80% error rate).",
      occurred_at: new Date().toISOString(),
    });
    setStatusBanner({
      title: "Mock failure spike simulated (D8)",
      detail: `Updated mock profile ${prv.provider_key} failure rate to 80% for 60s.`,
    });
  };

  if (providersQuery.isLoading) {
    return <LoadingPanel label="Loading circuit breaker states and rolling windows..." />;
  }

  return (
    <Stack spacing={2}>
      {statusBanner && <StatusPanel title={statusBanner.title} detail={statusBanner.detail} />}
      {errorBanner && (
        <ErrorPanel
          title={errorBanner.title}
          detail={errorBanner.detail}
          {...(errorBanner.onRetry ? { onRetry: errorBanner.onRetry } : {})}
        />
      )}

      {/* Per-provider Circuit Breaker Cards */}
      <Stack spacing={1.5}>
        {providers.map((prv) => {
          const canSimulate = prv.driver_kind === "mock" && prv.environment !== "production";
          const failurePct = (prv.error_rate_bps / 100).toFixed(2);
          const slowPct = ((prv.slow_call_rate_bps ?? 0) / 100).toFixed(2);
          const failureThreshPct = ((prv.failure_threshold_bps ?? 500) / 100).toFixed(1);
          const slowThreshPct = ((prv.slow_call_rate_threshold_bps ?? 1000) / 100).toFixed(1);
          const windowSec = Math.round((prv.rolling_window_ms ?? 60000) / 1000);
          const cooldownSec = Math.round((prv.cooldown_remaining_ms ?? 0) / 1000);
          const stateColor =
            prv.breaker_state === "CLOSED" ? "success" : prv.breaker_state === "OPEN" ? "error" : "warning";

          return (
            <Paper
              key={prv.provider_id}
              variant="outlined"
              sx={{
                p: 2,
                borderColor: selectedProvider?.provider_id === prv.provider_id ? "#228255" : "#e2e7e3",
              }}
            >
              <Stack spacing={1.25}>
                <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", md: "center" }} spacing={1}>
                  <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
                    <Typography variant="subtitle1" fontWeight={700}>{prv.name}</Typography>
                    <Chip size="small" color={stateColor} label={`Breaker: ${prv.breaker_state}`} />
                    <Chip size="small" variant="outlined" label={`${prv.channel} · driver:${prv.driver_kind}`} />
                    {prv.active_override && <Chip size="small" color="warning" variant="outlined" label={`Override: ${prv.active_override}`} />}
                  </Stack>

                  <Stack direction="row" spacing={1} flexWrap="wrap">
                    <Button size="small" variant="text" onClick={() => setSelectedProviderId(prv.provider_id)}>
                      Inspect & Override
                    </Button>
                    <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleProbeHalfOpen(prv)}>
                      Probe (half-open)
                    </Button>
                    <Button
                      size="small"
                      variant="outlined"
                      color="warning"
                      disabled={busy || !canSimulate}
                      title={canSimulate ? "Simulate failure spike on mock driver" : "Disabled: only allowed for mock driver in non-production (D8)"}
                      onClick={() => void handleSimulateFailure(prv)}
                    >
                      Simulate failure
                    </Button>
                  </Stack>
                </Stack>

                <Stack direction={{ xs: "column", sm: "row" }} spacing={2} flexWrap="wrap">
                  <Typography variant="caption">
                    <strong>Failure Rate:</strong> {failurePct}% (threshold {failureThreshPct}%)
                  </Typography>
                  <Typography variant="caption">
                    <strong>Slow-Call Rate:</strong> {slowPct}% (threshold {slowThreshPct}% @ {prv.slow_call_threshold_ms ?? 2000}ms)
                  </Typography>
                  <Typography variant="caption">
                    <strong>Window:</strong> {windowSec}s ({prv.rolling_window_ms ?? 60000}ms)
                  </Typography>
                  <Typography variant="caption">
                    <strong>Calls:</strong> {prv.buffered_calls ?? prv.window_requests} buffered / {prv.failed_calls ?? prv.window_failures} failed / {prv.slow_calls ?? 0} slow
                  </Typography>
                  <Typography variant="caption">
                    <strong>Cooldown:</strong> {cooldownSec}s
                  </Typography>
                  <Typography variant="caption">
                    <strong>Fallback:</strong> {prv.fallback_provider ?? "None"}
                  </Typography>
                </Stack>
              </Stack>
            </Paper>
          );
        })}
      </Stack>

      {/* Manual Override Form */}
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Box>
            <Typography variant="h6">Manual Breaker Override (Audited)</Typography>
            <Typography variant="caption" color="text.secondary">
              FORCE_OPEN, FORCE_CLOSE, or DRAINING requires both a mandatory audit reason and bounded ttl_ms (1 to 86,400,000 ms).
            </Typography>
          </Box>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} alignItems="center">
            <FormControl size="small" sx={{ minWidth: 210 }}>
              <InputLabel id="cb-override-provider">Target Provider</InputLabel>
              <Select
                labelId="cb-override-provider"
                label="Target Provider"
                value={selectedProvider?.provider_id ?? ""}
                onChange={(e) => { setSelectedProviderId(e.target.value); setOverrideIntent(null); }}
              >
                {providers.map((prv) => (
                  <MenuItem key={prv.provider_id} value={prv.provider_id}>{prv.name} ({prv.provider_key})</MenuItem>
                ))}
              </Select>
            </FormControl>
            <FormControl size="small" sx={{ minWidth: 165 }}>
              <InputLabel id="cb-override-mode">Override Mode</InputLabel>
              <Select
                labelId="cb-override-mode"
                label="Override Mode"
                value={overrideMode}
                onChange={(e) => { setOverrideMode(e.target.value as BreakerOverrideMode); setOverrideIntent(null); }}
              >
                <MenuItem value="FORCE_OPEN">FORCE_OPEN</MenuItem>
                <MenuItem value="FORCE_CLOSE">FORCE_CLOSE</MenuItem>
                <MenuItem value="DRAINING">DRAINING</MenuItem>
              </Select>
            </FormControl>
            <TextField
              size="small"
              label="Override TTL (ttl_ms)"
              value={overrideTtlMs}
              onChange={(e) => { setOverrideTtlMs(e.target.value); setOverrideIntent(null); }}
              error={!Number.isFinite(parsedTtlMs) || parsedTtlMs <= 0 || parsedTtlMs > MAX_TTL_MS}
              sx={{ width: 170 }}
            />
            <TextField
              size="small"
              fullWidth
              label="Mandatory Override Reason"
              placeholder="Upstream maintenance window..."
              value={overrideReason}
              onChange={(e) => { setOverrideReason(e.target.value); setOverrideIntent(null); }}
            />
            <Button
              variant="contained"
              disabled={busy || (!overrideFormValid && overrideIntent === null)}
              onClick={() => void handleApplyOverride()}
            >
              {overrideIntent ? "Retry Override" : "Apply Breaker Override"}
            </Button>
          </Stack>
        </Stack>
      </Paper>

      {/* Transition History */}
      {selectedProvider && (
        <Paper variant="outlined" sx={{ p: 2 }}>
          <Stack spacing={1}>
            <Typography variant="subtitle1" fontWeight={700}>
              Transition History — {selectedProvider.name}
            </Typography>
            <Divider />
            {transitions.length === 0 ? (
              <Typography variant="caption" color="text.secondary">No recent transitions recorded.</Typography>
            ) : (
              transitions.map((trn) => (
                <Paper key={trn.id} variant="outlined" sx={{ p: 1 }}>
                  <Stack direction={{ xs: "column", sm: "row" }} justifyContent="space-between" spacing={1}>
                    <Stack direction="row" spacing={1} alignItems="center">
                      <Chip size="small" variant="outlined" label={`${trn.from_state} -> ${trn.to_state}`} />
                      <Typography variant="body2">{trn.reason}</Typography>
                    </Stack>
                    <Typography variant="caption" color="text.secondary" sx={{ fontFamily: "Consolas, monospace" }}>
                      {trn.occurred_at}
                    </Typography>
                  </Stack>
                </Paper>
              ))
            )}
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}
