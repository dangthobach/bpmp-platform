// Minimal auth provider for the prototype: token stored in memory, refreshed
// out-of-band. Production version delegates to Keycloak / OIDC adapter.
import { createContext, useContext, useMemo, useState, type ReactNode } from "react";

interface AuthState {
  token: string | null;
  setToken: (t: string | null) => void;
  tenantId: string | null;
}

const Ctx = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(
    () => localStorage.getItem("authz.token"),
  );
  const tenantId = useMemo(() => decodeTenantId(token), [token]);
  const value: AuthState = {
    token,
    tenantId,
    setToken: (t) => {
      if (t) localStorage.setItem("authz.token", t);
      else localStorage.removeItem("authz.token");
      setToken(t);
    },
  };
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAuth must be used within AuthProvider");
  return v;
}

function decodeTenantId(token: string | null): string | null {
  if (!token) return null;
  try {
    const [, payload] = token.split(".");
    const json = JSON.parse(atob(payload.replace(/-/g, "+").replace(/_/g, "/")));
    return json.tenant_id ?? null;
  } catch {
    return null;
  }
}
