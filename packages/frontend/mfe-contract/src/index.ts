import type { ComponentType } from "react";
import type { CallApi } from "@ods/call-api";

export const shellContractVersion = "1.0";

export interface PlatformServices {
  callApi: CallApi;
  navigate(path: string): void;
  reportError(error: unknown, context: { remote: string; phase: string }): void;
}

export interface RemoteRouteProps {
  platform: PlatformServices;
}

export interface RemoteRouteModule {
  contractVersion: string;
  route: {
    path: string;
    title: string;
    Component: ComponentType<RemoteRouteProps>;
  };
}

export interface RemoteCatalogEntry {
  name: string;
  entry: string;
  exposedModule: string;
  contractVersion: string;
  minShellVersion: string;
  requiredCapabilities: string[];
  integrity?: string;
}

export interface RemoteCatalog {
  schemaVersion: "1";
  catalogVersion: string;
  generatedAt: string;
  expiresAt: string;
  remotes: RemoteCatalogEntry[];
  signature?: string;
  keyId?: string;
}

export function parseRemoteCatalog(value: unknown, allowedOrigins: ReadonlySet<string>): RemoteCatalog {
  if (!isRecord(value) || value.schemaVersion !== "1" || typeof value.catalogVersion !== "string"
    || typeof value.generatedAt !== "string" || typeof value.expiresAt !== "string"
    || !Array.isArray(value.remotes)) {
    throw new TypeError("invalid MFE catalog envelope");
  }
  if (Number.isNaN(Date.parse(value.generatedAt))) throw new TypeError("invalid catalog timestamp");
  if (Number.isNaN(Date.parse(value.expiresAt))) throw new TypeError("invalid catalog expiry");
  const names = new Set<string>();
  const remotes = value.remotes.map((remote, index) => parseRemote(remote, index, allowedOrigins));
  for (const remote of remotes) {
    if (names.has(remote.name)) throw new TypeError(`duplicate remote name: ${remote.name}`);
    names.add(remote.name);
  }
  const catalog: RemoteCatalog = {
    schemaVersion: "1",
    catalogVersion: value.catalogVersion,
    generatedAt: value.generatedAt,
    expiresAt: value.expiresAt,
    remotes,
  };
  if (typeof value.signature === "string") catalog.signature = value.signature;
  if (typeof value.keyId === "string") catalog.keyId = value.keyId;
  return catalog;
}

export function catalogSigningBytes(catalog: RemoteCatalog): Uint8Array {
  const payload = {
    catalogVersion: catalog.catalogVersion,
    expiresAt: catalog.expiresAt,
    generatedAt: catalog.generatedAt,
    keyId: catalog.keyId ?? "",
    remotes: catalog.remotes.map((remote) => ({
      contractVersion: remote.contractVersion,
      entry: remote.entry,
      exposedModule: remote.exposedModule,
      integrity: remote.integrity ?? "",
      minShellVersion: remote.minShellVersion,
      name: remote.name,
      requiredCapabilities: [...remote.requiredCapabilities],
    })),
    schemaVersion: catalog.schemaVersion,
  };
  return new TextEncoder().encode(JSON.stringify(payload));
}

export function assertRemoteRouteModule(value: unknown, expectedContract: string): RemoteRouteModule {
  if (!isRecord(value) || value.contractVersion !== expectedContract || !isRecord(value.route)
    || typeof value.route.path !== "string" || !value.route.path.startsWith("/")
    || typeof value.route.title !== "string" || typeof value.route.Component !== "function") {
    throw new TypeError("remote route contract is incompatible");
  }
  return value as unknown as RemoteRouteModule;
}

function parseRemote(value: unknown, index: number, allowedOrigins: ReadonlySet<string>): RemoteCatalogEntry {
  if (!isRecord(value)) throw new TypeError(`remote[${index}] must be an object`);
  for (const key of ["name", "entry", "exposedModule", "contractVersion", "minShellVersion"] as const) {
    if (typeof value[key] !== "string" || value[key].length === 0) {
      throw new TypeError(`remote[${index}].${key} is required`);
    }
  }
  if (!/^[a-z][a-z0-9-]{1,63}$/.test(value.name as string)) throw new TypeError("invalid remote name");
  const entry = new URL(value.entry as string, globalThis.location?.origin ?? "http://localhost");
  if (!allowedOrigins.has(entry.origin)) throw new TypeError(`remote origin is not allowed: ${entry.origin}`);
  if (!entry.pathname.endsWith("/mf-manifest.json")) throw new TypeError("remote must use mf-manifest.json");
  if (!Array.isArray(value.requiredCapabilities)
    || value.requiredCapabilities.some((item) => typeof item !== "string")) {
    throw new TypeError("requiredCapabilities must be a string array");
  }
  const remote: RemoteCatalogEntry = {
    name: value.name as string,
    entry: entry.href,
    exposedModule: value.exposedModule as string,
    contractVersion: value.contractVersion as string,
    minShellVersion: value.minShellVersion as string,
    requiredCapabilities: [...value.requiredCapabilities] as string[],
  };
  if (typeof value.integrity === "string") remote.integrity = value.integrity;
  return remote;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
