import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";

describe("Integration MFE", () => {
  it("renders endpoints, circuit breaker state, and supports CAS manual retry", async () => {
    const callApi = vi.fn().mockImplementation(async ({ path, method }: { path: string; method: string }) => {
      if (path === "/v1/admin/endpoints") {
        return {
          ok: true,
          requestId: "r1",
          correlationId: "c1",
          data: {
            endpoints: [
              { id: "ep-1", name: "T24 ESB", system_type: "T24_ESB", base_url: "https://esb.bank.example", secret_ref: "vault://t24", circuit_state: "OPEN", status: "ACTIVE" },
            ],
          },
        };
      }
      if (path === "/v1/admin/integration/deliveries") {
        return {
          ok: true,
          requestId: "r2",
          correlationId: "c2",
          data: {
            deliveries: [
              { id: "del-1", endpoint_id: "ep-1", downstream_idempotency_key: "key-1", redacted_summary: "AML Screen", status: "RECONCILING", attempt_count: 1, max_attempts: 3, version: 2 },
            ],
          },
        };
      }
      if (method === "POST" && path.endsWith("/retry")) {
        return { ok: true, requestId: "r3", correlationId: "c3", data: { delivery: { id: "del-1", status: "SUCCEEDED" } } };
      }
      return { ok: false, requestId: "r4", correlationId: "c4", problem: { title: "unexpected" } };
    });

    const platform: PlatformServices = { callApi, navigate: vi.fn(), reportError: vi.fn() };
    const Component = route.Component;
    render(<OdsThemeProvider><Component platform={platform} /></OdsThemeProvider>);

    await waitFor(() => expect(screen.getByText(/T24 ESB/)).toBeInTheDocument());
    expect(screen.getByText("Circuit: OPEN")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/preserved downstream key key-1/i)).toBeInTheDocument();
  });
});
