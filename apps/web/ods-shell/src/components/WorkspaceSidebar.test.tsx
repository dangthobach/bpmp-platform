// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RemoteCatalogEntry } from "@ods/mfe-contract";
import { WorkspaceSidebar } from "./WorkspaceSidebar";

const remotes: RemoteCatalogEntry[] = [
  { name: "task-mfe", routeBasePath: "/tasks", entry: "https://assets.example/task/mf-manifest.json", exposedModule: "route", contractVersion: "1.0", minShellVersion: "1.0", requiredCapabilities: ["task.read"] },
  { name: "case-mfe", routeBasePath: "/cases", entry: "https://assets.example/case/mf-manifest.json", exposedModule: "route", contractVersion: "1.0", minShellVersion: "1.0", requiredCapabilities: ["case.read"] },
];

afterEach(cleanup);

describe("WorkspaceSidebar", () => {
  it("marks the active catalog route and sends the selected remote to Shell", () => {
    const onSelect = vi.fn();
    render(<WorkspaceSidebar remotes={remotes} filter="" activeRemoteName="case-mfe" collapsed={false} onSelect={onSelect} onToggleCollapsed={() => undefined} />);

    expect(screen.getByRole("button", { name: "case" }).getAttribute("aria-current")).toBe("page");
    fireEvent.click(screen.getByRole("button", { name: "task" }));
    expect(onSelect).toHaveBeenCalledWith(remotes[0]);
  });

  it("filters catalog routes without changing the available route list", () => {
    render(<WorkspaceSidebar remotes={remotes} filter="case" activeRemoteName="case-mfe" collapsed={false} onSelect={() => undefined} onToggleCollapsed={() => undefined} />);

    expect(screen.getByRole("button", { name: "case" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "task" })).toBeNull();
  });

  it("keeps the collapsed toggle accessible", () => {
    const onToggleCollapsed = vi.fn();
    render(<WorkspaceSidebar remotes={remotes} filter="" activeRemoteName="task-mfe" collapsed={true} onSelect={() => undefined} onToggleCollapsed={onToggleCollapsed} />);
    const toggle = screen.getByRole("button", { name: "Expand navigation" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(toggle);
    expect(onToggleCollapsed).toHaveBeenCalledOnce();
  });
});
