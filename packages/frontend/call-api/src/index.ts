import axios, { AxiosError, type AxiosInstance, type AxiosRequestConfig } from "axios";

const validIdentifier = /^[A-Za-z0-9_.:-]{1,128}$/;

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface ProblemDetails {
  type: string;
  title: string;
  status: number;
  code: string;
  detail?: string;
  retryable: boolean;
  request_id: string;
  correlation_id: string;
  trace_id?: string;
}

export type ApiResult<T> =
  | { ok: true; data: T; requestId: string; correlationId: string }
  | { ok: false; problem: ProblemDetails; requestId: string; correlationId: string };

export interface CallApiOptions<TBody = unknown> {
  method: HttpMethod;
  path: string;
  body?: TBody;
  signal?: AbortSignal;
  idempotencyKey?: string;
  commandId?: string;
  correlationId?: string;
  timeoutMs?: number;
}

export type CallApi = <TResponse, TBody = unknown>(
  options: CallApiOptions<TBody>,
) => Promise<ApiResult<TResponse>>;

export interface CallApiConfiguration {
  baseURL?: string;
  defaultTimeoutMs: number;
  maxResponseBytes: number;
  csrfToken?: () => string | undefined;
  startTrace?: StartApiTrace;
  onProtocolError?: (error: Error) => void;
  transport?: AxiosInstance;
}

export interface ApiTraceOutcome {
  status: number;
  error: boolean;
}

export interface ApiTraceSpan {
  traceparent: string;
  tracestate?: string;
  end(outcome: ApiTraceOutcome): void;
}

export type StartApiTrace = (request: Readonly<{ method: HttpMethod }>) => ApiTraceSpan | undefined;

export function createCallApi(configuration: CallApiConfiguration): CallApi {
  const transport = configuration.transport ?? axios.create({
    baseURL: configuration.baseURL ?? "/",
    withCredentials: true,
    maxContentLength: configuration.maxResponseBytes,
    maxBodyLength: configuration.maxResponseBytes,
    validateStatus: () => true,
  });

  return async function callApi<TResponse, TBody = unknown>(
    options: CallApiOptions<TBody>,
  ): Promise<ApiResult<TResponse>> {
    assertSafePath(options.path);
    const requestId = createIdentifier();
    const correlationId = normalizeIdentifier(options.correlationId) ?? requestId;
    const headers: Record<string, string> = {
      Accept: "application/json, application/problem+json",
      "X-Request-ID": requestId,
      "X-Correlation-ID": correlationId,
    };
    const csrfToken = configuration.csrfToken?.();
    if (csrfToken) headers["X-CSRF-Token"] = csrfToken;
    if (options.idempotencyKey) {
      const idempotencyKey = normalizeIdentifier(options.idempotencyKey);
      if (!idempotencyKey) throw new TypeError("idempotency key must be a valid identifier");
      const commandId = options.commandId === undefined
        ? createIdentifier()
        : normalizeIdentifier(options.commandId);
      if (!commandId) throw new TypeError("command ID must be a valid identifier");
      headers["Idempotency-Key"] = idempotencyKey;
      headers["X-Command-ID"] = commandId;
    } else if (options.commandId !== undefined) {
      throw new TypeError("command ID requires an idempotency key");
    }

    const request: AxiosRequestConfig<TBody> = {
      method: options.method,
      url: options.path,
      headers,
      timeout: options.timeoutMs ?? configuration.defaultTimeoutMs,
    };
    if (options.signal !== undefined) request.signal = options.signal;
    if (options.body !== undefined) request.data = options.body;

    const traceSpan = safeStartTrace(configuration.startTrace, options.method, configuration.onProtocolError);
    if (traceSpan !== undefined) {
      headers.traceparent = traceSpan.traceparent;
      if (traceSpan.tracestate !== undefined) headers.tracestate = traceSpan.tracestate;
    }

    try {
      const response = await transport.request<TResponse>(request);
      safeEndTrace(traceSpan, { status: response.status, error: response.status >= 400 }, configuration.onProtocolError);
      const responseRequestId = headerIdentifier(response.headers["x-request-id"]) ?? requestId;
      const responseCorrelationId = headerIdentifier(response.headers["x-correlation-id"]) ?? correlationId;
      if (response.status >= 200 && response.status < 300) {
        return {
          ok: true,
          data: response.data,
          requestId: responseRequestId,
          correlationId: responseCorrelationId,
        };
      }
      return failureResult(
        response.status,
        response.data,
        responseRequestId,
        responseCorrelationId,
        configuration.onProtocolError,
      );
    } catch (error) {
      safeEndTrace(traceSpan, { status: 0, error: true }, configuration.onProtocolError);
      if (options.signal?.aborted) throw error;
      const axiosError = error as AxiosError;
      const code = axiosError.code === "ECONNABORTED" ? "request_timeout" : "network_unavailable";
      return {
        ok: false,
        problem: {
          type: `https://docs.bpmp.dev/problems/${code}`,
          title: code === "request_timeout" ? "Request timed out" : "Network unavailable",
          status: 0,
          code,
          retryable: true,
          request_id: requestId,
          correlation_id: correlationId,
        },
        requestId,
        correlationId,
      };
    }
  };
}

