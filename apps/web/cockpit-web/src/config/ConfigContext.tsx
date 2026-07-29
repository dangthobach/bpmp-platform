import { createContext, useCallback, useContext, useMemo, useState, type PropsWithChildren } from "react";
import type { RuntimeConfig } from "./runtime";

type BrowserConfiguration = {
  batchChunkSize: number;
  batchConcurrency: number;
};

const ConfigContext = createContext<{
  config: RuntimeConfig;
  applyBrowserConfiguration: (configuration: BrowserConfiguration) => void;
} | null>(null);
export function ConfigProvider({
  config,
  children,
}: PropsWithChildren<{ config: RuntimeConfig }>) {
  const [resolved, setResolved] = useState(config);
  const applyBrowserConfiguration = useCallback((configuration: BrowserConfiguration) => {
    setResolved((current) => {
      if (
        current.batchChunkSize === configuration.batchChunkSize &&
        current.batchConcurrency === configuration.batchConcurrency
      ) return current;
      return {
        ...current,
        batchChunkSize: configuration.batchChunkSize,
        batchConcurrency: configuration.batchConcurrency,
      };
    });
  }, []);
  const value = useMemo(
    () => ({ config: resolved, applyBrowserConfiguration }),
    [resolved, applyBrowserConfiguration],
  );
  return <ConfigContext.Provider value={value}>{children}</ConfigContext.Provider>;
}

export function useRuntimeConfig(): RuntimeConfig {
  const value = useContext(ConfigContext);
  if (!value) throw new Error("ConfigProvider is missing");
  return value.config;
}

export function useBrowserConfigurationUpdater() {
  const value = useContext(ConfigContext);
  if (!value) throw new Error("ConfigProvider is missing");
  return value.applyBrowserConfiguration;
}
