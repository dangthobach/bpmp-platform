// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeConfig } from "../config/runtime";
import { BpmpApiClient } from "./client";

const config: RuntimeConfig = {
  apiBaseUrl: "https://gateway.example.test/",
  organizationApiBaseUrl: "https://authz.example.test/",
  realtimeBaseUrl: "https://cockpit.example.test/",
  realtimePath: "/realtime/v1/events",
  realtimeSignalNames: [
    "workflow.changed",
    "work-item.changed",
    "case.changed",
    "audit.changed",
  ],
  realtimeReconnectInitialMs: 500,
  realtimeReconnectMaxMs: 10_000,
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
    }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })));

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
    }), {
      status: 409,
      headers: { "Content-Type": "application/json" },
    })));

    const client = new BpmpApiClient(config, () => identity);
    await expect(client.createOrganization({ code: "APAC", name: "Asia Pacific" }))
      .rejects.toMatchObject({
        message: "version mismatch",
        status: 409,
        correlationId: "control-plane-request",
      });
  });

  it("identifies an SPA fallback instead of reporting invalid organization data", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<html></html>", {
      status: 200,
      headers: { "Content-Type": "text/html" },
    })));

    const client = new BpmpApiClient(config, () => identity);
    await expect(client.listOrganizations()).rejects.toMatchObject({
      message: "Organization API route is unavailable",
      status: 502,
    });
  });
});

describe("BpmpApiClient work items API", () => {
  it("normalizes an empty protobuf list response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status: 200 })));

    const client = new BpmpApiClient(config, () => identity);

    await expect(client.listWorkItems()).resolves.toEqual({
      work_items: [],
      next_page_token: "",
    });
  });
});

describe("BpmpApiClient configuration facade", () => {
  it("uses the public gateway and preserves a caller-owned idempotency key", async () => {
    const profile = {
      id: "c7cb5db9-5b55-4717-a6af-cdfb5e4216e1",
      tenant_id: identity.tenantId,
      owner: "ENGINE",
      name: "Runtime defaults",
      scope: { type: "TENANT", reference: identity.tenantId },
      aggregate_version: 2,
      current_published_version_id: "d7cb5db9-5b55-4717-a6af-cdfb5e4216e2",
      is_deleted: false,
      created_at: "2026-07-27T00:00:00Z",
      updated_at: "2026-07-27T00:00:01Z",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(profile), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "X-Correlation-ID": "configuration-correlation",
      },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new BpmpApiClient(config, () => identity);
    await client.publishConfiguration(
      profile.id,
      profile.current_published_version_id,
      1,
      "activate policy",
      "stable-idempotency-key",
    );

    const [url, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(url.origin).toBe("https://gateway.example.test");
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer signed-user-token");
    expect(headers.get("X-BPMP-Tenant-ID")).toBe(identity.tenantId);
    expect(headers.get("Idempotency-Key")).toBe("stable-idempotency-key");
    expect(headers.get("X-Command-ID")).toBeTruthy();
    expect(headers.get("X-Correlation-ID")).toBeTruthy();
  });

  it("identifies a non-JSON configuration upstream response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Bad Gateway", {
      status: 502,
      headers: { "Content-Type": "text/plain" },
    })));

    const client = new BpmpApiClient(config, () => identity);
    await expect(client.listConfigurationProfiles()).rejects.toMatchObject({
      message: "Configuration API route is unavailable",
      status: 502,
    });
  });
});

describe("BpmpApiClient browser configuration", () => {
  it("sends tenant identity and surfaces Problem Details", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      type: "https://docs.bpmp.dev/problems/unauthorized",
      title: "Unauthorized",
      status: 401,
      code: "unauthorized",
    }), {
      status: 401,
      headers: {
        "Content-Type": "application/problem+json",
        "X-Correlation-ID": "browser-config-correlation",
      },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new BpmpApiClient(config, () => identity);
    await expect(client.getBrowserConfiguration()).rejects.toMatchObject({
      message: "Unauthorized",
      status: 401,
      correlationId: "browser-config-correlation",
    });

    const [, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer signed-user-token");
    expect(headers.get("X-BPMP-Tenant-ID")).toBe(identity.tenantId);
    expect(headers.get("X-Correlation-ID")).toBeTruthy();
  });
});
