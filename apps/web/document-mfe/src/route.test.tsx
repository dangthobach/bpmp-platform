import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";

describe("Document MFE", () => {
  it("enforces 50 MiB limit and disables download for quarantined documents", async () => {
    const callApi = vi.fn().mockResolvedValue({
      ok: true,
      requestId: "r1",
      correlationId: "c1",
      data: {
        as_of: "2026-11-27T10:00:00Z",
        documents: [
          {
            id: "doc-1",
            owner_ref_type: "CASE",
            owner_ref_id: "case-1",
            file_name: "pending-scan.pdf",
            mime_type: "application/pdf",
            size_bytes: 2048,
            status: "SCANNING",
            legal_hold: false,
            ocr_status: "NOT_REQUESTED",
            version: 1,
          },
        ],
      },
    });
    const platform: PlatformServices = {
      callApi,
      navigate: vi.fn(),
      reportError: vi.fn(),
    };
    const Component = route.Component;
    render(<OdsThemeProvider><Component platform={platform} /></OdsThemeProvider>);

    await waitFor(() => expect(screen.getByText("pending-scan.pdf")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Download" })).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Declared Size (bytes)"), { target: { value: "60000000" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Session" }));
    expect(await screen.findByText(/52,428,800 bytes/i)).toBeInTheDocument();
  });
});
