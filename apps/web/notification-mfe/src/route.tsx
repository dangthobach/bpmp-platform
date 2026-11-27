import { useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Box, Button, Chip, Divider, Page, Paper, Stack, Typography } from "@ods/shared-ui";
import {
  defaultProvidersSeed,
  defaultVariableMappingSeed,
  type NotificationChannel,
  type NotificationSection,
} from "./types";
import { TemplatesWorkspace } from "./components/TemplatesWorkspace";
import { VariableMappingWorkspace } from "./components/VariableMappingWorkspace";
import { ApplicationsWorkspace } from "./components/ApplicationsWorkspace";
import { ServiceProvidersWorkspace } from "./components/ServiceProvidersWorkspace";
import { DeliveryMonitoringWorkspace } from "./components/DeliveryMonitoringWorkspace";
import { MetricsDashboardWorkspace } from "./components/MetricsDashboardWorkspace";
import { AuditLogsWorkspace } from "./components/AuditLogsWorkspace";
import { CircuitBreakerWorkspace } from "./components/CircuitBreakerWorkspace";
import { SendNotificationWorkspace } from "./components/SendNotificationWorkspace";

export type { NotificationSection };

interface NavItemDef {
  id: NotificationSection;
  label: string;
}

interface NavGroupDef {
  group: "Management" | "Monitoring" | "Operations";
  items: NavItemDef[];
}

const NAV_GROUPS: NavGroupDef[] = [
  {
    group: "Management",
    items: [
      { id: "templates", label: "Templates" },
      { id: "variable-mapping", label: "Variable Mapping" },
      { id: "applications", label: "Applications" },
      { id: "providers", label: "Service Providers" },
    ],
  },
  {
    group: "Monitoring",
    items: [
      { id: "delivery-monitoring", label: "Delivery Monitoring" },
      { id: "metrics", label: "Metrics Dashboard" },
      { id: "audit-logs", label: "Audit Logs" },
      { id: "circuit-breaker", label: "Circuit Breaker" },
    ],
  },
  {
    group: "Operations",
    items: [{ id: "send-notification", label: "Send Notification" }],
  },
];

export function NotificationWorkspace({ platform }: RemoteRouteProps): ReactNode {
  const [activeSection, setActiveSection] = useState<NotificationSection>("templates");
  const [replayDeliveryId, setReplayDeliveryId] = useState<string | undefined>(undefined);
  const [sendPrefill, setSendPrefill] = useState<{
    templateKey?: string;
    schemaMajor?: number;
    channel?: NotificationChannel;
  }>({});

  const [variableRiskCount, setVariableRiskCount] = useState<number>(
    () => defaultVariableMappingSeed.links.filter((l) => l.severity === "WARN" || l.severity === "CRITICAL").length,
  );
  const [nonClosedBreakerCount, setNonClosedBreakerCount] = useState<number>(
    () => defaultProvidersSeed.filter((p) => p.breaker_state !== "CLOSED").length,
  );

  const handleReplayFromMonitoring = (deliveryId: string) => {
    setReplayDeliveryId(deliveryId);
    setActiveSection("templates");
  };

  const handleNavigateToSend = (templateKey: string, schemaMajor: number, channel: NotificationChannel) => {
    setSendPrefill({ templateKey, schemaMajor, channel });
    setActiveSection("send-notification");
  };

  return (
    <Page title="Notification Service">
      <Stack direction={{ xs: "column", md: "row" }} spacing={2.5} alignItems="flex-start">
        {/* Left Navigation Rail (3 groups, 9 sections) */}
        <Paper
          component="nav"
          aria-label="Notification navigation"
          variant="outlined"
          sx={{
            width: { xs: "100%", md: 248 },
            flexShrink: 0,
            p: 1.5,
            borderColor: "#e2e7e3",
            borderRadius: 2,
          }}
        >
          <Stack spacing={1.5}>
            {NAV_GROUPS.map((groupDef, groupIndex) => (
              <Box key={groupDef.group}>
                {groupIndex > 0 && <Divider sx={{ mb: 1.25 }} />}
                <Typography
                  variant="caption"
                  color="text.secondary"
                  sx={{ fontWeight: 750, textTransform: "uppercase", letterSpacing: "0.06em", px: 1, display: "block", mb: 0.75 }}
                >
                  {groupDef.group}
                </Typography>
                <Stack spacing={0.5}>
                  {groupDef.items.map((item) => {
                    const isActive = activeSection === item.id;
                    const badgeCount =
                      item.id === "variable-mapping"
                        ? variableRiskCount
                        : item.id === "circuit-breaker"
                          ? nonClosedBreakerCount
                          : 0;
                    const badgeColor = item.id === "circuit-breaker" ? "error" : "warning";
                    return (
                      <Button
                        key={item.id}
                        type="button"
                        fullWidth
                        variant={isActive ? "contained" : "text"}
                        aria-current={isActive ? "page" : undefined}
                        onClick={() => setActiveSection(item.id)}
                        sx={{
                          justifyContent: "space-between",
                          px: 1.25,
                          py: 0.85,
                          textAlign: "left",
                        }}
                      >
                        <span>{item.label}</span>
                        {badgeCount > 0 && (
                          <Chip
                            size="small"
                            color={badgeColor}
                            label={String(badgeCount)}
                            aria-label={`${item.label} alerts: ${badgeCount}`}
                            sx={{ height: 20, fontSize: 11 }}
                          />
                        )}
                      </Button>
                    );
                  })}
                </Stack>
              </Box>
            ))}
          </Stack>
        </Paper>

        {/* Active Section Workspace */}
        <Box sx={{ flex: 1, minWidth: 0, width: "100%" }}>
          {activeSection === "templates" && (
            <TemplatesWorkspace
              platform={platform}
              replayDeliveryId={replayDeliveryId}
              onClearReplay={() => setReplayDeliveryId(undefined)}
              onNavigateToSend={handleNavigateToSend}
            />
          )}
          {activeSection === "variable-mapping" && (
            <VariableMappingWorkspace platform={platform} onRiskCountChange={setVariableRiskCount} />
          )}
          {activeSection === "applications" && <ApplicationsWorkspace platform={platform} />}
          {activeSection === "providers" && <ServiceProvidersWorkspace platform={platform} />}
          {activeSection === "delivery-monitoring" && (
            <DeliveryMonitoringWorkspace platform={platform} onReplayDelivery={handleReplayFromMonitoring} />
          )}
          {activeSection === "metrics" && <MetricsDashboardWorkspace platform={platform} />}
          {activeSection === "audit-logs" && <AuditLogsWorkspace platform={platform} />}
          {activeSection === "circuit-breaker" && (
            <CircuitBreakerWorkspace platform={platform} onNonClosedCountChange={setNonClosedBreakerCount} />
          )}
          {activeSection === "send-notification" && (
            <SendNotificationWorkspace
              platform={platform}
              initialTemplateKey={sendPrefill.templateKey}
              initialSchemaMajor={sendPrefill.schemaMajor}
              initialChannel={sendPrefill.channel}
            />
          )}
        </Box>
      </Stack>
    </Page>
  );
}

export const contractVersion = "1.0";
export const route = {
  path: "/notifications",
  title: "Notifications",
  Component: NotificationWorkspace,
};
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
