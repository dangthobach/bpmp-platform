import { useQueryClient } from "@tanstack/react-query";
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type PropsWithChildren,
} from "react";
import { useAuth } from "../auth/AuthContext";
import { useRuntimeConfig } from "../config/ConfigContext";
import { readServerSentEvents, type ServerSentEvent } from "./sse";

export type RealtimeStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "unauthorized";

const RealtimeContext = createContext<RealtimeStatus>("connecting");

export function RealtimeProvider({ children }: PropsWithChildren) {
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<RealtimeStatus>("connecting");

  useEffect(() => {
    if (!identity) return;
    const connectedIdentity = identity;
    const controller = new AbortController();
    let cursor = "";
    let reconnectDelay = config.realtimeReconnectInitialMs;

    function reconcile(event: ServerSentEvent) {
      if (event.id) cursor = event.id;
      switch (event.event) {
        case "work-item.changed":
          void queryClient.invalidateQueries({ queryKey: ["work-items"] });
          break;
        case "case.changed":
          void queryClient.invalidateQueries({ queryKey: ["case"] });
          break;
        case "audit.changed":
          void queryClient.invalidateQueries({ queryKey: ["audit"] });
          break;
        case "workflow.changed":
          void queryClient.invalidateQueries({ queryKey: ["workflow-instance"] });
          break;
        case "resync-required":
          void Promise.all([
            queryClient.invalidateQueries({ queryKey: ["work-items"] }),
            queryClient.invalidateQueries({ queryKey: ["case"] }),
            queryClient.invalidateQueries({ queryKey: ["audit"] }),
            queryClient.invalidateQueries({ queryKey: ["workflow-instance"] }),
          ]);
          break;
      }
    }

    async function connect() {
      while (!controller.signal.aborted) {
        setStatus(cursor ? "reconnecting" : "connecting");
        try {
          const url = new URL(config.realtimePath, config.realtimeBaseUrl);
          url.searchParams.set("names", config.realtimeSignalNames.join(","));
          const headers = new Headers({
            Accept: "text/event-stream",
            Authorization: `Bearer ${connectedIdentity.accessToken}`,
            "X-BPMP-Tenant-ID": connectedIdentity.tenantId,
            "X-Correlation-ID": crypto.randomUUID(),
          });
          if (cursor) headers.set("Last-Event-ID", cursor);
          const response = await fetch(url, {
            method: "GET",
            headers,
            cache: "no-store",
            signal: controller.signal,
          });
          if (response.status === 401 || response.status === 403) {
            setStatus("unauthorized");
            return;
          }
          if (!response.ok || !response.body) {
            throw new Error(`Realtime connection failed with ${response.status}`);
          }
          setStatus("connected");
          reconnectDelay = config.realtimeReconnectInitialMs;
          await readServerSentEvents(response.body, reconcile, controller.signal);
        } catch (error) {
          if (controller.signal.aborted) return;
          console.warn("Realtime connection interrupted", error);
        }
        setStatus("reconnecting");
        await wait(reconnectDelay, controller.signal);
        reconnectDelay = Math.min(
          reconnectDelay * 2,
          config.realtimeReconnectMaxMs,
        );
      }
    }

    void connect();
    return () => controller.abort();
  }, [config, identity, queryClient]);

  const value = useMemo(() => status, [status]);
  return (
    <RealtimeContext.Provider value={value}>
      {children}
    </RealtimeContext.Provider>
  );
}

export function useRealtimeStatus(): RealtimeStatus {
  return useContext(RealtimeContext);
}

function wait(durationMs: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = window.setTimeout(resolve, durationMs);
    signal.addEventListener(
      "abort",
      () => {
        window.clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
  });
}
