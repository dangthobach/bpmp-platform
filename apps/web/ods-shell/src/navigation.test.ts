// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import type { RemoteCatalogEntry } from "@ods/mfe-contract";
import { navigateShell, pathMatchesBase, resolveRemoteForPath } from "./navigation";

const remote = (name: string, routeBasePath: string): RemoteCatalogEntry => ({
  name,
  routeBasePath,
  entry: `https://assets.example/${name}/mf-manifest.json`,
  exposedModule: "route",
  contractVersion: "1.0",
  minShellVersion: "1.0",
  requiredCapabilities: [],
});
const remotes = [remote("case-mfe", "/cases"), remote("task-mfe", "/tasks"), remote("admin-mfe", "/admin"), remote("admin-users-mfe", "/admin/users")];

describe("Shell route resolution", () => {
  it("selects a remote for its base path and nested pages", () => {
    expect(resolveRemoteForPath(remotes, "/tasks")).toMatchObject({ name: "task-mfe" });
    expect(resolveRemoteForPath(remotes, "/tasks/work-item-1")).toMatchObject({ name: "task-mfe" });
  });

  it("prefers the most specific signed route", () => {
    expect(resolveRemoteForPath(remotes, "/admin/users/42")).toMatchObject({ name: "admin-users-mfe" });
  });

  it("does not match a path that only shares a prefix", () => {
    expect(pathMatchesBase("/tasks-old", "/tasks")).toBe(false);
    expect(resolveRemoteForPath(remotes, "/tasks-old")).toBeUndefined();
  });
});

describe("navigateShell", () => {
  it("updates same-origin history and notifies route subscribers", () => {
    const listener = vi.fn();
    window.addEventListener("popstate", listener);
    try {
      navigateShell("/tasks?scope=mine#list");
      expect(window.location.pathname).toBe("/tasks");
      expect(window.location.search).toBe("?scope=mine");
      expect(window.location.hash).toBe("#list");
      expect(listener).toHaveBeenCalledOnce();
    } finally {
      window.removeEventListener("popstate", listener);
      window.history.replaceState({}, "", "/");
    }
  });

  it("rejects cross-origin navigation", () => {
    expect(() => navigateShell("https://evil.example/path")).toThrow("same-origin");
  });
});
