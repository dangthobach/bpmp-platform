import type { RemoteCatalogEntry } from "@ods/mfe-contract";

export function resolveRemoteForPath(
  remotes: readonly RemoteCatalogEntry[],
  pathname: string,
): RemoteCatalogEntry | undefined {
  return remotes
    .filter((remote) => remote.routeBasePath !== undefined && pathMatchesBase(pathname, remote.routeBasePath))
    .sort((left, right) => (right.routeBasePath?.length ?? 0) - (left.routeBasePath?.length ?? 0))[0];
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
