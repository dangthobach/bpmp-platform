// Thin fetch wrapper that:
// 1) Unwraps the EnvelopeResponse and throws ApiException on `success === false`.
// 2) Attaches the bearer token from the auth provider.
// 3) Propagates the request_id for tracing correlation.
import { z } from "zod";
import {
  ApiException,
  EnvelopeSchema,
  type Envelope,
} from "./envelope";

export type TokenProvider = () => string | null;

export interface HttpClientOptions {
  baseUrl: string;
  getToken: TokenProvider;
}

export class HttpClient {
  constructor(private readonly opts: HttpClientOptions) {}

  async request<T>(
    path: string,
    init: RequestInit,
    schema: z.ZodType<T>,
  ): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("accept", "application/json");
    if (init.body) headers.set("content-type", "application/json");
    const token = this.opts.getToken();
    if (token) headers.set("authorization", `Bearer ${token}`);

    const res = await fetch(`${this.opts.baseUrl}${path}`, {
      ...init,
      headers,
    });

    const raw = (await res.json()) as Envelope<unknown>;
    const parsed = EnvelopeSchema(schema).safeParse(raw);
    if (!parsed.success) {
      throw new ApiException(
        res.status,
        { code: "INVALID_RESPONSE", message: parsed.error.message },
        raw?.request_id ?? "",
      );
    }
    if (!parsed.data.success || !parsed.data.data) {
      throw new ApiException(
        res.status,
        parsed.data.error ?? { code: "UNKNOWN", message: "empty response" },
        parsed.data.request_id,
      );
    }
    return parsed.data.data as T;
  }

  get<T>(path: string, schema: z.ZodType<T>) {
    return this.request(path, { method: "GET" }, schema);
  }
  post<T>(path: string, body: unknown, schema: z.ZodType<T>) {
    return this.request(
      path,
      { method: "POST", body: JSON.stringify(body) },
      schema,
    );
  }
  patch<T>(path: string, body: unknown, schema: z.ZodType<T>) {
    return this.request(
      path,
      { method: "PATCH", body: JSON.stringify(body) },
      schema,
    );
  }
}
