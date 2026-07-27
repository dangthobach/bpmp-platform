// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeConfig } from "../config/runtime";
import { BpmpApiClient } from "./client";

const config: RuntimeConfig = {
  apiBaseUrl: "https://gateway.example.test/",
  organizationApiBaseUrl: "https://authz.example.test/",
  defaultPageSize: 50,
  maxPageSize: 200,
  batchChunkSize: 25,
  batchConcurrency: 4,
  requestTimeoutMs: 15_000,
  staleTimeMs: 5_000,
};

const identity = {
  tenantId: "d7b6e94b-317f-4a61-a223-ac78069380a1",
  accessToken: "signed-user-token",
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("BpmpApiClient organization API", () => {
  it("unwraps the authz envelope and preserves user and tenant identity", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        id: "c7cb5db9-5b55-4717-a6af-cdfb5e4216e1",
        code: "APAC",
        name: "Asia Pacific",
        root_path: "apac",
        node_count: 1,
        version: 0,
      }],
      error_code: "OK",
      message: "",
      request_id: "request-1",
      timestamp: 1_715_000_000,
    }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new BpmpApiClient(config, () => identity);
    const result = await client.listOrganizations(0, 50);

    expect(result[0]?.code).toBe("APAC");
    const [url, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(url.toString()).toBe(
      "https://authz.example.test/api/v1/organizations?offset=0&limit=50",
    );
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer signed-user-token");
    expect(headers.get("X-Tenant-ID")).toBe(identity.tenantId);
    expect(headers.get("X-Request-ID")).toBeTruthy();
  });

  it("rejects response data that does not match the published contract", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: [{ id: "not-a-uuid" }],
      error_code: "OK",
      message: "",
      request_id: "request-2",
      timestamp: 1_715_000_000,
    }), { status: 200 })));

    const client = new BpmpApiClient(config, () => identity);
    await expect(client.listOrganizations()).rejects.toMatchObject({
      message: "Invalid organization response data",
      status: 502,
    });
  });

  it("surfaces the control-plane request id on errors", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: null,
      error_code: "CONFLICT",
      message: "version mismatch",
      request_id: "control-plane-request",
      timestamp: 1_715_000_000,
    }), { status: 409 })));

    const client = new BpmpApiClient(config, () => identity);
    await expect(client.createOrganization({ code: "APAC", name: "Asia Pacific" }))
      .rejects.toMatchObject({
        message: "version mismatch",
        status: 409,
        correlationId: "control-plane-request",
      });
  });
});
