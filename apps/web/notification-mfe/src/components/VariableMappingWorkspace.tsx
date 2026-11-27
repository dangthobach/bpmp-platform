import { useEffect, useMemo, useState, type ReactNode } from "react";
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
  Typography,
} from "@ods/shared-ui";
import {
  createCommandPair,
  defaultVariableMappingSeed,
  type VariableMappingLink,
  type VariableMappingResponse,
} from "../types";

interface Props {
  platform: PlatformServices;
  onRiskCountChange?: ((count: number) => void) | undefined;
}

export function VariableMappingWorkspace({ platform, onRiskCountChange }: Props): ReactNode {
  const mappingQuery = useSWR<VariableMappingResponse>(
    "notification-mfe:variable-mapping",
    async () => {
      const res = await platform.callApi<VariableMappingResponse>({
        method: "GET",
        path: "/v1/notifications/variable-mapping",
      });
      if (!res.ok) return defaultVariableMappingSeed;
      return res.data?.links ? res.data : defaultVariableMappingSeed;
    },
    { shouldRetryOnError: false },
  );

  const data = mappingQuery.data ?? defaultVariableMappingSeed;
  const [filterMode, setFilterMode] = useState<"ALL" | "RISKS" | "HEALTHY">("ALL");
  const [showUnused, setShowUnused] = useState<boolean>(false);
  const [focusProcess, setFocusProcess] = useState<string>("ALL");
  const [resolvedLinkIds, setResolvedLinkIds] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [statusBanner, setStatusBanner] = useState<{ title: string; detail: string } | null>(null);
  const [errorBanner, setErrorBanner] = useState<{ title: string; detail: string } | null>(null);

  const effectiveLinks = useMemo(() => {
    return data.links.map((link) =>
      resolvedLinkIds.includes(link.id) ? { ...link, severity: "OK" as const, reason: "OK" as const } : link,
    );
  }, [data.links, resolvedLinkIds]);

  useEffect(() => {
    const riskCount = effectiveLinks.filter((l) => l.severity === "WARN" || l.severity === "CRITICAL").length;
    onRiskCountChange?.(riskCount);
  }, [effectiveLinks, onRiskCountChange]);

  const filteredLinks = useMemo(() => {
    return effectiveLinks.filter((link) => {
      if (!showUnused && link.reason === "UNUSED_EXPORT") return false;
      if (focusProcess !== "ALL" && link.process_key !== focusProcess) return false;
      if (filterMode === "RISKS" && link.severity === "OK") return false;
      if (filterMode === "HEALTHY" && link.severity !== "OK") return false;
      return true;
    });
  }, [effectiveLinks, showUnused, focusProcess, filterMode]);

  const handleDraftDeclareInTemplate = async (link: VariableMappingLink) => {
    if (busy || !link.template_id) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-varmap-declare");
    const res = await platform.callApi({
      method: "PUT",
      path: `/v1/notifications/templates/${encodeURIComponent(link.template_id)}/versions/draft`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        schema_major: link.schema_major,
        syntax: "handlebars/v1",
        variable_schema_json: JSON.stringify({
          type: "object",
          properties: { [link.variable_key]: { type: "string", title: link.variable_key } },
        }),
        change_note: `Draft fix: declare ${link.variable_key} from ${link.source_path ?? `vars.${link.variable_key}`}`,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Draft update failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setResolvedLinkIds((prev) => [...prev, link.id]);
    setStatusBanner({
      title: "Template draft updated (not published)",
      detail: `Declared "${link.variable_key}" in ${link.template_key}@${link.schema_major} draft. Review and publish in Templates.`,
    });
  };

  const handleDraftRemapVariable = async (link: VariableMappingLink) => {
    if (busy || !link.template_id) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-varmap-remap");
    const res = await platform.callApi({
      method: "PUT",
      path: `/v1/notifications/templates/${encodeURIComponent(link.template_id)}/versions/draft`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        schema_major: link.schema_major,
        syntax: "handlebars/v1",
        variable_schema_json: JSON.stringify({
          type: "object",
          properties: {
            [link.variable_key]: { type: "string", format: link.actual_type ?? "decimal", title: link.variable_key },
          },
        }),
        change_note: `Draft fix: align ${link.variable_key} type to ${link.actual_type ?? "decimal"}`,
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Draft remap failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setResolvedLinkIds((prev) => [...prev, link.id]);
    setStatusBanner({
      title: "Template draft variable remapped (draft-only)",
      detail: `Aligned "${link.variable_key}" in ${link.template_key} draft to match context export type.`,
    });
  };

  const handleDraftTriggerRuleFix = async (link: VariableMappingLink, mode: "EXPORT_FIELD" | "LINK_TEMPLATE") => {
    if (busy || !link.rule_id) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-rule-draft");
    const proc = data.processes.find((p) => p.rule_id === link.rule_id);
    const res = await platform.callApi({
      method: "PUT",
      path: `/v1/notifications/trigger-rules/${encodeURIComponent(link.rule_id)}/revisions`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: {
        expected_version: proc?.expected_version ?? 1,
        template_key: link.template_key || "loan.approved",
        schema_major: link.schema_major || 1,
        exported_variables:
          mode === "EXPORT_FIELD"
            ? [{ key: link.variable_key, source_path: `vars.${link.variable_key}`, type: link.expected_type ?? "string" }]
            : proc?.exported_variables ?? [],
      },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Trigger rule draft failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    setResolvedLinkIds((prev) => [...prev, link.id]);
    setStatusBanner({
      title: "Trigger-rule draft revision saved (not activated)",
      detail:
        mode === "EXPORT_FIELD"
          ? `Added exported field vars.${link.variable_key} to trigger-rule ${link.rule_id} draft.`
          : `Linked process ${link.process_key} to template draft in rule ${link.rule_id}.`,
    });
  };

  if (mappingQuery.isLoading) {
    return <LoadingPanel label="Analyzing Process Context -> Template variable dependencies..." />;
  }

  const criticalCount = effectiveLinks.filter((l) => l.severity === "CRITICAL").length;
  const warnCount = effectiveLinks.filter((l) => l.severity === "WARN").length;
  const okCount = effectiveLinks.filter((l) => l.severity === "OK" && l.reason !== "UNUSED_EXPORT").length;

  return (
    <Stack spacing={2}>
      {statusBanner && <StatusPanel title={statusBanner.title} detail={statusBanner.detail} />}
      {errorBanner && <ErrorPanel title={errorBanner.title} detail={errorBanner.detail} />}

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", md: "center" }} spacing={1}>
            <Box>
              <Typography variant="h6">Variable Mapping & Process Context Dependencies</Typography>
              <Typography variant="caption" color="text.secondary">
                Process Context (vars.&lt;name&gt;) → Trigger Rule → Template Schema. All remediation actions create drafts only (never implicit publish).
              </Typography>
            </Box>
            <Stack direction="row" spacing={1}>
              <Chip size="small" color="error" label={`CRITICAL: ${criticalCount}`} />
              <Chip size="small" color="warning" label={`WARN: ${warnCount}`} />
              <Chip size="small" color="success" label={`OK: ${okCount}`} />
            </Stack>
          </Stack>

          <Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} alignItems="center" flexWrap="wrap">
            {(["ALL", "RISKS", "HEALTHY"] as const).map((mode) => (
              <Button key={mode} size="small" variant={filterMode === mode ? "contained" : "outlined"} onClick={() => setFilterMode(mode)}>
                {mode === "ALL" ? "All Links" : mode === "RISKS" ? "Risks Only" : "Healthy Only"}
              </Button>
            ))}
            <FormControl size="small" sx={{ minWidth: 210 }}>
              <InputLabel id="varmap-focus-process">Focus Process Node</InputLabel>
              <Select labelId="varmap-focus-process" label="Focus Process Node" value={focusProcess} onChange={(e) => setFocusProcess(e.target.value)}>
                <MenuItem value="ALL">All Processes</MenuItem>
                {data.processes.map((proc) => (
                  <MenuItem key={proc.process_key} value={proc.process_key}>
                    {proc.process_name} ({proc.process_key})
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
            <Button size="small" variant={showUnused ? "contained" : "outlined"} onClick={() => setShowUnused((v) => !v)}>
              {showUnused ? "Hide Unused Exports" : "Show Unused Exports"}
            </Button>
          </Stack>
        </Stack>
      </Paper>

      {/* Dependency Rows */}
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Box sx={{ overflowX: "auto" }}>
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                <th style={{ padding: "8px 6px" }}>Process Context</th>
                <th style={{ padding: "8px 6px" }}>Source Path</th>
                <th style={{ padding: "8px 6px" }}>Template</th>
                <th style={{ padding: "8px 6px" }}>Variable</th>
                <th style={{ padding: "8px 6px" }}>Severity / Reason</th>
                <th style={{ padding: "8px 6px" }}>Draft-Only Fix Action</th>
              </tr>
            </thead>
            <tbody>
              {filteredLinks.map((link) => (
                <tr key={link.id} style={{ borderBottom: "1px solid #eff3f0" }}>
                  <td style={{ padding: "8px 6px" }}>
                    <Typography variant="body2" fontWeight={650}>{link.process_key}</Typography>
                    <Typography variant="caption" color="text.secondary">{link.trigger_kind}</Typography>
                  </td>
                  <td style={{ padding: "8px 6px", fontFamily: "Consolas, monospace" }}>
                    {link.source_path ?? `vars.${link.variable_key}`}
                  </td>
                  <td style={{ padding: "8px 6px" }}>
                    {link.template_key ? `${link.template_key}@${link.schema_major}` : <em>(unbound)</em>}
                  </td>
                  <td style={{ padding: "8px 6px", fontFamily: "Consolas, monospace" }}>
                    {link.variable_key}
                    {link.expected_type && (
                      <Typography variant="caption" color="text.secondary" display="block">
                        expected: {link.expected_type}{link.actual_type ? ` / actual: ${link.actual_type}` : ""}
                      </Typography>
                    )}
                  </td>
                  <td style={{ padding: "8px 6px" }}>
                    <Stack direction="row" spacing={0.5} alignItems="center">
                      <Chip
                        size="small"
                        color={link.severity === "CRITICAL" ? "error" : link.severity === "WARN" ? "warning" : "success"}
                        label={link.severity}
                      />
                      <Chip size="small" variant="outlined" label={link.reason} />
                    </Stack>
                  </td>
                  <td style={{ padding: "8px 6px" }}>
                    {link.reason === "MISSING_IN_CONTEXT" && (
                      <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleDraftTriggerRuleFix(link, "EXPORT_FIELD")}>
                        Draft: Add Exported Field
                      </Button>
                    )}
                    {link.reason === "TYPE_MISMATCH" && (
                      <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleDraftRemapVariable(link)}>
                        Draft: Remap Variable
                      </Button>
                    )}
                    {link.reason === "UNDECLARED_IN_TEMPLATE" && (
                      <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleDraftDeclareInTemplate(link)}>
                        Draft: Declare in Template
                      </Button>
                    )}
                    {link.reason === "UNBOUND_PROCESS" && (
                      <Button size="small" variant="outlined" disabled={busy} onClick={() => void handleDraftTriggerRuleFix(link, "LINK_TEMPLATE")}>
                        Draft: Link Template
                      </Button>
                    )}
                    {(link.reason === "OK" || link.reason === "UNUSED_EXPORT") && (
                      <Typography variant="caption" color="text.secondary">No action required</Typography>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Box>
      </Paper>
    </Stack>
  );
}
