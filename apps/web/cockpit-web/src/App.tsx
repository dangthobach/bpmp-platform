import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { useEffect, useState, type PropsWithChildren } from "react";
import { ApiProvider, useApi } from "./api/ApiContext";
import { AuthProvider, useAuth } from "./auth/AuthContext";
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
  const { identity } = useAuth();
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
  if (identity && !configuration.isSuccess) return null;
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
