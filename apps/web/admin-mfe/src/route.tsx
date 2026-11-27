import { useCallback, useEffect, useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Box, Chip, ErrorPanel, Page, Paper, Stack, Typography } from "@ods/shared-ui";

interface DatasetSummary {
  id: string;
  code: string;
  name: string;
  current_publication_version: number;
  version: number;
}

interface AuditEntry {
  event_id: string;
  source_service: string;
  aggregate_type: string;
  aggregate_id: string;
  action: string;
  actor_id: string;
  occurred_at: string;
  projection_generation: number;
}

interface AuditSearchResponse {
  entries: AuditEntry[];
  projection_generation: number;
  as_of: string;
}

function AdminCommonWorkspace({ platform }: RemoteRouteProps): ReactNode {
  const [datasets, setDatasets] = useState<DatasetSummary[]>([]);
  const [audit, setAudit] = useState<AuditSearchResponse | null>(null);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    const [mdmRes, auditRes] = await Promise.all([
      platform.callApi<{ datasets: DatasetSummary[] }>({ method: "GET", path: "/v1/mdm/datasets" }),
      platform.callApi<AuditSearchResponse>({ method: "GET", path: "/v1/audit/entries?limit=25" }),
    ]);
    if (!mdmRes.ok) {
      setError(mdmRes.problem.detail || mdmRes.problem.title);
      return;
    }
    if (!auditRes.ok) {
      setError(auditRes.problem.detail || auditRes.problem.title);
      return;
    }
    setDatasets(mdmRes.data.datasets ?? []);
    setAudit(auditRes.data);
    setError("");
  }, [platform]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <Page title="ODS Common: Master Data & Audit Projection">
      <Stack spacing={2.5}>
        {error ? <ErrorPanel title="Admin load failed" detail={error} onRetry={() => void refresh()} /> : null}

        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Typography variant="h6" sx={{ mb: 1.5 }}>Versioned Master Data Sets ({datasets.length})</Typography>
          <Stack spacing={1}>
            {datasets.length === 0 ? (
              <Typography color="text.secondary">No master data sets published yet.</Typography>
            ) : datasets.map((ds) => (
              <Box key={ds.id} sx={{ p: 1.5, border: "1px solid #e2e7e3", borderRadius: 1.5, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <Box>
                  <Typography fontWeight={700}>{ds.name} ({ds.code})</Typography>
                  <Typography variant="caption" color="text.secondary">CAS version {ds.version}</Typography>
                </Box>
                <Chip size="small" color="primary" label={`Published v${ds.current_publication_version}`} />
              </Box>
            ))}
          </Stack>
        </Paper>

        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 1.5 }}>
            <Typography variant="h6">Rebuildable Audit Projection</Typography>
            {audit ? (
              <Typography variant="caption" color="text.secondary">
                Generation #{audit.projection_generation} · As of {audit.as_of}
              </Typography>
            ) : null}
          </Stack>
          <Stack spacing={1}>
            {(audit?.entries ?? []).length === 0 ? (
              <Typography color="text.secondary">No projected audit events.</Typography>
            ) : (audit?.entries ?? []).map((entry) => (
              <Box key={entry.event_id} sx={{ p: 1.5, border: "1px solid #e2e7e3", borderRadius: 1.5 }}>
                <Typography fontWeight={700}>{entry.action} — {entry.aggregate_type}:{entry.aggregate_id}</Typography>
                <Typography variant="caption" color="text.secondary">
                  Source: {entry.source_service} · Actor: {entry.actor_id} · {entry.occurred_at}
                </Typography>
              </Box>
            ))}
          </Stack>
        </Paper>
      </Stack>
    </Page>
  );
}

export const contractVersion = "1.0";
export const route = { path: "/admin", title: "Admin & MDM", Component: AdminCommonWorkspace };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
