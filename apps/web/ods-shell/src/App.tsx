import { useEffect, useMemo, useState, type ReactNode } from "react";
import { createCallApi } from "@ods/call-api";
import type { PlatformServices, RemoteCatalog } from "@ods/mfe-contract";
import { Box, ErrorPanel, LoadingPanel, OdsThemeProvider, Page } from "@ods/shared-ui";
import { initializeBrowserTelemetry, recordComponentError, startApiTrace } from "@ods/telemetry";
import { loadShellConfig, type ShellConfig } from "./config";
import { loadCatalog } from "./federation/catalog";
import { createCatalogSignatureVerifier } from "./federation/signature";
import { federationRuntime } from "./federation/runtime";
import { RemoteSlot } from "./federation/RemoteSlot";
import { navigateShell, resolveRemoteForPath } from "./navigation";
import { WorkspaceSidebar, formatRemoteName } from "./components/WorkspaceSidebar";
import { WorkspaceNavbar } from "./components/WorkspaceNavbar";

type CatalogState =
  | { status: "loading" }
  | { status: "ready"; catalog: RemoteCatalog }
  | { status: "failed"; error: Error };
type AuthState = { status: "checking" } | { status: "authenticated" } | { status: "failed"; error: Error };

export function App(): ReactNode {
  const [attempt, setAttempt] = useState(0);
  const [bootstrap, setBootstrap] = useState<{ config: ShellConfig; catalog: RemoteCatalog } | null>(null);
  const [state, setState] = useState<CatalogState>({ status: "loading" });
  const [auth, setAuth] = useState<AuthState>({ status: "checking" });
  const [activeRemote, setActiveRemote] = useState("case-mfe");
  const [currentPath, setCurrentPath] = useState(() => window.location.pathname);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [moduleSearch, setModuleSearch] = useState("");
  const config = bootstrap?.config;
  const platform = useMemo<PlatformServices>(() => ({
    callApi: createCallApi({
      defaultTimeoutMs: config?.apiTimeoutMs ?? 15_000,
      maxResponseBytes: config?.maxApiResponseBytes ?? 4 * 1024 * 1024,
      ...(config === undefined ? {} : { startTrace: startApiTrace }),
      onProtocolError: (error) => reportError(error, { remote: "shell", phase: "api-protocol" }),
    }),
    navigate: navigateShell,
    reportError,
  }), [config?.apiTimeoutMs, config?.maxApiResponseBytes]);

  useEffect(() => {
    let active = true;
    void fetch("/auth/session", { credentials: "include", cache: "no-store", headers: { Accept: "application/json" } })
      .then((response) => {
        if (response.status === 401) {
          const returnTo = encodeURIComponent(window.location.href);
          window.location.assign(`/auth/oidc/login?return_to=${returnTo}`);
          return;
        }
        if (!response.ok) throw new Error(`Session lookup failed (${response.status})`);
        if (active) setAuth({ status: "authenticated" });
      })
      .catch((error: unknown) => {
        if (active) setAuth({ status: "failed", error: error instanceof Error ? error : new Error("Session lookup failed") });
      });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    const syncPath = () => setCurrentPath(window.location.pathname);
    window.addEventListener("popstate", syncPath);
    return () => window.removeEventListener("popstate", syncPath);
  }, []);

  useEffect(() => {
    if (auth.status !== "authenticated") return;
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
  }, [attempt, auth.status]);

  let content: ReactNode;
  let selectedRemoteName: string | undefined;
  let availableRemotes: RemoteCatalog["remotes"] = [];
  if (auth.status === "checking") {
    content = <Page title="Signing in"><LoadingPanel label="Checking Keycloak session" /></Page>;
  } else if (auth.status === "failed") {
    content = <Page title="Sign in unavailable"><ErrorPanel title="Could not verify your session" detail={auth.error.message} onRetry={() => window.location.reload()} /></Page>;
  } else if (state.status === "loading") {
    content = <Page title="Starting ODS"><LoadingPanel label="Loading application catalog" /></Page>;
  } else if (state.status === "failed") {
    content = <Page title="ODS unavailable"><ErrorPanel title="Application catalog could not be loaded" detail={state.error.message} onRetry={() => setAttempt((value) => value + 1)} /></Page>;
  } else {
    const remotes = state.catalog.remotes;
    availableRemotes = remotes;
    const selected = resolveRemoteForPath(remotes, currentPath)
      ?? remotes.find((remote) => remote.name === activeRemote)
      ?? remotes[0];
    selectedRemoteName = selected?.name;
    content = selected && config
      ? <RemoteSlot key={selected.name} entry={selected} runtime={federationRuntime} timeoutMs={config.remoteTimeoutMs} platform={platform} />
      : <Page title="No module"><ErrorPanel title="No modules are configured" /></Page>;
  }

  const localCasePreview = state.status === "ready" && state.catalog.remotes.some((remote) => remote.name === "case-mfe");
  const notificationRemote = availableRemotes.find((remote) => remote.name === "notification-mfe");
  const activeTitle = selectedRemoteName === undefined ? "Workspace" : formatRemoteName(selectedRemoteName);
  return <OdsThemeProvider><Box sx={{ display: "flex", minHeight: "100vh", bgcolor: "#f5f7f5" }}>
    <WorkspaceSidebar
      remotes={availableRemotes}
      filter={moduleSearch}
      activeRemoteName={selectedRemoteName}
      collapsed={sidebarCollapsed}
      onSelect={(remote) => { setActiveRemote(remote.name); if (remote.routeBasePath !== undefined) navigateShell(remote.routeBasePath); }}
      onToggleCollapsed={() => setSidebarCollapsed((value) => !value)}
    />
    <Box sx={{ display: "flex", flexDirection: "column", flex: 1, minWidth: 0 }}>
      <WorkspaceNavbar
        title={activeTitle}
        localPreview={localCasePreview}
        canSearchModules={availableRemotes.length > 1}
        moduleSearch={moduleSearch}
        loading={state.status === "loading"}
        onModuleSearch={setModuleSearch}
        onRefresh={() => setAttempt((value) => value + 1)}
        onOpenNotifications={notificationRemote ? () => {
          setActiveRemote(notificationRemote.name);
          navigateShell(notificationRemote.routeBasePath ?? "/notifications");
        } : undefined}
      />
      <Box component="main" sx={{ minWidth: 0, flex: 1 }}>{content}</Box>
    </Box>
  </Box></OdsThemeProvider>;
}

function reportError(error: unknown, context: { remote: string; phase: string }): void {
  void error;
  recordComponentError(context);
}
