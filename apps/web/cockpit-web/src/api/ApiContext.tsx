import { createContext, useContext, useMemo, type PropsWithChildren } from "react";
import { BpmpApiClient } from "./client";
import { useRuntimeConfig } from "../config/ConfigContext";
import { useAuth } from "../auth/AuthContext";

const ApiContext = createContext<BpmpApiClient | null>(null);

export function ApiProvider({ children }: PropsWithChildren) {
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const client = useMemo(
    () =>
      new BpmpApiClient(config, () => {
        if (!identity) throw new Error("API identity is unavailable");
        return identity;
      }),
    [config, identity],
  );
  return <ApiContext.Provider value={client}>{children}</ApiContext.Provider>;
}

export function useApi(): BpmpApiClient {
  const value = useContext(ApiContext);
  if (!value) throw new Error("ApiProvider is missing");
  return value;
}
