import {
  createContext,
  useContext,
  useMemo,
  useState,
  type PropsWithChildren,
} from "react";

export interface SessionIdentity {
  tenantId: string;
  accessToken: string;
}

interface AuthContextValue {
  identity: SessionIdentity | null;
  connect: (identity: SessionIdentity) => void;
  disconnect: () => void;
}

const storageKey = "bpmp.cockpit.session.v1";
const AuthContext = createContext<AuthContextValue | null>(null);

function readSession(): SessionIdentity | null {
  try {
    const raw = sessionStorage.getItem(storageKey);
    if (!raw) return null;
    const value = JSON.parse(raw) as Partial<SessionIdentity>;
    return value.tenantId && value.accessToken
      ? { tenantId: value.tenantId, accessToken: value.accessToken }
      : null;
  } catch {
    return null;
  }
}

export function AuthProvider({ children }: PropsWithChildren) {
  const [identity, setIdentity] = useState<SessionIdentity | null>(readSession);
  const value = useMemo<AuthContextValue>(
    () => ({
      identity,
      connect(next) {
        sessionStorage.setItem(storageKey, JSON.stringify(next));
        setIdentity(next);
      },
      disconnect() {
        sessionStorage.removeItem(storageKey);
        setIdentity(null);
      },
    }),
    [identity],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error("AuthProvider is missing");
  return value;
}
