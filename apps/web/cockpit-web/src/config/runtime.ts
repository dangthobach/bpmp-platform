import { z } from "zod";

const runtimeConfigSchema = z.object({
  apiBaseUrl: z.string().url(),
  organizationApiBaseUrl: z.string().url(),
  realtimeBaseUrl: z.string().url(),
  realtimePath: z.string().startsWith("/"),
  realtimeSignalNames: z.array(z.enum([
    "workflow.changed",
    "work-item.changed",
    "case.changed",
    "audit.changed",
  ])).min(1),
  realtimeReconnectInitialMs: z.number().int().positive(),
  realtimeReconnectMaxMs: z.number().int().positive(),
  defaultPageSize: z.number().int().positive(),
  maxPageSize: z.number().int().positive(),
  requestTimeoutMs: z.number().int().positive(),
  staleTimeMs: z.number().int().nonnegative(),
}).refine(
  (value) => value.defaultPageSize <= value.maxPageSize,
  "defaultPageSize must not exceed maxPageSize",
).refine(
  (value) => value.realtimeReconnectInitialMs <= value.realtimeReconnectMaxMs,
  "realtimeReconnectInitialMs must not exceed realtimeReconnectMaxMs",
);

export type RuntimeConfig = z.infer<typeof runtimeConfigSchema> & {
  batchChunkSize: number;
  batchConcurrency: number;
};

let configPromise: Promise<RuntimeConfig> | undefined;

export function loadRuntimeConfig(): Promise<RuntimeConfig> {
  configPromise ??= fetch("/config.json", { cache: "no-store" })
    .then(async (response) => {
      if (!response.ok) {
        throw new Error(`Runtime configuration failed with ${response.status}`);
      }
      const deployment = runtimeConfigSchema.parse(await response.json());
      return { ...deployment, batchChunkSize: 0, batchConcurrency: 0 };
    });
  return configPromise;
}
