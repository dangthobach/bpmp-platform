import { useEffect, useState, type ReactNode } from "react";
import type { PlatformServices, RemoteCatalogEntry, RemoteRouteModule } from "@ods/mfe-contract";
import { ErrorPanel, LoadingPanel, Page } from "@ods/shared-ui";
import { RemoteBoundary } from "./RemoteBoundary";
import type { FederationRuntime } from "./runtime";

interface Props {
  entry: RemoteCatalogEntry;
  runtime: FederationRuntime;
  timeoutMs: number;
  platform: PlatformServices;
}

type LoadState =
  | { status: "loading" }
  | { status: "ready"; module: RemoteRouteModule }
  | { status: "failed"; error: Error };

export function RemoteSlot({ entry, runtime, timeoutMs, platform }: Props): ReactNode {
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<LoadState>({ status: "loading" });

  useEffect(() => {
    let active = true;
    setState({ status: "loading" });
    void runtime.load(entry, timeoutMs).then(
      (module) => { if (active) setState({ status: "ready", module }); },
      (error: unknown) => {
        if (!active) return;
        const normalized = error instanceof Error ? error : new Error("unknown remote load failure");
        platform.reportError(normalized, { remote: entry.name, phase: "load" });
        setState({ status: "failed", error: normalized });
      },
    );
    return () => { active = false; };
  }, [attempt, entry, platform, runtime, timeoutMs]);

  if (state.status === "loading") return <Page title="Loading module"><LoadingPanel label={`Loading ${entry.name}`} /></Page>;
  if (state.status === "failed") return <Page title="Module unavailable"><ErrorPanel
    title={`Could not load ${entry.name}`}
    detail={state.error.message}
    onRetry={() => setAttempt((value) => value + 1)}
  /></Page>;

  const RemoteComponent = state.module.route.Component;
  return <RemoteBoundary
    remoteName={entry.name}
    onError={(error, phase) => platform.reportError(error, { remote: entry.name, phase })}
  ><RemoteComponent platform={platform} /></RemoteBoundary>;
}
