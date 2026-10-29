import {
  SpanKind,
  SpanStatusCode,
  context,
  trace,
  type Span,
  type Tracer,
} from "@opentelemetry/api";
import { ZoneContextManager } from "@opentelemetry/context-zone";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { BatchSpanProcessor, TracerProvider } from "@opentelemetry/sdk-trace";
import type { ApiTraceSpan, StartApiTrace } from "@ods/call-api";

export interface BrowserTelemetryConfiguration {
  serviceName: string;
  serviceVersion: string;
  otlpTraceEndpoint?: string;
}

let initialized = false;

export function initializeBrowserTelemetry(configuration: BrowserTelemetryConfiguration): void {
  if (initialized) return;
  const spanProcessors = configuration.otlpTraceEndpoint === undefined
    ? []
    : [new BatchSpanProcessor({
      exporter: new OTLPTraceExporter({ url: configuration.otlpTraceEndpoint }),
    })];
  const provider = new TracerProvider({
    resource: resourceFromAttributes({
      "service.name": configuration.serviceName,
      "service.version": configuration.serviceVersion,
    }),
    spanProcessors,
  });
  trace.setGlobalTracerProvider(provider);
  context.setGlobalContextManager(new ZoneContextManager().enable());
  initialized = true;
}

export function createApiTraceStarter(tracer: Tracer = trace.getTracer("@ods/call-api", "0.1.0")): StartApiTrace {
  return ({ method }) => {
    const span = tracer.startSpan(`HTTP ${method}`, {
      kind: SpanKind.CLIENT,
      attributes: { "http.request.method": method },
    });
    const spanContext = span.spanContext();
    if (!spanContext.traceId || !spanContext.spanId) {
      span.end();
      return undefined;
    }
    const flags = spanContext.traceFlags.toString(16).padStart(2, "0");
    const traceState = spanContext.traceState?.serialize();
    const value: ApiTraceSpan = {
      traceparent: `00-${spanContext.traceId}-${spanContext.spanId}-${flags}`,
      end: (outcome) => endSpan(span, outcome.status, outcome.error),
    };
    if (traceState) value.tracestate = traceState;
    return value;
  };
}

export const startApiTrace = createApiTraceStarter();

export function recordComponentError(contextValue: { remote: string; phase: string }): void {
  const span = trace.getTracer("@ods/telemetry", "0.1.0").startSpan("ods.component.error", {
    attributes: {
      "ods.remote.name": contextValue.remote,
      "ods.failure.phase": contextValue.phase,
    },
  });
  span.setStatus({ code: SpanStatusCode.ERROR });
  span.end();
}

function endSpan(span: Span, status: number, error: boolean): void {
  if (status > 0) span.setAttribute("http.response.status_code", status);
  if (error) span.setStatus({ code: SpanStatusCode.ERROR });
  span.end();
}
