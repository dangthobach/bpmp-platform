import { useCallback, useEffect, useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Box, Button, Chip, ErrorPanel, Page, Paper, Stack, StatusPanel, Typography } from "@ods/shared-ui";

interface EndpointRecord {
  id: string;
  name: string;
  system_type: string;
  base_url: string;
  secret_ref: string;
  circuit_state: string;
  status: string;
}

interface DeliveryRecord {
  id: string;
  endpoint_id: string;
  downstream_idempotency_key: string;
  redacted_summary: string;
  status: string;
  attempt_count: number;
  max_attempts: number;
  last_response_class?: string;
  version: number;
}

function IntegrationOpsWorkspace({ platform }: RemoteRouteProps): ReactNode {
  const [endpoints, setEndpoints] = useState<EndpointRecord[]>([]);
  const [deliveries, setDeliveries] = useState<DeliveryRecord[]>([]);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    const [epRes, delRes] = await Promise.all([
      platform.callApi<{ endpoints: EndpointRecord[] }>({ method: "GET", path: "/v1/admin/endpoints" }),
      platform.callApi<{ deliveries: DeliveryRecord[] }>({ method: "GET", path: "/v1/admin/integration/deliveries" }),
    ]);
    if (!epRes.ok) {
      setError(epRes.problem.detail || epRes.problem.title);
      return;
    }
    if (!delRes.ok) {
      setError(delRes.problem.detail || delRes.problem.title);
      return;
    }
    setEndpoints(epRes.data.endpoints ?? []);
    setDeliveries(delRes.data.deliveries ?? []);
    setError("");
  }, [platform]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const handleRetry = async (del: DeliveryRecord) => {
    const res = await platform.callApi<{ delivery: DeliveryRecord }>({
      method: "POST",
      path: `/v1/admin/integration/deliveries/${encodeURIComponent(del.id)}/retry`,
      idempotencyKey: `retry-${del.id}-v${del.version}`,
      body: { expected_version: del.version, reason: "Operator manual retry after downstream recovery" },
    });
    if (!res.ok) {
      setError(res.problem.detail || res.problem.title);
      return;
    }
    setNotice(`Delivery ${del.id} retried with preserved downstream key ${del.downstream_idempotency_key}.`);
    await refresh();
  };

  return (
    <Page title="Integration Hub & Operations">
      <Stack spacing={2.5}>
        {error ? <ErrorPanel title="Integration error" detail={error} onRetry={() => void refresh()} /> : null}
        {notice ? <StatusPanel title="Integration status" detail={notice} /> : null}

        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Typography variant="h6" sx={{ mb: 1.5 }}>Registered External Endpoints ({endpoints.length})</Typography>
          <Stack spacing={1}>
            {endpoints.length === 0 ? (
              <Typography color="text.secondary">No external endpoints configured.</Typography>
            ) : endpoints.map((ep) => (
              <Box key={ep.id} sx={{ p: 1.5, border: "1px solid #e2e7e3", borderRadius: 1.5, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <Box>
                  <Typography fontWeight={700}>{ep.name} ({ep.system_type})</Typography>
                  <Typography variant="caption" color="text.secondary">{ep.base_url} · Secret ref: {ep.secret_ref}</Typography>
                </Box>
                <Chip size="small" label={`Circuit: ${ep.circuit_state}`} color={ep.circuit_state === "CLOSED" ? "success" : "error"} />
              </Box>
            ))}
          </Stack>
        </Paper>

        <Paper variant="outlined" sx={{ p: 2.5 }}>
          <Typography variant="h6" sx={{ mb: 1.5 }}>Delivery & Reconciliation Ledger ({deliveries.length})</Typography>
          <Stack spacing={1}>
            {deliveries.length === 0 ? (
              <Typography color="text.secondary">No external deliveries recorded.</Typography>
            ) : deliveries.map((del) => (
              <Box key={del.id} sx={{ p: 1.5, border: "1px solid #e2e7e3", borderRadius: 1.5, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <Box>
                  <Typography fontWeight={700}>{del.redacted_summary}</Typography>
                  <Typography variant="caption" color="text.secondary">
                    {del.id} · Attempts {del.attempt_count}/{del.max_attempts} · Key: {del.downstream_idempotency_key}
                  </Typography>
                </Box>
                <Stack direction="row" spacing={1} alignItems="center">
                  <Chip size="small" label={del.status} color={del.status === "SUCCEEDED" ? "success" : del.status === "RECONCILING" ? "warning" : "error"} />
                  <Button
                    size="small"
                    variant="outlined"
                    disabled={del.status === "SUCCEEDED" || del.status === "COMPENSATED"}
                    onClick={() => void handleRetry(del)}
                  >
                    Retry
                  </Button>
                </Stack>
              </Box>
            ))}
          </Stack>
        </Paper>
      </Stack>
    </Page>
  );
}

export const contractVersion = "1.0";
export const route = { path: "/integrations", title: "Integrations", Component: IntegrationOpsWorkspace };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
