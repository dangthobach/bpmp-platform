import { render, screen, waitFor } from "@testing-library/react";
import { SWRConfig } from "swr";
import { describe, expect, it, vi } from "vitest";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";

describe("PersonalWorklist", () => {
  it("renders data returned by the injected API contract", async () => {
    const platform: PlatformServices = {
      callApi: vi.fn().mockResolvedValue({
        ok: true,
        data: { work_items: [{ work_item_id: "task-1", task_name: "Review application", status: "OPEN" }] },
        requestId: "request-1",
        correlationId: "correlation-1",
      }),
      navigate: vi.fn(),
      reportError: vi.fn(),
    };
    const Component = route.Component;

    render(<SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <OdsThemeProvider><Component platform={platform} /></OdsThemeProvider>
    </SWRConfig>);

    expect(screen.getByRole("status")).toHaveTextContent("Loading assigned tasks");
    await waitFor(() => expect(screen.getByText("Review application")).toBeInTheDocument());
    expect(platform.callApi).toHaveBeenCalledWith({ method: "GET", path: "/v1/work-items?page_size=25" });
  });

  it("shows a scoped retry for retryable API failures", async () => {
    const callApi = vi.fn().mockResolvedValue({
      ok: false,
      problem: { title: "Upstream unavailable", code: "upstream_unavailable", retryable: true },
      requestId: "request-1",
      correlationId: "correlation-1",
    });
    const Component = route.Component;
    render(<SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <OdsThemeProvider><Component platform={{ callApi, navigate: vi.fn(), reportError: vi.fn() }} /></OdsThemeProvider>
    </SWRConfig>);

    await waitFor(() => expect(screen.getByText("Upstream unavailable")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
