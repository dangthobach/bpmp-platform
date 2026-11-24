import { describe, expect, it } from "vitest";
import { assertRemoteRouteModule, parseRemoteCatalog } from "./index";

const validCatalog = {
  schemaVersion: "1",
  catalogVersion: "catalog-1",
  generatedAt: "2026-10-28T00:00:00Z",
  expiresAt: "2026-10-29T00:00:00Z",
  remotes: [{
    name: "task-mfe",
    entry: "https://assets.example/task/v1/mf-manifest.json",
    exposedModule: "route",
    routeBasePath: "/tasks",
    contractVersion: "1.0",
    minShellVersion: "1.0",
    requiredCapabilities: ["task.read"],
  }],
};

describe("parseRemoteCatalog", () => {
  it("accepts an allowlisted manifest", () => {
    expect(parseRemoteCatalog(validCatalog, new Set(["https://assets.example"])).remotes).toHaveLength(1);
  });

  it("rejects a non-allowlisted origin", () => {
    expect(() => parseRemoteCatalog(validCatalog, new Set(["https://other.example"]))).toThrow(
      "not allowed",
    );
  });

  it("rejects duplicate remote names", () => {
    expect(() => parseRemoteCatalog(
      { ...validCatalog, remotes: [validCatalog.remotes[0], validCatalog.remotes[0]] },
      new Set(["https://assets.example"]),
    )).toThrow("duplicate remote name");
  });

  it("rejects a malformed remote version before registration", () => {
    expect(() => parseRemoteCatalog(
      { ...validCatalog, remotes: [{ ...validCatalog.remotes[0], minShellVersion: "latest" }] },
      new Set(["https://assets.example"]),
    )).toThrow("numeric major.minor");
  });

  it.each(["tasks", "//evil.example/tasks", "/tasks?admin=1", "/tasks/#section", "/tasks/"])("rejects unsafe route base %s", (routeBasePath) => {
    expect(() => parseRemoteCatalog(
      { ...validCatalog, remotes: [{ ...validCatalog.remotes[0], routeBasePath }] },
      new Set(["https://assets.example"]),
    )).toThrow("routeBasePath");
  });

  it("rejects duplicate route roots", () => {
    expect(() => parseRemoteCatalog(
      { ...validCatalog, remotes: [validCatalog.remotes[0], { ...validCatalog.remotes[0], name: "case-mfe" }] },
      new Set(["https://assets.example"]),
    )).toThrow("duplicate remote route");
  });
});

describe("assertRemoteRouteModule", () => {
  it("rejects a malformed imported module", () => {
    expect(() => assertRemoteRouteModule({
      contractVersion: "1.0",
      route: { path: "tasks", title: "Tasks", Component: "not-a-component" },
    }, "1.0")).toThrow("incompatible");
  });

  it("rejects a route that differs from the signed catalog path", () => {
    expect(() => assertRemoteRouteModule({
      contractVersion: "1.0",
      route: { path: "/other", title: "Tasks", Component: () => null },
    }, "1.0", "/tasks")).toThrow("signed catalog route");
  });
});
