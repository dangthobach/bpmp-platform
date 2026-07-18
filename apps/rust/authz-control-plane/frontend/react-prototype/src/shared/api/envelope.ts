// Mirror of `authz_sdk::EnvelopeResponse` so frontend and backend share one shape.
// Keep in sync with `crates/authz-sdk/src/envelope.rs`.
import { z } from "zod";

export const ApiErrorSchema = z.object({
  code: z.string(),
  message: z.string(),
  details: z.unknown().nullish(),
});

export type ApiError = z.infer<typeof ApiErrorSchema>;

export const EnvelopeSchema = <T extends z.ZodTypeAny>(data: T) =>
  z.object({
    success: z.boolean(),
    data: data.nullish(),
    error: ApiErrorSchema.nullish(),
    request_id: z.string(),
    timestamp: z.string(),
  });

export type Envelope<T> = {
  success: boolean;
  data?: T | null;
  error?: ApiError | null;
  request_id: string;
  timestamp: string;
};

export class ApiException extends Error {
  constructor(
    public readonly status: number,
    public readonly error: ApiError,
    public readonly requestId: string,
  ) {
    super(`${error.code}: ${error.message}`);
    this.name = "ApiException";
  }
}
