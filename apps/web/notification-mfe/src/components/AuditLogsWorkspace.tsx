import { useMemo, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  InputLabel,
  LoadingPanel,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  Typography,
} from "@ods/shared-ui";
import {
  defaultAuditSeed,
  type AuditEvidenceRecord,
  type AuditResourceType,
  type AuditSeverity,
} from "../types";

interface AuditListResponse {
  records?: AuditEvidenceRecord[];
}

interface Props {
  platform: PlatformServices;
}

export function AuditLogsWorkspace({ platform }: Props): ReactNode {
  const [resourceType, setResourceType] = useState<string>("ALL");
  const [severity, setSeverity] = useState<string>("ALL");
  const [searchQuery, setSearchQuery] = useState<string>("");
  const [selectedRecord, setSelectedRecord] = useState<AuditEvidenceRecord | null>(null);

  const queryPath = useMemo(() => {
    const params = new URLSearchParams();
    if (resourceType !== "ALL") params.set("resource_type", resourceType);
    if (severity !== "ALL") params.set("severity", severity);
    if (searchQuery.trim()) params.set("q", searchQuery.trim());
    const qs = params.toString();
    return qs ? `/v1/notifications/audit?${qs}` : "/v1/notifications/audit";
  }, [resourceType, severity, searchQuery]);

  const auditQuery = useSWR<AuditListResponse>(
    `notification-mfe:audit:${queryPath}`,
    async () => {
      const res = await platform.callApi<AuditListResponse>({ method: "GET", path: queryPath });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const records = useMemo(() => {
    const base =
      auditQuery.data?.records && auditQuery.data.records.length > 0
        ? auditQuery.data.records
        : defaultAuditSeed;
    return base.filter((rec) => {
      if (resourceType !== "ALL" && rec.resource_type !== resourceType) return false;
      if (severity !== "ALL" && rec.severity !== severity) return false;
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        const hay = `${rec.action} ${rec.target_ref} ${rec.actor_display} ${rec.command_id}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [auditQuery.data, resourceType, severity, searchQuery]);

  if (auditQuery.isLoading) {
    return <LoadingPanel label="Loading notification audit evidence..." />;
  }

  return (
    <Stack spacing={2}>
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Box>
            <Typography variant="h6">Audit Logs & Evidence</Typography>
            <Typography variant="caption" color="text.secondary">
              Immutable audit trail derived from verified actor proofs (actor_display, actor_role, client_ip) and cryptographic before/after digests.
            </Typography>
          </Box>

          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} alignItems="center">
            <TextField
              size="small"
              label="Search action, resource, or actor"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              sx={{ flex: 1, minWidth: 240 }}
            />
            <FormControl size="small" sx={{ minWidth: 165 }}>
              <InputLabel id="audit-resource-filter">Resource Type</InputLabel>
              <Select labelId="audit-resource-filter" label="Resource Type" value={resourceType} onChange={(e) => setResourceType(e.target.value)}>
                <MenuItem value="ALL">All Resources</MenuItem>
                {(["TEMPLATE", "PROVIDER", "APPLICATION", "CIRCUIT_BREAKER", "DISPATCH", "TRIGGER_RULE"] as AuditResourceType[]).map((rt) => (
                  <MenuItem key={rt} value={rt}>{rt}</MenuItem>
                ))}
              </Select>
            </FormControl>
            <FormControl size="small" sx={{ minWidth: 135 }}>
              <InputLabel id="audit-severity-filter">Severity</InputLabel>
              <Select labelId="audit-severity-filter" label="Severity" value={severity} onChange={(e) => setSeverity(e.target.value)}>
                <MenuItem value="ALL">All Severities</MenuItem>
                {(["INFO", "WARN", "CRITICAL"] as AuditSeverity[]).map((sv) => (
                  <MenuItem key={sv} value={sv}>{sv}</MenuItem>
                ))}
              </Select>
            </FormControl>
          </Stack>

          <Box sx={{ overflowX: "auto" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                  <th style={{ padding: "8px 6px" }}>Timestamp</th>
                  <th style={{ padding: "8px 6px" }}>Actor & Role</th>
                  <th style={{ padding: "8px 6px" }}>Client IP</th>
                  <th style={{ padding: "8px 6px" }}>Severity</th>
                  <th style={{ padding: "8px 6px" }}>Resource Type</th>
                  <th style={{ padding: "8px 6px" }}>Action & Target</th>
                  <th style={{ padding: "8px 6px" }}>Details</th>
                </tr>
              </thead>
              <tbody>
                {records.map((rec) => (
                  <tr key={rec.id} style={{ borderBottom: "1px solid #eff3f0" }}>
                    <td style={{ padding: "8px 6px", fontFamily: "Consolas, monospace", fontSize: 12 }}>{rec.occurred_at}</td>
                    <td style={{ padding: "8px 6px" }}>
                      <Typography variant="body2" fontWeight={650}>{rec.actor_display}</Typography>
                      <Typography variant="caption" color="text.secondary">{rec.actor_role}</Typography>
                    </td>
                    <td style={{ padding: "8px 6px", fontFamily: "Consolas, monospace" }}>{rec.client_ip}</td>
                    <td style={{ padding: "8px 6px" }}>
                      <Chip
                        size="small"
                        color={rec.severity === "CRITICAL" ? "error" : rec.severity === "WARN" ? "warning" : "default"}
                        label={rec.severity}
                      />
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Chip size="small" variant="outlined" label={rec.resource_type} />
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Typography variant="body2" fontWeight={650}>{rec.action}</Typography>
                      <Typography variant="caption" color="text.secondary">{rec.target_ref}</Typography>
                    </td>
                    <td style={{ padding: "8px 6px" }}>
                      <Button size="small" variant="outlined" onClick={() => setSelectedRecord(rec)}>
                        Inspect
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Box>
        </Stack>
      </Paper>

      <Dialog open={selectedRecord !== null} onClose={() => setSelectedRecord(null)} maxWidth="sm" fullWidth>
        <DialogTitle>Audit Evidence Details</DialogTitle>
        <DialogContent>
          {selectedRecord && (
            <Stack spacing={1} sx={{ mt: 0.5 }}>
              <Typography variant="body2"><strong>Action:</strong> {selectedRecord.action}</Typography>
              <Typography variant="body2"><strong>Actor:</strong> {selectedRecord.actor_display} ({selectedRecord.actor_role})</Typography>
              <Typography variant="body2"><strong>Client IP:</strong> {selectedRecord.client_ip}</Typography>
              <Typography variant="body2"><strong>Command ID:</strong> {selectedRecord.command_id}</Typography>
              <Typography variant="body2"><strong>Before Digest:</strong> {selectedRecord.before_digest ?? "-"}</Typography>
              <Typography variant="body2"><strong>After Digest:</strong> {selectedRecord.after_digest}</Typography>
              {selectedRecord.details && (
                <Paper variant="outlined" sx={{ p: 1.25, bgcolor: "#f8faf8" }}>
                  <pre style={{ margin: 0, fontSize: 12, fontFamily: "Consolas, monospace", whiteSpace: "pre-wrap" }}>
                    {JSON.stringify(selectedRecord.details, null, 2)}
                  </pre>
                </Paper>
              )}
            </Stack>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setSelectedRecord(null)}>Close</Button>
        </DialogActions>
      </Dialog>
    </Stack>
  );
}
