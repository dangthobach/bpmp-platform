import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CallApi } from "@ods/call-api";
import { OdsThemeProvider } from "@ods/shared-ui";
import moduleContract from "./route";

describe("Studio route", () => {
  it("lists process models, loads immutable versions and submits deployment with idempotency", async () => {
    const callApi = vi.fn(async (options: { method: string; path: string; body?: unknown; idempotencyKey?: string }) => {
      if (options.path === "/v1/authoring/process-models?page_size=50") return { ok: true as const, data: { models: [{ id: "process-1", name: "Claims" }] }, requestId: "r1", correlationId: "c1" };
      if (options.path.includes("/versions?")) return { ok: true as const, data: { versions: [{ id: "version-1" }] }, requestId: "r2", correlationId: "c2" };
      if (options.path.includes("/deployments") && options.method === "GET") return { ok: true as const, data: { deployments: [] }, requestId: "r3", correlationId: "c3" };
      return { ok: true as const, data: { deployment_id: "deployment-1" }, requestId: "r4", correlationId: "c4" };
    });
    const Component = moduleContract.route.Component;
    render(<OdsThemeProvider><Component platform={{ callApi: callApi as unknown as CallApi, navigate: vi.fn(), reportError: vi.fn() }} /></OdsThemeProvider>);

    await screen.findByText("process-1");
    fireEvent.click(screen.getByRole("button", { name: "Manage deployments" }));
    await waitFor(() => expect(callApi).toHaveBeenCalledWith(expect.objectContaining({ path: expect.stringContaining("/versions?") })));
    fireEvent.change(screen.getByLabelText("Immutable version ID"), { target: { value: "version-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Deploy version" }));
    await screen.findByText(/Deployment command accepted/);
    expect(callApi).toHaveBeenCalledWith(expect.objectContaining({ method: "POST", body: { process_model_version_id: "version-1", target_environment: "stage" }, idempotencyKey: expect.any(String) }));
  });
});