function safeStartTrace(
  startTrace: StartApiTrace | undefined,
  method: HttpMethod,
  onProtocolError: CallApiConfiguration["onProtocolError"],
): ApiTraceSpan | undefined {
  if (startTrace === undefined) return undefined;
  try {
    const span = startTrace({ method });
    if (span === undefined || !validTraceParent(span.traceparent)
      || (span.tracestate !== undefined && !validTraceState(span.tracestate))) {
      onProtocolError?.(new Error("telemetry adapter returned invalid W3C trace context"));
      return undefined;
    }
    return span;
  } catch {
    onProtocolError?.(new Error("telemetry adapter could not start API trace"));
    return undefined;
  }
}

function safeEndTrace(
  span: ApiTraceSpan | undefined,
  outcome: ApiTraceOutcome,
  onProtocolError: CallApiConfiguration["onProtocolError"],
): void {
  if (span === undefined) return;
  try {
    span.end(outcome);
  } catch {
    onProtocolError?.(new Error("telemetry adapter could not end API trace"));
  }
}

function validTraceParent(value: string): boolean {
  return /^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/.test(value)
    && !/^00-0{32}-/.test(value)
    && !/-0{16}-/.test(value);
}

function validTraceState(value: string): boolean {
  return value.length <= 512 && !/[\r\n]/.test(value);
}

function failureResult<T>(
  status: number,
  value: T,
  requestId: string,
  correlationId: string,
  onProtocolError: CallApiConfiguration["onProtocolError"],
): ApiResult<never> {
  if (isProblemDetails(value)) {
    return {
      ok: false,
      problem: {
        ...value,
        status,
        request_id: normalizeIdentifier(value.request_id) ?? requestId,
        correlation_id: normalizeIdentifier(value.correlation_id) ?? correlationId,
      },
      requestId,
      correlationId,
    };
  }
  const error = new Error(`API returned ${status} without valid Problem Details`);
  onProtocolError?.(error);
  return {
    ok: false,
    problem: {
      type: "https://docs.bpmp.dev/problems/invalid_upstream_response",
      title: "Invalid upstream response",
      status,
      code: "invalid_upstream_response",
      retryable: status >= 500,
      request_id: requestId,
      correlation_id: correlationId,
    },
    requestId,
    correlationId,
  };
}

function isProblemDetails(value: unknown): value is ProblemDetails {
  if (value === null || typeof value !== "object") return false;
  const candidate = value as Partial<ProblemDetails>;
  return typeof candidate.type === "string"
    && typeof candidate.title === "string"
    && typeof candidate.code === "string"
    && typeof candidate.retryable === "boolean"
    && typeof candidate.request_id === "string"
    && typeof candidate.correlation_id === "string";
}

function assertSafePath(path: string): void {
  if (!path.startsWith("/") || path.startsWith("//") || /^https?:/i.test(path)) {
    throw new TypeError("callApi path must be a same-origin absolute path");
  }
}

function normalizeIdentifier(value: string | undefined): string | undefined {
  return value && validIdentifier.test(value) ? value : undefined;
}

function headerIdentifier(value: unknown): string | undefined {
  return typeof value === "string" ? normalizeIdentifier(value) : undefined;
}

function createIdentifier(): string {
  return crypto.randomUUID().replaceAll("-", "");
}
