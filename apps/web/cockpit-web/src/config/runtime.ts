import { z } from "zod";

const runtimeConfigSchema = z.object({
  apiBaseUrl: z.string().url(),
  organizationApiBaseUrl: z.string().url(),
  defaultPageSize: z.number().int().positive(),
  maxPageSize: z.number().int().positive(),
  batchChunkSize: z.number().int().positive(),
  batchConcurrency: z.number().int().positive(),
  requestTimeoutMs: z.number().int().positive(),
  staleTimeMs: z.number().int().nonnegative(),
}).refine(
  (value) => value.defaultPageSize <= value.maxPageSize,
  "defaultPageSize must not exceed maxPageSize",
);

export type RuntimeConfig = z.infer<typeof runtimeConfigSchema>;

let configPromise: Promise<RuntimeConfig> | undefined;

export function loadRuntimeConfig(): Promise<RuntimeConfig> {
  configPromise ??= fetch("/config.json", { cache: "no-store" })
    .then(async (response) => {
      if (!response.ok) {
        throw new Error(`Runtime configuration failed with ${response.status}`);
      }
      return runtimeConfigSchema.parse(await response.json());
    });
  return configPromise;
}
