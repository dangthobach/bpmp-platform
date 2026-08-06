import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { LogOut, RefreshCw } from "lucide-react";
import { useEffect, useState, type PropsWithChildren } from "react";
import { ApiProvider, useApi } from "./api/ApiContext";
import { ApiError } from "./api/client";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { Button } from "./components/Button";
import { ConnectionDialog } from "./components/ConnectionDialog";
import { ConfigProvider, useBrowserConfigurationUpdater } from "./config/ConfigContext";
import type { RuntimeConfig } from "./config/runtime";
import { AuditPage } from "./features/audit/AuditPage";
import { CasesPage } from "./features/cases/CasesPage";
import { StartWorkflowPage } from "./features/start/StartWorkflowPage";
import { WorkItemsPage } from "./features/work-items/WorkItemsPage";
import { OrganizationsPage } from "./features/organizations/OrganizationsPage";
import { ConfigurationPage } from "./features/configuration/ConfigurationPage";
import { AppShell } from "./layout/AppShell";
import { usePathname } from "./routing/router";
import { RealtimeProvider } from "./realtime/RealtimeContext";

export function App({ config }: { config: RuntimeConfig }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: 1, refetchOnWindowFocus: false },
          mutations: { retry: false },
        },
      }),
  );
  return (
    <ConfigProvider config={config}>
      <AuthProvider>
        <QueryClientProvider client={queryClient}>
          <ApiProvider>
            <BrowserConfigurationBoundary>
              <RealtimeProvider>
                <AuthenticatedApp />
              </RealtimeProvider>
            </BrowserConfigurationBoundary>
          </ApiProvider>
        </QueryClientProvider>
      </AuthProvider>
    </ConfigProvider>
  );
}

function BrowserConfigurationBoundary({ children }: PropsWithChildren) {
  const api = useApi();
  const { identity, disconnect } = useAuth();
  const apply = useBrowserConfigurationUpdater();
  const configuration = useQuery({
    queryKey: ["browser-configuration", identity?.tenantId],
    queryFn: () => api.getBrowserConfiguration(),
    enabled: Boolean(identity),
    staleTime: 0,
  });
  useEffect(() => {
    if (!configuration.data) return;
    apply({
      batchChunkSize: configuration.data.batch_chunk_size,
      batchConcurrency: configuration.data.batch_concurrency,
    });
  }, [apply, configuration.data]);
  if (identity && configuration.isPending) {
    return (
      <main className="connection-screen" aria-busy="true">
        <section className="connection-panel" role="status">
          <h1>Loading runtime configuration</h1>
        </section>
      </main>
    );
  }
  if (identity && configuration.isError) {
    const error = configuration.error;
    const correlation = error instanceof ApiError && error.correlationId
      ? ` Correlation ID: ${error.correlationId}.`
      : "";
    return (
      <main className="connection-screen">
        <section className="connection-panel" role="alert">
          <h1>Runtime configuration unavailable</h1>
          <p className="form-error">
            {error instanceof Error ? error.message : "Request failed"}.{correlation}
          </p>
          <div className="connection-panel__actions">
            <Button
              icon={RefreshCw}
              variant="primary"
              onClick={() => void configuration.refetch()}
              disabled={configuration.isFetching}
            >
              Retry
            </Button>
            <Button icon={LogOut} onClick={disconnect}>Disconnect</Button>
          </div>
        </section>
      </main>
    );
  }
  return children;
}

function AuthenticatedApp() {
  const { identity } = useAuth();
  const pathname = usePathname();
  if (!identity) return <ConnectionDialog />;
  const page = (() => {
    switch (pathname) {
      case "/start":
        return <StartWorkflowPage />;
      case "/cases":
        return <CasesPage />;
      case "/audit":
        return <AuditPage />;
      case "/organizations":
        return <OrganizationsPage />;
      case "/configuration":
        return <ConfigurationPage />;
      default:
        return <WorkItemsPage />;
    }
  })();
  return (
    <AppShell>
      {page}
    </AppShell>
  );
}
