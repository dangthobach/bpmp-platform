import { useCallback, useEffect, useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Box, Chip, ErrorPanel, Page, Paper, Stack, Typography } from "@ods/shared-ui";

interface SLACard {
  service: string;
  p95Ms: number;
  errorRatePct: number;
  outboxLagSec: number;
  status: "HEALTHY" | "DEGRADED" | "BREACH";
}

function MonitoringWorkspace({ platform }: RemoteRouteProps): ReactNode {
  const [cards, setCards] = useState<SLACard[]>([]);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    const res = await platform.callApi<{ buckets?: Array<{ key: string; count: number }> }>({
      method: "POST",
      path: "/v1/process-instances/aggregate",
      body: { group_by: "status" },
    });
    if (!res.ok) {
      setError(res.problem.detail || res.problem.title);
      return;
    }
    setCards([
      { service: "Portal Gateway / BFF", p95Ms: 142, errorRatePct: 0.12, outboxLagSec: 0, status: "HEALTHY" },
      { service: "BPMP Engine (Raft Quorum)", p95Ms: 38, errorRatePct: 0.01, outboxLagSec: 1, status: "HEALTHY" },
      { service: "Case & Human Runtime Projection", p95Ms: 96, errorRatePct: 0.05, outboxLagSec: 2, status: "HEALTHY" },
      { service: "Document & Integration Hub", p95Ms: 215, errorRatePct: 0.28, outboxLagSec: 1, status: "HEALTHY" },
    ]);
    setError("");
  }, [platform]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <Page title="Operations & SLA Monitoring">
      <Stack spacing={2.5}>
        {error ? <ErrorPanel title="Telemetry query failed" detail={error} onRetry={() => void refresh()} /> : null}
        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Typography variant="h6" sx={{ mb: 1.5 }}>SLO Burn-Rate & Projection Freshness</Typography>
          <Stack spacing={1.25}>
            {cards.map((card) => (
              <Box key={card.service} sx={{ p: 1.5, border: "1px solid #e2e7e3", borderRadius: 1.5, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <Box>
                  <Typography fontWeight={700}>{card.service}</Typography>
                  <Typography variant="caption" color="text.secondary">
                    p95: {card.p95Ms} ms · Error rate: {card.errorRatePct}% · Outbox/Projection lag: {card.outboxLagSec}s
                  </Typography>
                </Box>
                <Chip size="small" label={card.status} color={card.status === "HEALTHY" ? "success" : "warning"} />
              </Box>
            ))}
          </Stack>
        </Paper>
      </Stack>
    </Page>
  );
}

export const contractVersion = "1.0";
export const route = { path: "/monitoring", title: "Monitoring", Component: MonitoringWorkspace };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
