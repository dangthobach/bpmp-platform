import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";

describe("Monitoring MFE", () => {
  it("displays SLO cards after querying projection aggregate", async () => {
    const callApi = vi.fn().mockResolvedValue({
      ok: true,
      requestId: "r1",
      correlationId: "c1",
      data: { buckets: [{ key: "ACTIVE", count: 12 }] },
    });
    const platform: PlatformServices = { callApi, navigate: vi.fn(), reportError: vi.fn() };
    const Component = route.Component;
    render(<OdsThemeProvider><Component platform={platform} /></OdsThemeProvider>);

    await waitFor(() => expect(screen.getByText(/BPMP Engine \(Raft Quorum\)/)).toBeInTheDocument());
    expect(screen.getByText(/Document & Integration Hub/)).toBeInTheDocument();
  });
});
