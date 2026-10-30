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

  it("injects a validated trace context and closes the span once", async () => {
    const end = vi.fn();
    const request = vi.fn().mockResolvedValue({ status: 204, data: undefined, headers: {} });
    const callApi = createCallApi({
      defaultTimeoutMs: 1_000,
      maxResponseBytes: 1_000_000,
      startTrace: () => ({
        traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        tracestate: "vendor=value",
        end,
      }),
      transport: { request } as unknown as AxiosInstance,
    });

    await callApi({ method: "GET", path: "/v1/work-items" });

    expect(request).toHaveBeenCalledWith(expect.objectContaining({
      headers: expect.objectContaining({
        traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        tracestate: "vendor=value",
      }),
    }));
    expect(end).toHaveBeenCalledOnce();
    expect(end).toHaveBeenCalledWith({ status: 204, error: false });
  });

  it("isolates telemetry failure without retrying the API request", async () => {
    const protocolError = vi.fn();
    const request = vi.fn().mockResolvedValue({ status: 200, data: {}, headers: {} });
    const callApi = createCallApi({
      defaultTimeoutMs: 1_000,
      maxResponseBytes: 1_000_000,
      startTrace: () => { throw new Error("collector unavailable"); },
      onProtocolError: protocolError,
      transport: { request } as unknown as AxiosInstance,
    });

    await callApi({ method: "POST", path: "/v1/work-items", idempotencyKey: "once" });

    expect(request).toHaveBeenCalledOnce();
    expect(protocolError).toHaveBeenCalledWith(expect.objectContaining({
      message: "telemetry adapter could not start API trace",
    }));
  });

  it("sends the stable command and idempotency identities required by the gateway", async () => {
    const request = vi.fn().mockResolvedValue({ status: 202, data: {}, headers: {} });
    const callApi = createCallApi({
      defaultTimeoutMs: 1_000,
      maxResponseBytes: 1_000_000,
      transport: { request } as unknown as AxiosInstance,
    });

    await callApi({
      method: "POST",
      path: "/v1/work-items/task-1/complete",
      commandId: "command-1",
      idempotencyKey: "intent-1",
      body: { expected_version: 1, decision: "APPROVED" },
    });

    expect(request).toHaveBeenCalledWith(expect.objectContaining({
      headers: expect.objectContaining({
        "X-Command-ID": "command-1",
        "Idempotency-Key": "intent-1",
      }),
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
