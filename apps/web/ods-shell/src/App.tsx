import { useEffect, useMemo, useState, type ReactNode } from "react";
import { createCallApi } from "@ods/call-api";
import type { PlatformServices, RemoteCatalog } from "@ods/mfe-contract";
import { ErrorPanel, LoadingPanel, OdsThemeProvider, Page, ShellHeader } from "@ods/shared-ui";
import { initializeBrowserTelemetry, recordComponentError, startApiTrace } from "@ods/telemetry";
import { loadShellConfig, type ShellConfig } from "./config";
import { loadCatalog } from "./federation/catalog";
import { createCatalogSignatureVerifier } from "./federation/signature";
import { federationRuntime } from "./federation/runtime";
import { RemoteSlot } from "./federation/RemoteSlot";

type CatalogState =
  | { status: "loading" }
  | { status: "ready"; catalog: RemoteCatalog }
  | { status: "failed"; error: Error };

export function App(): ReactNode {
  const [attempt, setAttempt] = useState(0);
  const [bootstrap, setBootstrap] = useState<{ config: ShellConfig; catalog: RemoteCatalog } | null>(null);
  const [state, setState] = useState<CatalogState>({ status: "loading" });
  const config = bootstrap?.config;
  const platform = useMemo<PlatformServices>(() => ({
    callApi: createCallApi({
      defaultTimeoutMs: config?.apiTimeoutMs ?? 15_000,
      maxResponseBytes: config?.maxApiResponseBytes ?? 4 * 1024 * 1024,
      ...(config === undefined ? {} : { startTrace: startApiTrace }),
      onProtocolError: (error) => reportError(error, { remote: "shell", phase: "api-protocol" }),
    }),
    navigate: (path) => history.pushState({}, "", path),
    reportError,
  }), [config?.apiTimeoutMs, config?.maxApiResponseBytes]);

  useEffect(() => {
    let active = true;
    setState({ status: "loading" });
    setBootstrap(null);
    void loadShellConfig().then(async (loadedConfig) => {
      initializeBrowserTelemetry({
        serviceName: "ods-shell",
        serviceVersion: loadedConfig.shellVersion,
        ...(loadedConfig.telemetryTraceEndpoint === undefined
          ? {}
          : { otlpTraceEndpoint: loadedConfig.telemetryTraceEndpoint }),
      });
      return {
        config: loadedConfig,
        catalog: await loadCatalog(loadedConfig, createCatalogSignatureVerifier(loadedConfig.catalogVerificationKeys)),
      };
    }).then(
      (loaded) => { if (active) { setBootstrap(loaded); setState({ status: "ready", catalog: loaded.catalog }); } },
      (error: unknown) => {
        if (active) setState({ status: "failed", error: error instanceof Error ? error : new Error("catalog failure") });
      },
    );
    return () => { active = false; };
  }, [attempt]);

  let content: ReactNode;
  if (state.status === "loading") {
    content = <Page title="Starting ODS"><LoadingPanel label="Loading application catalog" /></Page>;
  } else if (state.status === "failed") {
    content = <Page title="ODS unavailable"><ErrorPanel title="Application catalog could not be loaded" detail={state.error.message} onRetry={() => setAttempt((value) => value + 1)} /></Page>;
  } else {
    const taskRemote = state.catalog.remotes.find((remote) => remote.name === "task-mfe");
    content = taskRemote && config
      ? <RemoteSlot entry={taskRemote} runtime={federationRuntime} timeoutMs={config.remoteTimeoutMs} platform={platform} />
      : <Page title="No module"><ErrorPanel title="Task module is not configured" /></Page>;
  }

  return <OdsThemeProvider><ShellHeader title="ODS Platform" />{content}</OdsThemeProvider>;
}

function reportError(error: unknown, context: { remote: string; phase: string }): void {
  void error;
  recordComponentError(context);
}
