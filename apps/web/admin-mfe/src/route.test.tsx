import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";

describe("Admin MFE", () => {
  it("renders versioned MDM datasets and audit projection generation watermark", async () => {
    const callApi = vi.fn().mockImplementation(async ({ path }: { path: string }) => {
      if (path === "/v1/mdm/datasets") {
        return {
          ok: true,
          requestId: "r1",
          correlationId: "c1",
          data: { datasets: [{ id: "mdm-1", code: "BRANCH_CODES", name: "Branch Codes", current_publication_version: 3, version: 3 }] },
        };
      }
      return {
        ok: true,
        requestId: "r2",
        correlationId: "c2",
        data: {
          projection_generation: 2,
          as_of: "2026-11-27T12:00:00Z",
          entries: [{ event_id: "e1", source_service: "case-service", aggregate_type: "Case", aggregate_id: "case-1", action: "CaseCreated", actor_id: "u1", occurred_at: "2026-11-27T11:59:00Z", projection_generation: 2 }],
        },
      };
    });

    const platform: PlatformServices = { callApi, navigate: vi.fn(), reportError: vi.fn() };
    const Component = route.Component;
    render(<OdsThemeProvider><Component platform={platform} /></OdsThemeProvider>);

    await waitFor(() => expect(screen.getByText(/Branch Codes/)).toBeInTheDocument());
    expect(screen.getByText("Published v3")).toBeInTheDocument();
    expect(screen.getByText(/Generation #2/)).toBeInTheDocument();
  });
});
