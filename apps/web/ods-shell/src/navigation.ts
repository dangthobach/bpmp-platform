import type { RemoteCatalogEntry } from "@ods/mfe-contract";

export interface WorkspaceRouteMetadata {
  readonly remoteName: string;
  readonly routeBasePath: string;
  readonly title: string;
  readonly requiredCapabilities: readonly string[];
}

export const KNOWN_WORKSPACE_ROUTES: readonly WorkspaceRouteMetadata[] = [
  { remoteName: "task-mfe", routeBasePath: "/tasks", title: "Tasks", requiredCapabilities: ["task.read"] },
  {
    remoteName: "studio-mfe",
    routeBasePath: "/studio",
    title: "Studio",
    requiredCapabilities: ["authoring.listProcessModel", "authz.catalog.read", "configuration.profile.read"],
  },
  { remoteName: "case-mfe", routeBasePath: "/cases", title: "Cases", requiredCapabilities: ["case.read"] },
  {
    remoteName: "notification-mfe",
    routeBasePath: "/notifications",
    title: "Notifications",
    requiredCapabilities: ["notification.read"],
  },
];

export function resolveRemoteForPath(
  remotes: readonly RemoteCatalogEntry[],
  pathname: string,
): RemoteCatalogEntry | undefined {
  return remotes
    .filter((remote) => remote.routeBasePath !== undefined && pathMatchesBase(pathname, remote.routeBasePath))
    .sort((left, right) => (right.routeBasePath?.length ?? 0) - (left.routeBasePath?.length ?? 0))[0];
}

export function filterRemotesByCapabilities(
  remotes: readonly RemoteCatalogEntry[],
  grantedCapabilities?: ReadonlySet<string>,
): RemoteCatalogEntry[] {
  if (grantedCapabilities === undefined) {
    return [...remotes];
  }
  return remotes.filter((remote) =>
    remote.requiredCapabilities.every((capability) => grantedCapabilities.has(capability)),
  );
}

export function pathMatchesBase(pathname: string, basePath: string): boolean {
  return basePath === "/" || pathname === basePath || pathname.startsWith(`${basePath}/`);
}

export function navigateShell(path: string): void {
  const target = new URL(path, window.location.origin);
  if (target.origin !== window.location.origin) throw new TypeError("Shell navigation must stay same-origin");
  window.history.pushState({}, "", `${target.pathname}${target.search}${target.hash}`);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
