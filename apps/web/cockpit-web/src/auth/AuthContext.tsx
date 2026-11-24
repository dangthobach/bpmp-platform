import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type PropsWithChildren,
} from "react";

export interface SessionIdentity {
  tenantId: string;
  actorId: string;
}

interface AuthContextValue {
  identity: SessionIdentity | null;
  loading: boolean;
  connect: () => void;
  disconnect: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function csrfToken(): string {
  const prefix = "__Host-bpmp_csrf=";
  const value = document.cookie.split(";").map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  return value ? decodeURIComponent(value.slice(prefix.length)) : "";
}

export function AuthProvider({ children }: PropsWithChildren) {
  const [identity, setIdentity] = useState<SessionIdentity | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    // Remove credentials persisted by the previous manual-token login flow.
    sessionStorage.removeItem("bpmp.cockpit.session.v1");
    void fetch("/auth/session", { credentials: "include", cache: "no-store" })
      .then(async (response) => {
        if (response.status === 401) return null;
        if (!response.ok) throw new Error(`Session lookup failed (${response.status})`);
        const value = await response.json() as { tenant_id?: unknown; actor_id?: unknown };
        if (typeof value.tenant_id !== "string" || typeof value.actor_id !== "string") {
          throw new Error("Gateway returned an invalid browser session");
        }
        return { tenantId: value.tenant_id, actorId: value.actor_id };
      })
      .then((session) => { if (active) setIdentity(session); })
      .catch((error: unknown) => {
        if (active) console.error("Could not restore Cockpit session", error);
      })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  const value = useMemo<AuthContextValue>(() => ({
    identity,
    loading,
    connect() {
      window.location.assign("/auth/oidc/login");
    },
    async disconnect() {
      const response = await fetch("/auth/session/logout", {
        method: "POST",
        credentials: "include",
        headers: { "X-CSRF-Token": csrfToken() },
      });
      if (!response.ok) throw new Error(`Logout failed (${response.status})`);
      setIdentity(null);
    },
  }), [identity, loading]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error("AuthProvider is missing");
  return value;
}
