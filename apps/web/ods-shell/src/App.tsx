import { useEffect, useMemo, useState, type ReactNode } from "react";
import { createCallApi } from "@ods/call-api";
import type { PlatformServices, RemoteCatalog } from "@ods/mfe-contract";
import { Box, Button, ErrorPanel, LoadingPanel, OdsThemeProvider, Page, ShellHeader } from "@ods/shared-ui";
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
  const [activeRemote, setActiveRemote] = useState("case-mfe");
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
    const remotes = state.catalog.remotes;
    const selected = remotes.find((remote) => remote.name === activeRemote) ?? remotes[0];
    content = selected && config
      ? <>{remotes.length > 1 ? <Box component="nav" aria-label="ODS modules" sx={{ display: "flex", gap: 1, px: { xs: 2, md: 3 }, py: 1.5, bgcolor: "background.paper", borderBottom: "1px solid #e2e7e3" }}>{remotes.map((remote) => <Button key={remote.name} type="button" aria-current={remote.name === selected.name ? "page" : undefined} variant={remote.name === selected.name ? "contained" : "text"} onClick={() => setActiveRemote(remote.name)}>{remote.name.replace("-mfe", "").replaceAll("-", " ")}</Button>)}</Box> : null}<RemoteSlot key={selected.name} entry={selected} runtime={federationRuntime} timeoutMs={config.remoteTimeoutMs} platform={platform} /></>
      : <Page title="No module"><ErrorPanel title="No modules are configured" /></Page>;
  }

  const localCasePreview = state.status === "ready" && state.catalog.remotes.length === 1 && state.catalog.remotes[0]?.name === "case-mfe";
  return <OdsThemeProvider><ShellHeader title="ODS Platform" label={localCasePreview ? "Case local preview" : "ODS workspace"} />{content}</OdsThemeProvider>;
}

function reportError(error: unknown, context: { remote: string; phase: string }): void {
  void error;
  recordComponentError(context);
}
