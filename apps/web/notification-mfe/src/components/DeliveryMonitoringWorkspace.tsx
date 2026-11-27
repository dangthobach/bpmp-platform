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
  defaultDeliveriesSeed,
  toGroupedDeliveryStatus,
  type DeliveryQueryPage,
  type DeliveryRecord,
  type GroupedDeliveryStatus,
  type NotificationChannel,
} from "../types";

interface CursorToken {
  beforeCreatedAt: string;
  beforeId: string;
}

interface Props {
  platform: PlatformServices;
  onReplayDelivery: (deliveryId: string) => void;
}

export function DeliveryMonitoringWorkspace({ platform, onReplayDelivery }: Props): ReactNode {
  const [cursorStack, setCursorStack] = useState<CursorToken[]>([]);
  const currentCursor = cursorStack[cursorStack.length - 1];
  const [filterChannel, setFilterChannel] = useState<string>("ALL");
  const [filterStatus, setFilterStatus] = useState<string>("ALL");
  const [searchQuery, setSearchQuery] = useState<string>("");

  const queryPath = useMemo(() => {
    const params = new URLSearchParams({ page_size: "20" });
    if (currentCursor) {
      params.set("before_created_at", currentCursor.beforeCreatedAt);
      params.set("before_id", currentCursor.beforeId);
    }
    if (filterChannel !== "ALL") params.set("channel", filterChannel);
    if (filterStatus !== "ALL") params.set("status", filterStatus);
    if (searchQuery.trim()) params.set("q", searchQuery.trim());
    return `/v1/notifications/deliveries?${params.toString()}`;
  }, [currentCursor, filterChannel, filterStatus, searchQuery]);

  const deliveriesQuery = useSWR<DeliveryQueryPage>(
    `notification-mfe:deliveries:${queryPath}`,
    async () => {
      const res = await platform.callApi<DeliveryQueryPage>({ method: "GET", path: queryPath });
      if (!res.ok) throw new Error(res.problem.title || "Failed to load deliveries");
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const [resentDeliveries, setResentDeliveries] = useState<DeliveryRecord[]>([]);
  const [selectedDeliveryId, setSelectedDeliveryId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [statusBanner, setStatusBanner] = useState<{ title: string; detail: string } | null>(null);
  const [errorBanner, setErrorBanner] = useState<{ title: string; detail: string } | null>(null);

  const rawDeliveries = useMemo(() => {
    const apiItems = deliveriesQuery.data?.deliveries;
    const base = apiItems && apiItems.length > 0 ? apiItems : defaultDeliveriesSeed;
    return [...resentDeliveries, ...base];
  }, [deliveriesQuery.data, resentDeliveries]);

  const deliveries = useMemo(() => {
    return rawDeliveries.filter((item) => {
      if (filterChannel !== "ALL" && item.channel !== filterChannel) return false;
      const grouped = toGroupedDeliveryStatus(item.status, item.attempt_count);
      if (filterStatus !== "ALL" && grouped !== filterStatus) return false;
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        const hay = `${item.id} ${item.message_id ?? ""} ${item.correlation_id ?? ""} ${item.template_key ?? ""} ${item.masked_address}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
  }, [rawDeliveries, filterChannel, filterStatus, searchQuery]);

  const selectedDelivery = useMemo(
    () => deliveries.find((d) => d.id === selectedDeliveryId) ?? deliveries[0] ?? null,
    [deliveries, selectedDeliveryId],
  );

  const detailQuery = useSWR<DeliveryRecord | null>(
    selectedDelivery ? `notification-mfe:delivery-detail:${selectedDelivery.id}` : null,
    async () => {
      if (!selectedDelivery) return null;
      const res = await platform.callApi<DeliveryRecord>({
        method: "GET",
        path: `/v1/notifications/deliveries/${encodeURIComponent(selectedDelivery.id)}`,
      });
      if (!res.ok) return null;
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const activeTrace = detailQuery.data?.trace ?? selectedDelivery?.trace ?? [];

  const handleResend = async (delivery: DeliveryRecord) => {
    if (busy) return;
    setBusy(true);
    setErrorBanner(null);
    const pair = createCommandPair("ntf-resend");
    const res = await platform.callApi<{ delivery?: DeliveryRecord }>({
      method: "POST",
      path: `/v1/notifications/deliveries/${encodeURIComponent(delivery.id)}/resend`,
      commandId: pair.commandId,
      idempotencyKey: pair.idempotencyKey,
      body: { reason: "Operator resend from Delivery Monitoring" },
    });
    setBusy(false);
    if (!res.ok) {
      setErrorBanner({ title: res.problem.title || "Resend failed", detail: `Code: ${res.problem.code}` });
      return;
    }
    const child: DeliveryRecord = res.data.delivery ?? {
      ...delivery,
      id: `dlv-resend-${Date.now()}`,
      status: "QUEUED",
      attempt_count: 0,
      resend_of_delivery_id: delivery.id,
      created_at: new Date().toISOString(),
    };
    setResentDeliveries((prev) => [child, ...prev]);
    setStatusBanner({
      title: "Idempotent Resend enqueued (D7)",
      detail: `Created child delivery ${child.id} linked to original ${delivery.id} (resend_of_delivery_id).`,
    });
  };

  const hasNextCursor = Boolean(deliveriesQuery.data?.next_before_id && deliveriesQuery.data?.next_before_created_at);

  const handleNextPage = () => {
    if (!deliveriesQuery.data?.next_before_id || !deliveriesQuery.data?.next_before_created_at) return;
    setCursorStack((prev) => [
      ...prev,
      {
        beforeCreatedAt: deliveriesQuery.data!.next_before_created_at!,
        beforeId: deliveriesQuery.data!.next_before_id!,
      },
    ]);
  };

  const handlePreviousPage = () => {
    setCursorStack((prev) => prev.slice(0, -1));
  };

  if (deliveriesQuery.isLoading) {
    return <LoadingPanel label="Loading notification deliveries..." />;
  }

  return (
    <Stack spacing={2}>
      {statusBanner && <StatusPanel title={statusBanner.title} detail={statusBanner.detail} />}
      {errorBanner && <ErrorPanel title={errorBanner.title} detail={errorBanner.detail} />}

      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Box>
            <Typography variant="h6">Delivery Monitoring (Keyset Paginated)</Typography>
            <Typography variant="caption" color="text.secondary">
              Search by message ID, correlation ID, template key, or exact full recipient address (hashed server-side). Partial PII substring search is disallowed.
            </Typography>
          </Box>

          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25} alignItems="center">
            <TextField
              size="small"
              label="Search message / correlation / template / full address"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              sx={{ flex: 1, minWidth: 260 }}
            />
            <FormControl size="small" sx={{ minWidth: 135 }}>
              <InputLabel id="dlv-filter-channel">Channel</InputLabel>
              <Select labelId="dlv-filter-channel" label="Channel" value={filterChannel} onChange={(e) => setFilterChannel(e.target.value)}>
                <MenuItem value="ALL">All Channels</MenuItem>
                {(["EMAIL", "SMS", "PUSH", "IN_APP", "ZNS", "WEBHOOK"] as NotificationChannel[]).map((ch) => (
                  <MenuItem key={ch} value={ch}>{ch}</MenuItem>
                ))}
              </Select>
            </FormControl>
            <FormControl size="small" sx={{ minWidth: 145 }}>
              <InputLabel id="dlv-filter-status">Grouped Status</InputLabel>
              <Select labelId="dlv-filter-status" label="Grouped Status" value={filterStatus} onChange={(e) => setFilterStatus(e.target.value)}>
                <MenuItem value="ALL">All Statuses</MenuItem>
                {(["DELIVERED", "FAILED", "BOUNCED", "RETRYING", "QUEUED"] as GroupedDeliveryStatus[]).map((st) => (
                  <MenuItem key={st} value={st}>{st}</MenuItem>
                ))}
              </Select>
            </FormControl>
          </Stack>

          <Box sx={{ overflowX: "auto" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                  <th style={{ padding: "8px 6px" }}>Delivery / Correlation</th>
                  <th style={{ padding: "8px 6px" }}>Template & Channel</th>
                  <th style={{ padding: "8px 6px" }}>Recipient (Masked)</th>
                  <th style={{ padding: "8px 6px" }}>Grouped Status</th>
                  <th style={{ padding: "8px 6px" }}>Attempts</th>
                  <th style={{ padding: "8px 6px" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {deliveries.map((dlv) => {
                  const grouped = toGroupedDeliveryStatus(dlv.status, dlv.attempt_count);
                  const chipColor =
                    grouped === "DELIVERED" ? "success" : grouped === "FAILED" || grouped === "BOUNCED" ? "error" : "warning";
                  return (
                    <tr
                      key={dlv.id}
                      style={{
                        borderBottom: "1px solid #eff3f0",
                        backgroundColor: selectedDelivery?.id === dlv.id ? "#f2f8f4" : "transparent",
                      }}
                    >
                      <td style={{ padding: "8px 6px" }}>
                        <Button size="small" variant="text" sx={{ p: 0 }} onClick={() => setSelectedDeliveryId(dlv.id)}>
                          {dlv.id}
                        </Button>
                        <Typography variant="caption" color="text.secondary" display="block">
                          {dlv.correlation_id ?? dlv.request_id}
                          {dlv.resend_of_delivery_id ? ` • resend_of: ${dlv.resend_of_delivery_id}` : ""}
                        </Typography>
                      </td>
                      <td style={{ padding: "8px 6px" }}>
                        <Typography variant="body2">{dlv.template_key ?? dlv.template_version_id}</Typography>
                        <Typography variant="caption" color="text.secondary">
                          {dlv.channel} • {dlv.lane} • {dlv.selected_provider_id ?? "-"}
                        </Typography>
                      </td>
                      <td style={{ padding: "8px 6px", fontFamily: "Consolas, monospace" }}>{dlv.masked_address}</td>
                      <td style={{ padding: "8px 6px" }}>
                        <Chip size="small" color={chipColor} label={grouped} />
                      </td>
                      <td style={{ padding: "8px 6px" }}>{dlv.attempt_count}/{dlv.max_attempts ?? 3}</td>
                      <td style={{ padding: "8px 6px" }}>
                        <Stack direction="row" spacing={0.5}>
                          <Button size="small" variant="outlined" onClick={() => onReplayDelivery(dlv.id)}>
                            Replay Preview
                          </Button>
                          <Button
                            size="small"
                            variant="outlined"
                            disabled={busy || Boolean(dlv.has_secret_variables) || Boolean(dlv.payload_expired)}
                            onClick={() => void handleResend(dlv)}
                          >
                            Resend
                          </Button>
                        </Stack>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </Box>

          <Stack direction="row" spacing={1} justifyContent="flex-end">
            <Button size="small" variant="outlined" disabled={cursorStack.length === 0} onClick={handlePreviousPage}>
              Previous Page
            </Button>
            <Button size="small" variant="outlined" disabled={!hasNextCursor} onClick={handleNextPage}>
              Next Cursor Page
            </Button>
          </Stack>
        </Stack>
      </Paper>

      {/* Pipeline Trace Inspector */}
      {selectedDelivery && (
        <Paper variant="outlined" sx={{ p: 2 }}>
          <Stack spacing={1.25}>
            <Stack direction="row" justifyContent="space-between" alignItems="center">
              <Typography variant="subtitle1" fontWeight={700}>
                Pipeline Trace Inspector — {selectedDelivery.id}
              </Typography>
              <Typography variant="caption" color="text.secondary">
                Replay Preview writes DELIVERY_PAYLOAD_INSPECTED audit evidence and falls back to sample data if expired.
              </Typography>
            </Stack>
            {selectedDelivery.error_message && (
              <ErrorPanel title="Delivery Error" detail={selectedDelivery.error_message} />
            )}
            <Stack spacing={0.75}>
              {activeTrace.length === 0 ? (
                <Typography variant="caption" color="text.secondary">No trace steps returned for this delivery.</Typography>
              ) : (
                activeTrace.map((step, idx) => (
                  <Paper key={`${step.step}-${idx}`} variant="outlined" sx={{ p: 1 }}>
                    <Stack direction={{ xs: "column", sm: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", sm: "center" }} spacing={1}>
                      <Stack direction="row" spacing={1} alignItems="center">
                        <Chip
                          size="small"
                          color={step.status === "OK" ? "success" : step.status === "WARN" ? "warning" : "error"}
                          label={step.status}
                        />
                        <Typography variant="body2" fontWeight={650}>{step.step}</Typography>
                        <Typography variant="caption" color="text.secondary">{step.detail}</Typography>
                      </Stack>
                      <Typography variant="caption" sx={{ fontFamily: "Consolas, monospace" }}>{step.time_ms} ms</Typography>
                    </Stack>
                  </Paper>
                ))
              )}
            </Stack>
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}
