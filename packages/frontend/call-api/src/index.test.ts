import type { AxiosInstance } from "axios";
import { describe, expect, it, vi } from "vitest";
import { createCallApi } from "./index";

describe("createCallApi", () => {
  it("propagates identities and returns typed success", async () => {
    const request = vi.fn().mockResolvedValue({
      status: 200,
      data: { work_items: [] },
      headers: { "x-request-id": "server-request", "x-correlation-id": "case-1" },
    });
    const callApi = createCallApi({
      defaultTimeoutMs: 1_000,
      maxResponseBytes: 1_000_000,
      transport: { request } as unknown as AxiosInstance,
    });

    const result = await callApi<{ work_items: unknown[] }>({
      method: "GET",
      path: "/v1/work-items",
      correlationId: "case-1",
    });

    expect(result).toEqual({
      ok: true,
      data: { work_items: [] },
      requestId: "server-request",
      correlationId: "case-1",
    });
    expect(request).toHaveBeenCalledWith(expect.objectContaining({
      headers: expect.objectContaining({ "X-Correlation-ID": "case-1" }),
    }));
  });

  it("maps an invalid error body without exposing it", async () => {
    const protocolError = vi.fn();
    const callApi = createCallApi({
      defaultTimeoutMs: 1_000,
      maxResponseBytes: 1_000_000,
      onProtocolError: protocolError,
      transport: {
        request: vi.fn().mockResolvedValue({ status: 502, data: { stack: "secret" }, headers: {} }),
      } as unknown as AxiosInstance,
    });

    const result = await callApi({ method: "GET", path: "/v1/work-items" });

    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.problem.code).toBe("invalid_upstream_response");
    expect(protocolError).toHaveBeenCalledOnce();
  });

  it("rejects cross-origin paths before transport", async () => {
    const callApi = createCallApi({
      defaultTimeoutMs: 1_000,
      maxResponseBytes: 1_000_000,
      transport: { request: vi.fn() } as unknown as AxiosInstance,
    });
    await expect(callApi({ method: "GET", path: "https://evil.example/data" })).rejects.toThrow(
      "same-origin",
    );
  });
});
