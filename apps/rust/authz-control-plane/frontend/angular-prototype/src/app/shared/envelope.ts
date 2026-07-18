// Mirror of `authz_sdk::EnvelopeResponse`. Single shape parsed at the edge.
import { z } from 'zod';

export const ApiErrorSchema = z.object({
  code: z.string(),
  message: z.string(),
  details: z.unknown().nullish(),
});

export const EnvelopeSchema = <T extends z.ZodTypeAny>(data: T) =>
  z.object({
    success: z.boolean(),
    data: data.nullish(),
    error: ApiErrorSchema.nullish(),
    request_id: z.string(),
    timestamp: z.string(),
  });

export class ApiException extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly requestId: string,
  ) {
    super(`${code}: ${message}`);
    this.name = 'ApiException';
  }
}
