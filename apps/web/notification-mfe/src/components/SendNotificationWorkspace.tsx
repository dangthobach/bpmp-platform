import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { PlatformServices } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
  Divider,
  ErrorPanel,
  FormControl,
  InputLabel,
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
  defaultTemplatesSeed,
  defaultTemplateVersionsSeed,
  mapPriorityLabelToValue,
  schemaJsonToVariables,
  type CommandPair,
  type DryRunPlan,
  type NotificationChannel,
  type NotificationLane,
  type SendPriorityLabel,
  type SendPriorityValue,
} from "../types";

interface SendNotificationBody {
  application_id: string;
  template_key: string;
  schema_major: number;
  category_key: string;
  lane_override: NotificationLane;
  priority: SendPriorityValue;
  dry_run?: boolean;
  locale: string;
  correlation_id?: string;
  recipients: Array<{
    subject_ref?: string;
    external_address?: string;
    channel: NotificationChannel;
    locale: string;
  }>;
  data: Record<string, unknown>;
}

interface SendIntent {
  pair: CommandPair;
  body: SendNotificationBody;
}

interface SubmitReceiptResponse {
  request_id?: string;
  status?: string;
  replayed?: boolean;
  plan?: DryRunPlan;
  deliveries?: Array<{
    delivery_id: string;
    channel: string;
    masked_address: string;
    status: string;
  }>;
}

interface Props {
  platform: PlatformServices;
  initialTemplateKey?: string | undefined;
  initialSchemaMajor?: number | undefined;
  initialChannel?: NotificationChannel | undefined;
}

