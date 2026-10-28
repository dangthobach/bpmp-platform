import { useEffect, useMemo, useState, type ReactNode } from "react";
import { createCallApi } from "@ods/call-api";
import type { PlatformServices, RemoteCatalog } from "@ods/mfe-contract";
import { ErrorPanel, LoadingPanel, OdsThemeProvider, Page, ShellHeader } from "@ods/shared-ui";
import { shellConfig } from "./config";
import { loadCatalog } from "./federation/catalog";
import { federationRuntime } from "./federation/runtime";
import { RemoteSlot } from "./federation/RemoteSlot";

type CatalogState =
  | { status: "loading" }
  | { status: "ready"; catalog: RemoteCatalog }
  | { status: "failed"; error: Error };

export function App(): ReactNode {
  const config = useMemo(shellConfig, []);
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<CatalogState>({ status: "loading" });
  const platform = useMemo<PlatformServices>(() => ({
    callApi: createCallApi({
      defaultTimeoutMs: config.apiTimeoutMs,
      maxResponseBytes: config.maxApiResponseBytes,
      onProtocolError: (error) => reportError(error, { remote: "shell", phase: "api-protocol" }),
    }),
    navigate: (path) => history.pushState({}, "", path),
    reportError,
  }), [config]);

  useEffect(() => {
    let active = true;
    setState({ status: "loading" });
    void loadCatalog(config, async () => false).then(
      (catalog) => { if (active) setState({ status: "ready", catalog }); },
      (error: unknown) => {
        if (active) setState({ status: "failed", error: error instanceof Error ? error : new Error("catalog failure") });
      },
    );
    return () => { active = false; };
  }, [attempt, config]);

  let content: ReactNode;
  if (state.status === "loading") {
    content = <Page title="Starting ODS"><LoadingPanel label="Loading application catalog" /></Page>;
  } else if (state.status === "failed") {
    content = <Page title="ODS unavailable"><ErrorPanel title="Application catalog could not be loaded" detail={state.error.message} onRetry={() => setAttempt((value) => value + 1)} /></Page>;
  } else {
    const taskRemote = state.catalog.remotes.find((remote) => remote.name === "task-mfe");
    content = taskRemote
      ? <RemoteSlot entry={taskRemote} runtime={federationRuntime} timeoutMs={config.remoteTimeoutMs} platform={platform} />
      : <Page title="No module"><ErrorPanel title="Task module is not configured" /></Page>;
  }

  return <OdsThemeProvider><ShellHeader title="ODS Platform" />{content}</OdsThemeProvider>;
}

function reportError(error: unknown, context: { remote: string; phase: string }): void {
  const message = error instanceof Error ? error.message : "unknown error";
  // The telemetry adapter intentionally accepts metadata only. Never attach response bodies, tokens or PII.
  console.error("ODS component failure", { ...context, message });
}
