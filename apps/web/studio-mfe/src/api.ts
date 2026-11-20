import type { HttpMethod, CallApi, ApiResult } from "@ods/call-api";

export async function send<T = unknown>(callApi: CallApi, method: HttpMethod, path: string, body?: unknown, key?: string): Promise<ApiResult<T>> {
  return callApi<T, unknown>({ method, path, ...(body === undefined ? {} : { body }), ...(key ? { idempotencyKey: key } : {}) });
}
export function commandKey(): string { return crypto.randomUUID().replaceAll("-", ""); }
export function rows(value: unknown, ...keys: string[]): Record<string, unknown>[] {
  if (Array.isArray(value)) return value.filter(isRecord);
  if (!isRecord(value)) return [];
  for (const key of keys) if (Array.isArray(value[key])) return (value[key] as unknown[]).filter(isRecord);
  return [];
}
export function isRecord(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value); }
export function recordId(value: Record<string, unknown>): string {
  for (const key of ["id", "profile_id", "version_id", "process_model_id", "deployment_id"]) if (typeof value[key] === "string") return value[key] as string;
  return "";
}
