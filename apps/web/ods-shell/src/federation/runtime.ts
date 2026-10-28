import { loadRemote, registerRemotes } from "@module-federation/runtime";
import {
  assertRemoteRouteModule,
  type RemoteCatalogEntry,
  type RemoteRouteModule,
} from "@ods/mfe-contract";

export interface FederationRuntime {
  load(entry: RemoteCatalogEntry, timeoutMs: number): Promise<RemoteRouteModule>;
}

export const federationRuntime: FederationRuntime = {
  async load(entry, timeoutMs) {
    registerRemotes([{ name: entry.name, alias: entry.name, entry: entry.entry }]);
    const loaded = await withTimeout(
      loadRemote(`${entry.name}/${entry.exposedModule}`),
      timeoutMs,
      `remote ${entry.name} load timed out`,
    );
    return assertRemoteRouteModule(loaded, entry.contractVersion);
  },
};

export async function withTimeout<T>(promise: Promise<T>, timeoutMs: number, message: string): Promise<T> {
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((_, reject) => {
        timeout = setTimeout(() => reject(new Error(message)), timeoutMs);
      }),
    ]);
  } finally {
    if (timeout !== undefined) clearTimeout(timeout);
  }
}
