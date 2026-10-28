export interface ShellConfig {
  shellVersion: string;
  catalogUrl: string;
  catalogTimeoutMs: number;
  remoteTimeoutMs: number;
  apiTimeoutMs: number;
  maxApiResponseBytes: number;
  allowedRemoteOrigins: ReadonlySet<string>;
  allowUnsignedCatalog: boolean;
}

export function shellConfig(): ShellConfig {
  const localRemoteOrigin = "http://127.0.0.1:4301";
  return {
    shellVersion: "1.0",
    catalogUrl: "/mfe-catalog.json",
    catalogTimeoutMs: 5_000,
    remoteTimeoutMs: 10_000,
    apiTimeoutMs: 15_000,
    maxApiResponseBytes: 4 * 1024 * 1024,
    allowedRemoteOrigins: new Set([location.origin, localRemoteOrigin]),
    allowUnsignedCatalog: import.meta.env.DEV,
  };
}
