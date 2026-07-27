import { createContext, useContext, type PropsWithChildren } from "react";
import type { RuntimeConfig } from "./runtime";

const ConfigContext = createContext<RuntimeConfig | null>(null);

export function ConfigProvider({
  config,
  children,
}: PropsWithChildren<{ config: RuntimeConfig }>) {
  return <ConfigContext.Provider value={config}>{children}</ConfigContext.Provider>;
}

export function useRuntimeConfig(): RuntimeConfig {
  const value = useContext(ConfigContext);
  if (!value) throw new Error("ConfigProvider is missing");
  return value;
}
