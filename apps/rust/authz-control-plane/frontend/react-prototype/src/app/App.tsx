// Root composition: router + Radix theme + auth + query client + api context.
// All cross-cutting providers wired here so feature modules stay thin.
import { useMemo } from "react";
import { Theme } from "@radix-ui/themes";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { BrowserRouter, Route, Routes, Navigate } from "react-router-dom";
import "@radix-ui/themes/styles.css";

import { AuthProvider, useAuth } from "@shared/auth/AuthContext";
import { HttpClient } from "@shared/api/http";
import { OrgApiContext } from "@features/organizations/api/client";
import { OrganizationsApi } from "@features/organizations/api/organizationsApi";
import { OrganizationsPage } from "@features/organizations/OrganizationsPage";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
});

function ApiProvider({ children }: { children: React.ReactNode }) {
  const { token } = useAuth();
  const api = useMemo(() => {
    const http = new HttpClient({
      baseUrl: import.meta.env.VITE_AUTHZ_APP_URL ?? "",
      getToken: () => token,
    });
    return new OrganizationsApi(http);
  }, [token]);
  return (
    <OrgApiContext.Provider value={api}>{children}</OrgApiContext.Provider>
  );
}

export function App() {
  return (
    <Theme accentColor="indigo" radius="medium" appearance="inherit">
      <BrowserRouter>
        <AuthProvider>
          <QueryClientProvider client={queryClient}>
            <ApiProvider>
              <Routes>
                <Route path="/organizations" element={<OrganizationsPage />} />
                <Route
                  path="*"
                  element={<Navigate to="/organizations" replace />}
                />
              </Routes>
              <ReactQueryDevtools initialIsOpen={false} />
            </ApiProvider>
          </QueryClientProvider>
        </AuthProvider>
      </BrowserRouter>
    </Theme>
  );
}
