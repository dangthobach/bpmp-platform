// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import type { RemoteCatalogEntry } from "@ods/mfe-contract";
import {
  KNOWN_WORKSPACE_ROUTES,
  filterRemotesByCapabilities,
  navigateShell,
  pathMatchesBase,
  resolveRemoteForPath,
} from "./navigation";

const remote = (name: string, routeBasePath: string, requiredCapabilities: string[] = []): RemoteCatalogEntry => ({
  name,
  routeBasePath,
  entry: `https://assets.example/${name}/mf-manifest.json`,
  exposedModule: "route",
  contractVersion: "1.0",
  minShellVersion: "1.0",
  requiredCapabilities,
});
const remotes = [
  remote("case-mfe", "/cases", ["case.read"]),
  remote("task-mfe", "/tasks", ["task.read"]),
  remote("notification-mfe", "/notifications", ["notification.read"]),
  remote("admin-mfe", "/admin"),
  remote("admin-users-mfe", "/admin/users"),
];

describe("Shell route resolution", () => {
  it("selects a remote for its base path and nested pages, including /notifications", () => {
    expect(resolveRemoteForPath(remotes, "/tasks")).toMatchObject({ name: "task-mfe" });
    expect(resolveRemoteForPath(remotes, "/tasks/work-item-1")).toMatchObject({ name: "task-mfe" });
    expect(resolveRemoteForPath(remotes, "/notifications")).toMatchObject({ name: "notification-mfe" });
    expect(resolveRemoteForPath(remotes, "/notifications/monitoring/deliveries")).toMatchObject({
      name: "notification-mfe",
    });
  });

  it("prefers the most specific signed route", () => {
    expect(resolveRemoteForPath(remotes, "/admin/users/42")).toMatchObject({ name: "admin-users-mfe" });
  });

  it("does not match a path that only shares a prefix", () => {
    expect(pathMatchesBase("/tasks-old", "/tasks")).toBe(false);
    expect(resolveRemoteForPath(remotes, "/tasks-old")).toBeUndefined();
    expect(resolveRemoteForPath(remotes, "/notifications-legacy")).toBeUndefined();
  });

  it("gates catalog remotes by granted capabilities when session capability set is provided", () => {
    expect(KNOWN_WORKSPACE_ROUTES.find((entry) => entry.remoteName === "notification-mfe")).toEqual({
      remoteName: "notification-mfe",
      routeBasePath: "/notifications",
      title: "Notifications",
      requiredCapabilities: ["notification.read"],
    });
    expect(filterRemotesByCapabilities(remotes).map((item) => item.name)).toContain("notification-mfe");
    const withoutNotification = filterRemotesByCapabilities(remotes, new Set(["case.read", "task.read"]));
    expect(withoutNotification.map((item) => item.name)).toEqual([
      "case-mfe",
      "task-mfe",
      "admin-mfe",
      "admin-users-mfe",
    ]);
    const withNotification = filterRemotesByCapabilities(
      remotes,
      new Set(["case.read", "task.read", "notification.read"]),
    );
    expect(withNotification.map((item) => item.name)).toContain("notification-mfe");
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
