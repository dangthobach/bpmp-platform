import { SpanStatusCode, type Span, type Tracer } from "@opentelemetry/api";
import { describe, expect, it, vi } from "vitest";
import { createApiTraceStarter } from "./index";

describe("createApiTraceStarter", () => {
  it("creates W3C trace headers and records only bounded HTTP outcome metadata", () => {
    const setAttribute = vi.fn();
    const setStatus = vi.fn();
    const end = vi.fn();
    const span = {
      spanContext: () => ({
        traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
        spanId: "00f067aa0ba902b7",
        traceFlags: 1,
      }),
      setAttribute,
      setStatus,
      end,
    } as unknown as Span;
    const tracer = { startSpan: vi.fn(() => span) } as unknown as Tracer;

    const traced = createApiTraceStarter(tracer)({ method: "POST" });
    traced?.end({ status: 503, error: true });

    expect(traced?.traceparent).toBe("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01");
    expect(setAttribute).toHaveBeenCalledWith("http.response.status_code", 503);
    expect(setStatus).toHaveBeenCalledWith({ code: SpanStatusCode.ERROR });
    expect(end).toHaveBeenCalledOnce();
  });
});