export function SendNotificationWorkspace({
  platform,
  initialTemplateKey,
  initialSchemaMajor,
  initialChannel,
}: Props): ReactNode {
  const [applicationId, setApplicationId] = useState<string>(defaultApplicationsSeed[0]!.id);
  const [templateKey, setTemplateKey] = useState<string>(initialTemplateKey ?? "case.submitted");
  const [schemaMajor, setSchemaMajor] = useState<number>(initialSchemaMajor ?? 1);
  const [priorityLabel, setPriorityLabel] = useState<SendPriorityLabel>("NORMAL");
  const [recipientMode, setRecipientMode] = useState<"SUBJECT_REF" | "EXTERNAL_ADDRESS">("SUBJECT_REF");
  const [subjectRef, setSubjectRef] = useState("subj:customer:8812");
  const [externalAddress, setExternalAddress] = useState("");
  const [channel, setChannel] = useState<NotificationChannel>(initialChannel ?? "EMAIL");
  const [locale, setLocale] = useState("vi-VN");
  const [correlationId, setCorrelationId] = useState("corr-manual-001");

  const selectedApplication = useMemo(
    () => defaultApplicationsSeed.find((a) => a.id === applicationId) ?? defaultApplicationsSeed[0]!,
    [applicationId],
  );
  const selectedTemplate = useMemo(
    () => defaultTemplatesSeed.find((t) => t.template_key === templateKey) ?? defaultTemplatesSeed[0]!,
    [templateKey],
  );

  const schemaFields = useMemo(() => {
    const versions = defaultTemplateVersionsSeed[selectedTemplate.id] ?? [];
    const activeVer = versions.find((v) => v.status === "PUBLISHED") ?? versions[0];
    return activeVer ? schemaJsonToVariables(activeVer.variable_schema_json) : [];
  }, [selectedTemplate.id]);

  const [variableValues, setVariableValues] = useState<Record<string, string>>({
    caseTrackingCode: "HS-2026-00918",
    customerName: "Nguyen Van A",
  });

  useEffect(() => {
    if (initialTemplateKey) setTemplateKey(initialTemplateKey);
    if (initialSchemaMajor) setSchemaMajor(initialSchemaMajor);
    if (initialChannel) setChannel(initialChannel);
  }, [initialTemplateKey, initialSchemaMajor, initialChannel]);

  useEffect(() => {
    const defaults: Record<string, string> = {};
    for (const f of schemaFields) {
      defaults[f.key] = f.sampleValue;
    }
    if (Object.keys(defaults).length > 0) setVariableValues(defaults);
  }, [schemaFields]);

  const [dryRunPlan, setDryRunPlan] = useState<DryRunPlan | null>(null);
  const [sendIntent, setSendIntent] = useState<SendIntent | null>(null);
  const [receipt, setReceipt] = useState<SubmitReceiptResponse | null>(null);
  const [errorState, setErrorState] = useState<{ title: string; detail: string; retryable: boolean } | null>(null);
  const [busy, setBusy] = useState(false);

  const recipientValid =
    recipientMode === "SUBJECT_REF" ? subjectRef.trim().length > 0 : externalAddress.trim().length > 0;
  const canSubmit = Boolean(templateKey) && recipientValid;

  const buildRequestBody = (dryRun: boolean): SendNotificationBody => {
    const recipientTarget =
      recipientMode === "SUBJECT_REF"
        ? { subject_ref: subjectRef.trim(), channel, locale }
        : { external_address: externalAddress.trim(), channel, locale };

    return {
      application_id: selectedApplication.id,
      template_key: templateKey,
      schema_major: schemaMajor,
      category_key: selectedTemplate.category_id,
      lane_override: selectedApplication.default_lane,
      priority: mapPriorityLabelToValue(priorityLabel),
      ...(dryRun ? { dry_run: true } : {}),
      locale,
      ...(correlationId.trim() ? { correlation_id: correlationId.trim() } : {}),
      recipients: [recipientTarget],
      data: { ...variableValues },
    };
  };

  const handleDryRun = async () => {
    if (!canSubmit || busy) return;
    setBusy(true);
    setErrorState(null);
    const body = buildRequestBody(true);
    const res = await platform.callApi<SubmitReceiptResponse, SendNotificationBody>({
      method: "POST",
      path: "/v1/notifications/send",
      ...(body.correlation_id ? { correlationId: body.correlation_id } : {}),
      body,
    });
    setBusy(false);
    if (!res.ok) {
      setErrorState({
        title: res.problem.title || "Dry-run planning failed",
        detail: `Code: ${res.problem.code}`,
        retryable: false,
      });
      return;
    }
    const plan: DryRunPlan = res.data.plan ?? {
      template_version_id: selectedTemplate.active_release?.template_version_id ?? "ver-case-v1",
      release_ordinal: selectedTemplate.active_release?.ordinal ?? 1,
      variant: { channel, locale, variant_kind: channel === "ZNS" ? "PROVIDER_TEMPLATE" : "INLINE" },
      schema_errors: [],
      quota: {
        remaining: Math.max(0, selectedApplication.quota.per_day - selectedApplication.usage_today),
        limit: selectedApplication.quota.per_day,
      },
      routing: {
        candidates: [
          { provider: channel === "SMS" ? "vnpt-sms-brandname" : "ses-smtp-primary", breaker_state: channel === "SMS" ? "HALF_OPEN" : "CLOSED" },
          { provider: channel === "SMS" ? "viettel-brandname-fallback" : "smtp-internal-backup", breaker_state: "CLOSED" },
        ],
        selected: channel === "SMS" ? "viettel-brandname-fallback" : "ses-smtp-primary",
        ...(channel === "SMS" ? { failover_reason: "Primary vnpt-sms-brandname breaker is HALF_OPEN; failing over to Tier-2" } : {}),
      },
      warnings: [],
    };
    setDryRunPlan(plan);
  };

  const submitManualNotification = async (overrideIntent?: SendIntent) => {
    if (busy) return;
    let activeIntent = overrideIntent ?? sendIntent;
    if (!activeIntent) {
      if (!canSubmit) return;
      activeIntent = {
        pair: createCommandPair("ntf-send"),
        body: buildRequestBody(false),
      };
      setSendIntent(activeIntent);
    }

    setBusy(true);
    setErrorState(null);
    const res = await platform.callApi<SubmitReceiptResponse, SendNotificationBody>({
      method: "POST",
      path: "/v1/notifications/send",
      commandId: activeIntent.pair.commandId,
      idempotencyKey: activeIntent.pair.idempotencyKey,
      ...(activeIntent.body.correlation_id ? { correlationId: activeIntent.body.correlation_id } : {}),
      body: activeIntent.body,
    });
    setBusy(false);

    if (!res.ok) {
      if (!res.problem.retryable) setSendIntent(null);
      setErrorState({
        title: res.problem.title || "Notification submission failed",
        detail: `Code: ${res.problem.code}; command: ${activeIntent.pair.commandId}`,
        retryable: res.problem.retryable,
      });
      return;
    }

    setReceipt({
      request_id: res.data.request_id ?? activeIntent.pair.commandId,
      status: res.data.status ?? "ACCEPTED",
      ...(res.data.replayed !== undefined ? { replayed: res.data.replayed } : {}),
      ...(res.data.deliveries ? { deliveries: res.data.deliveries } : {}),
    });
    setSendIntent(null);
  };

  const formLockedByRetryIntent = sendIntent !== null;

  return (
    <Stack spacing={2}>
      {receipt && (
        <StatusPanel
          title={`Notification request ${receipt.status ?? "ACCEPTED"}`}
          detail={`Request ID: ${receipt.request_id ?? "accepted"}${receipt.replayed ? " (idempotent replay)" : ""}. Queued asynchronously on lane ${selectedApplication.default_lane} with priority ${priorityLabel} (${mapPriorityLabelToValue(priorityLabel)}).`}
        />
      )}

      {errorState && (
        <ErrorPanel
          title={errorState.title}
          detail={errorState.detail}
          {...(errorState.retryable && sendIntent ? { onRetry: () => { void submitManualNotification(sendIntent); } } : {})}
        />
      )}

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={2}>
          <Box>
            <Typography variant="h6">Send Notification (Dry-Run & Idempotent Dispatch)</Typography>
            <Typography variant="caption" color="text.secondary">
              Producers supply template_key@major, recipient, priority (1..3 within lane, D4), and schema variables only — subject/content overrides are prohibited (D5).
            </Typography>
          </Box>

          <Stack direction={{ xs: "column", md: "row" }} spacing={1.5} alignItems="center">
            <FormControl size="small" sx={{ minWidth: 210 }}>
              <InputLabel id="send-app-select">Application</InputLabel>
              <Select
                labelId="send-app-select"
                label="Application"
                value={applicationId}
                disabled={busy || formLockedByRetryIntent}
                onChange={(e) => setApplicationId(e.target.value)}
              >
                {defaultApplicationsSeed.map((app) => (
                  <MenuItem key={app.id} value={app.id}>{app.name} ({app.application_key})</MenuItem>
                ))}
              </Select>
            </FormControl>

            <FormControl size="small" sx={{ minWidth: 220 }}>
              <InputLabel id="send-template-select">Template</InputLabel>
              <Select
                labelId="send-template-select"
                label="Template"
                value={templateKey}
                disabled={busy || formLockedByRetryIntent}
                onChange={(e) => setTemplateKey(e.target.value)}
              >
                {defaultTemplatesSeed.map((tmpl) => (
                  <MenuItem key={tmpl.id} value={tmpl.template_key}>{tmpl.template_key} — {tmpl.name}</MenuItem>
                ))}
              </Select>
            </FormControl>

            <FormControl size="small" sx={{ minWidth: 155 }}>
              <InputLabel id="send-priority-select">Priority (D4)</InputLabel>
              <Select
                labelId="send-priority-select"
                label="Priority (D4)"
                value={priorityLabel}
                disabled={busy || formLockedByRetryIntent}
                onChange={(e) => setPriorityLabel(e.target.value as SendPriorityLabel)}
              >
                <MenuItem value="NORMAL">NORMAL (1)</MenuItem>
                <MenuItem value="HIGH">HIGH (2)</MenuItem>
                <MenuItem value="URGENT">URGENT (3)</MenuItem>
              </Select>
            </FormControl>

            <Chip size="small" variant="outlined" label={`Lane: ${selectedApplication.default_lane}`} />
            <Chip size="small" variant="outlined" label={`Category: ${selectedTemplate.category_id}`} />
          </Stack>

          <Stack direction={{ xs: "column", md: "row" }} spacing={1.5} alignItems="center">
            <FormControl size="small" sx={{ minWidth: 185 }}>
              <InputLabel id="send-recipient-mode">Recipient Mode</InputLabel>
              <Select
                labelId="send-recipient-mode"
                label="Recipient Mode"
                value={recipientMode}
                disabled={busy || formLockedByRetryIntent}
                onChange={(e) => setRecipientMode(e.target.value as "SUBJECT_REF" | "EXTERNAL_ADDRESS")}
              >
                <MenuItem value="SUBJECT_REF">Internal subject_ref</MenuItem>
                <MenuItem value="EXTERNAL_ADDRESS">External address</MenuItem>
              </Select>
            </FormControl>

            {recipientMode === "SUBJECT_REF" ? (
              <TextField
                size="small"
                fullWidth
                label="Recipient subject_ref"
                value={subjectRef}
                onChange={(e) => setSubjectRef(e.target.value)}
                disabled={busy || formLockedByRetryIntent}
              />
            ) : (
              <TextField
                size="small"
                fullWidth
                label="External Recipient Address"
                placeholder="customer@example.vn or +84901234567"
                value={externalAddress}
                onChange={(e) => setExternalAddress(e.target.value)}
                disabled={busy || formLockedByRetryIntent}
              />
            )}

            <FormControl size="small" sx={{ minWidth: 130 }}>
              <InputLabel id="send-channel-select">Channel</InputLabel>
              <Select
                labelId="send-channel-select"
                label="Channel"
                value={channel}
                disabled={busy || formLockedByRetryIntent}
                onChange={(e) => setChannel(e.target.value as NotificationChannel)}
              >
                {(["EMAIL", "SMS", "PUSH", "IN_APP", "ZNS", "WEBHOOK"] as NotificationChannel[]).map((ch) => (
                  <MenuItem key={ch} value={ch}>{ch}</MenuItem>
                ))}
              </Select>
            </FormControl>

            <TextField
              size="small"
              label="Correlation ID"
              value={correlationId}
              onChange={(e) => setCorrelationId(e.target.value)}
              disabled={busy || formLockedByRetryIntent}
              sx={{ minWidth: 180 }}
            />
          </Stack>

          <Divider />

          {/* Variable Form generated from template variable_schema (D5: no subject/content override) */}
          <Stack spacing={1.25}>
            <Typography variant="subtitle2">Template Variables (from variable_schema)</Typography>
            <Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} flexWrap="wrap">
              {schemaFields.map((field) => (
                <TextField
                  key={field.key}
                  size="small"
                  label={`${field.label}${field.required ? " *" : ""}`}
                  value={variableValues[field.key] ?? ""}
                  onChange={(e) => setVariableValues((prev) => ({ ...prev, [field.key]: e.target.value }))}
                  disabled={busy || formLockedByRetryIntent}
                  sx={{ minWidth: 220 }}
                />
              ))}
            </Stack>
          </Stack>

          <Stack direction="row" spacing={1.5} justifyContent="space-between" alignItems="center" flexWrap="wrap">
            <Typography variant="caption" color="text.secondary">
              {sendIntent
                ? `Pending retry preserves Idempotency-Key ${sendIntent.pair.idempotencyKey}`
                : "Run Dry-Run to inspect quota, schema validation, and circuit-breaker routing before dispatching."}
            </Typography>
            <Stack direction="row" spacing={1}>
              {sendIntent && (
                <Button variant="outlined" disabled={busy} onClick={() => { setSendIntent(null); setErrorState(null); }}>
                  Discard Pending Retry
                </Button>
              )}
              <Button variant="outlined" disabled={busy || !canSubmit || formLockedByRetryIntent} onClick={() => void handleDryRun()}>
                Dry-Run Plan
              </Button>
              <Button variant="contained" disabled={busy || (!canSubmit && !sendIntent)} onClick={() => void submitManualNotification()}>
                {sendIntent ? "Retry Same Send Request" : "Send Notification"}
              </Button>
            </Stack>
          </Stack>
        </Stack>
      </Paper>

      {/* Dry-Run Routing / Quota / Breaker Plan */}
      {dryRunPlan && (
        <Paper variant="outlined" sx={{ p: 2, bgcolor: "#f6faf7", borderColor: "#b9dfca" }}>
          <Stack spacing={1}>
            <Stack direction="row" justifyContent="space-between" alignItems="center">
              <Typography variant="subtitle1" fontWeight={700}>Dry-Run Execution Plan (No DB Write / No Enqueue)</Typography>
              <Chip
                size="small"
                color={dryRunPlan.schema_errors.length === 0 ? "success" : "error"}
                label={dryRunPlan.schema_errors.length === 0 ? "Schema Valid" : `${dryRunPlan.schema_errors.length} Schema Error(s)`}
              />
            </Stack>
            <Typography variant="body2">
              <strong>Resolved Release:</strong> {dryRunPlan.template_version_id} (ordinal {dryRunPlan.release_ordinal}) • Variant: {dryRunPlan.variant.channel}/{dryRunPlan.variant.locale} ({dryRunPlan.variant.variant_kind})
            </Typography>
            <Typography variant="body2">
              <strong>Quota Remaining:</strong> {dryRunPlan.quota.remaining.toLocaleString()} / {dryRunPlan.quota.limit.toLocaleString()} daily
            </Typography>
            <Typography variant="body2">
              <strong>Selected Provider:</strong> {dryRunPlan.routing.selected}
              {dryRunPlan.routing.failover_reason ? ` — Failover: ${dryRunPlan.routing.failover_reason}` : ""}
            </Typography>
            <Stack direction="row" spacing={1} flexWrap="wrap">
              {dryRunPlan.routing.candidates.map((cand) => (
                <Chip
                  key={cand.provider}
                  size="small"
                  color={cand.breaker_state === "CLOSED" ? "success" : cand.breaker_state === "OPEN" ? "error" : "warning"}
                  variant="outlined"
                  label={`${cand.provider} (${cand.breaker_state})`}
                />
              ))}
            </Stack>
            {dryRunPlan.warnings.length > 0 && (
              <Typography variant="caption" color="warning.main">
                Warnings: {dryRunPlan.warnings.join("; ")}
              </Typography>
            )}
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}
