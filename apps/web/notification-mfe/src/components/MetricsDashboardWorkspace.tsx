import { useMemo, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { PlatformServices } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
  LoadingPanel,
  Paper,
  Stack,
  Typography,
} from "@ods/shared-ui";
import { defaultMetricsSeed, type MetricSlice } from "../types";

interface MetricsResponse {
  slices?: MetricSlice[];
}

interface Props {
  platform: PlatformServices;
}

type TimeWindow = "1h" | "24h" | "7d";
type GroupBy = "channel" | "lane" | "provider";

const WINDOW_HOURS: Record<TimeWindow, number> = {
  "1h": 1,
  "24h": 24,
  "7d": 168,
};

export function MetricsDashboardWorkspace({ platform }: Props): ReactNode {
  const [windowKey, setWindowKey] = useState<TimeWindow>("24h");
  const [groupBy, setGroupBy] = useState<GroupBy>("channel");

  const metricsQuery = useSWR<MetricsResponse>(
    `notification-mfe:metrics:${windowKey}:${groupBy}`,
    async () => {
      const res = await platform.callApi<MetricsResponse>({
        method: "POST",
        path: "/v1/notifications/metrics/query",
        body: {
          window_hours: WINDOW_HOURS[windowKey],
          group_by: groupBy,
          filter: {},
        },
      });
      if (!res.ok) return {};
      return res.data;
    },
    { shouldRetryOnError: false },
  );

  const slices = useMemo(() => {
    const base =
      metricsQuery.data?.slices && metricsQuery.data.slices.length > 0
        ? metricsQuery.data.slices
        : defaultMetricsSeed;
    const expectedDimension = groupBy.toUpperCase();
    return base.filter((s) => s.dimension === expectedDimension);
  }, [metricsQuery.data, groupBy]);

  const totals = useMemo(() => {
    return slices.reduce(
      (acc, s) => ({
        accepted: acc.accepted + s.accepted,
        sent: acc.sent + s.sent,
        delivered: acc.delivered + s.delivered,
        failed: acc.failed + s.failed,
      }),
      { accepted: 0, sent: 0, delivered: 0, failed: 0 },
    );
  }, [slices]);

  if (metricsQuery.isLoading) {
    return <LoadingPanel label="Querying histogram-backed notification metrics..." />;
  }

  return (
    <Stack spacing={2}>
      <Paper variant="outlined" sx={{ p: 2 }}>
        <Stack spacing={1.5}>
          <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", md: "center" }} spacing={1}>
            <Box>
              <Typography variant="h6">Metrics Dashboard (Zero PII)</Typography>
              <Typography variant="caption" color="text.secondary">
                Aggregated from hourly latency histograms (`latency_histogram`) for accurate p50 / p95 / p99 percentiles across buckets.
              </Typography>
            </Box>
            <Stack direction="row" spacing={1} flexWrap="wrap">
              {(["1h", "24h", "7d"] as TimeWindow[]).map((w) => (
                <Button key={w} size="small" variant={windowKey === w ? "contained" : "outlined"} onClick={() => setWindowKey(w)}>
                  {w}
                </Button>
              ))}
              {(["channel", "lane", "provider"] as GroupBy[]).map((g) => (
                <Button key={g} size="small" variant={groupBy === g ? "contained" : "outlined"} onClick={() => setGroupBy(g)}>
                  By {g}
                </Button>
              ))}
            </Stack>
          </Stack>

          {/* Summary KPI strip */}
          <Stack direction={{ xs: "column", sm: "row" }} spacing={1.5}>
            <Paper variant="outlined" sx={{ p: 1.5, flex: 1 }}>
              <Typography variant="caption" color="text.secondary">Accepted ({windowKey})</Typography>
              <Typography variant="h5" fontWeight={750}>{totals.accepted.toLocaleString()}</Typography>
            </Paper>
            <Paper variant="outlined" sx={{ p: 1.5, flex: 1 }}>
              <Typography variant="caption" color="text.secondary">Sent</Typography>
              <Typography variant="h5" fontWeight={750}>{totals.sent.toLocaleString()}</Typography>
            </Paper>
            <Paper variant="outlined" sx={{ p: 1.5, flex: 1 }}>
              <Typography variant="caption" color="text.secondary">Delivered</Typography>
              <Typography variant="h5" fontWeight={750} color="primary.main">{totals.delivered.toLocaleString()}</Typography>
            </Paper>
            <Paper variant="outlined" sx={{ p: 1.5, flex: 1 }}>
              <Typography variant="caption" color="text.secondary">Failed</Typography>
              <Typography variant="h5" fontWeight={750} color="error.main">{totals.failed.toLocaleString()}</Typography>
            </Paper>
          </Stack>

          <Box sx={{ overflowX: "auto" }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ borderBottom: "1px solid #e2e7e3", textAlign: "left" }}>
                  <th style={{ padding: "8px 6px" }}>{groupBy.toUpperCase()}</th>
                  <th style={{ padding: "8px 6px" }}>Accepted</th>
                  <th style={{ padding: "8px 6px" }}>Sent</th>
                  <th style={{ padding: "8px 6px" }}>Delivered</th>
                  <th style={{ padding: "8px 6px" }}>Failed</th>
                  <th style={{ padding: "8px 6px" }}>p50 Latency</th>
                  <th style={{ padding: "8px 6px" }}>p95 Latency</th>
                  <th style={{ padding: "8px 6px" }}>p99 Latency</th>
                </tr>
              </thead>
              <tbody>
                {slices.map((slice) => (
                  <tr key={`${slice.dimension}-${slice.key}`} style={{ borderBottom: "1px solid #eff3f0" }}>
                    <td style={{ padding: "8px 6px" }}>
                      <Chip size="small" variant="outlined" label={slice.key} />
                    </td>
                    <td style={{ padding: "8px 6px" }}>{slice.accepted.toLocaleString()}</td>
                    <td style={{ padding: "8px 6px" }}>{slice.sent.toLocaleString()}</td>
                    <td style={{ padding: "8px 6px" }}>{slice.delivered.toLocaleString()}</td>
                    <td style={{ padding: "8px 6px" }}>{slice.failed.toLocaleString()}</td>
                    <td style={{ padding: "8px 6px" }}>{slice.p50_latency_ms} ms</td>
                    <td style={{ padding: "8px 6px" }}>{slice.p95_latency_ms} ms</td>
                    <td style={{ padding: "8px 6px" }}>{slice.p99_latency_ms} ms</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Box>
        </Stack>
      </Paper>
    </Stack>
  );
}
