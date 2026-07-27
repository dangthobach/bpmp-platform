import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { ApiProvider } from "./api/ApiContext";
import { AuthProvider, useAuth } from "./auth/AuthContext";
import { ConnectionDialog } from "./components/ConnectionDialog";
import { ConfigProvider } from "./config/ConfigContext";
import type { RuntimeConfig } from "./config/runtime";
import { AuditPage } from "./features/audit/AuditPage";
import { CasesPage } from "./features/cases/CasesPage";
import { StartWorkflowPage } from "./features/start/StartWorkflowPage";
import { WorkItemsPage } from "./features/work-items/WorkItemsPage";
import { AppShell } from "./layout/AppShell";
import { usePathname } from "./routing/router";

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
            <AuthenticatedApp />
          </ApiProvider>
        </QueryClientProvider>
      </AuthProvider>
    </ConfigProvider>
  );
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
