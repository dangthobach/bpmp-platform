// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeConfig } from "../config/runtime";
import { BpmpApiClient } from "./client";
import type { ProcessExpressionInput, ProcessQueryInput } from "./types";

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

describe("BpmpApiClient decision authoring API", () => {
  it("uses the tenant-scoped decision draft routes and preserves mutation idempotency", async () => {
    const modelId = "c7cb5db9-5b55-4717-a6af-cdfb5e4216e1";
    const draftId = modelId;
    const model = { id:modelId, tenant_id:identity.tenantId, model_key:"decision.generic", name:"Generic decision", description:"", status:"DRAFT", revision:1, created_at_epoch_ms:100, updated_at_epoch_ms:100 };
    const draft = { decision_model_id:draftId, revision:1, dmn_xml:"<definitions/>", validation_status:"NOT_VALIDATED", diagnostics:[], updated_at_epoch_ms:100 };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({models:[model],next_page_token:""}),{status:200,headers:{"Content-Type":"application/json"}}))
      .mockResolvedValueOnce(new Response(JSON.stringify({model,draft}),{status:201,headers:{"Content-Type":"application/json"}}));
    vi.stubGlobal("fetch",fetchMock);
    const client = new BpmpApiClient(config,()=>identity);
    const page = await client.listDecisionModels("generic");
    expect(page.models[0]?.model_key).toBe("decision.generic");
    await client.createDecisionModel({model_key:"decision.generic",name:"Generic decision",initial_dmn_xml:"<definitions/>"},"decision-create-key");
    const calls = fetchMock.mock.calls;
    const listUrl = calls[0]?.[0];
    const request = calls[1]?.[1];
    expect(listUrl).toBeDefined();
    expect(request).toBeDefined();
    expect(String(listUrl)).toContain("/v1/authoring/decision-models?page_size=50&search=generic");
    if (!request) throw new Error("create request was not sent");
    expect(request.method).toBe("POST");
    expect(new Headers(request.headers).get("Idempotency-Key")).toBe("decision-create-key");
  });
});

describe("BpmpApiClient identity directory", () => {
  it("loads the Authz user directory using the configured tenant context", async () => {
    const userId = "c7cb5db9-5b55-4717-a6af-cdfb5e4216e1";
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      users: [{
        user_id: userId,
        username: "alice",
        is_active: true,
        query_revoked: false,
        attributes_version: 2,
        roles: ["branch-manager"],
        version: 3,
        created_at_epoch_ms: 1_715_000_000,
        updated_at_epoch_ms: 1_715_000_100,
      }],
      next_page_token: userId,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new BpmpApiClient(config, () => identity);
    const result = await client.listIdentityUsers(userId, 25);

    expect(result.users[0]?.roles).toEqual(["branch-manager"]);
    expect(result.next_page_token).toBe(userId);
    const [url, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(url.toString()).toBe(`https://gateway.example.test/v1/access/users?limit=25&after_user_id=${userId}`);
    expect(new Headers(init.headers).get("X-BPMP-Tenant-ID")).toBe(identity.tenantId);
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

  it("uses the authenticated claim and unclaim routes with versioned idempotent commands", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ command_id: "command-1" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);

    await client.claimWorkItem({ workItemId: "work-1", expectedVersion: 4, idempotencyKey: "claim-work-1-v4" });
    await client.unclaimWorkItem({ workItemId: "work-2", expectedVersion: 7, candidateGroup: "approvers", idempotencyKey: "unclaim-work-2-v7" });

    const [claimUrl, claimInit] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(claimUrl.pathname).toBe("/v1/work-items/work-1/claim");
    expect(JSON.parse(String(claimInit.body))).toEqual({ expected_version: 4 });
    expect(new Headers(claimInit.headers).get("Idempotency-Key")).toBe("claim-work-1-v4");
    const [unclaimUrl, unclaimInit] = fetchMock.mock.calls[1] as [URL, RequestInit];
    expect(unclaimUrl.pathname).toBe("/v1/work-items/work-2/unclaim");
    expect(JSON.parse(String(unclaimInit.body))).toEqual({ expected_version: 7, candidate_group: "approvers" });
    expect(new Headers(unclaimInit.headers).get("Idempotency-Key")).toBe("unclaim-work-2-v7");
  });
});

describe("BpmpApiClient assignment policy API", () => {
  it("lists policies and sends versioned idempotent upserts", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ policies: [], next_page_token: "next" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ policy: { policy_ref: "review-default", workflow_type: "purchase", workflow_version: "v1", node_id: "review", assignee_id: "", candidate_group: "finance", sla_duration_ms: 0, escalation_policy_ref: "", config_version: "cfg-1", version: 1 }, duplicate: false }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);

    await expect(client.listAssignmentPolicies("page-token", 25)).resolves.toEqual({ policies: [], next_page_token: "next" });
    await client.upsertAssignmentPolicy({ policy_ref: "review-default", workflow_type: "purchase", workflow_version: "v1", node_id: "review", assignee_id: "", candidate_group: "finance", sla_duration_ms: 0, escalation_policy_ref: "", config_version: "cfg-1", expected_version: 0 });

    const [listUrl] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(listUrl.searchParams.get("page_token")).toBe("page-token");
    const [writeUrl, writeInit] = fetchMock.mock.calls[1] as [URL, RequestInit];
    expect(writeUrl.pathname).toBe("/v1/assignment-policies");
    expect(writeInit.method).toBe("PUT");
    expect(JSON.parse(String(writeInit.body))).toMatchObject({ expected_version: 0, candidate_group: "finance" });
    expect(new Headers(writeInit.headers).get("Idempotency-Key")).toBeTruthy();
  });
});

describe("BpmpApiClient process export API", () => {
  it("creates, polls, cancels, and decodes protobuf JSON base64 CSV bytes", async () => {
    const exportResponse = (status: string, csv?: string) => new Response(JSON.stringify({ export: {
      id: "export-1", status, row_count: 1, ...(csv ? { csv } : {}),
      created_at_epoch_ms: 1_715_000_000, expires_at_epoch_ms: 1_715_086_400_000,
    } }), { status: 200 });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(exportResponse("QUERY_EXPORT_STATUS_PENDING"))
      .mockResolvedValueOnce(exportResponse("QUERY_EXPORT_STATUS_COMPLETED", "aWQsbmFtZQ0K"))
      .mockResolvedValueOnce(exportResponse("QUERY_EXPORT_STATUS_CANCELLED"));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);

    await expect(client.createQueryExport({ scope: "QUERY_SCOPE_COMPLETED", select: ["PROCESS_FIELD_INSTANCE_ID"] }, 1000)).resolves.toMatchObject({ id: "export-1", status: "QUERY_EXPORT_STATUS_PENDING" });
    await expect(client.getQueryExport("export-1")).resolves.toMatchObject({ id: "export-1", csv: "id,name\r\n" });
    await expect(client.cancelQueryExport("export-1")).resolves.toMatchObject({ status: "QUERY_EXPORT_STATUS_CANCELLED" });
    const [createUrl, createInit] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(createUrl.pathname).toBe("/v1/query-exports");
    expect(createInit.method).toBe("POST");
    expect(JSON.parse(String(createInit.body))).toMatchObject({ max_rows: 1000, query: { scope: "QUERY_SCOPE_COMPLETED", select: ["PROCESS_FIELD_INSTANCE_ID"] } });
    expect((fetchMock.mock.calls[1] as [URL])[0].pathname).toBe("/v1/query-exports/export-1");
    expect((fetchMock.mock.calls[2] as [URL, RequestInit])[1].method).toBe("DELETE");
  });
});

describe("BpmpApiClient completed process query", () => {
  it("posts the typed tree and normalizes an omitted protobuf result list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);
    const filter: ProcessExpressionInput = {
      event_relation: {
        quantifier: "RELATION_QUANTIFIER_EXISTS",
        expression: {
          predicate: {
            field: "EVENT_FIELD_EVENT_TYPE",
            operator: "COMPARISON_OPERATOR_EQUAL",
            values: [{ string_value: "workflow_completed" }],
          },
        },
      },
    };

    await expect(client.queryCompletedProcesses({ filter, page_size: 25 })).resolves.toEqual({
      processes: [],
      next_page_token: "",
    });
    const [url, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(url.pathname).toBe("/v1/process-instances/completed/query");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ filter, page_size: 25 });
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer signed-user-token");
    expect(headers.get("X-BPMP-Tenant-ID")).toBe(identity.tenantId);
  });
});

describe("BpmpApiClient saved process queries", () => {
  it("lists, saves, and deletes an actor-owned query through Projection APIs", async () => {
    const savedQuery: ProcessQueryInput = { scope: "QUERY_SCOPE_COMPLETED", select: ["PROCESS_FIELD_INSTANCE_ID"] };
    const saved = { id: "saved-query-1", name: "Recent completions", version: 1, query: savedQuery, created_at_epoch_ms: 1_715_000_000, updated_at_epoch_ms: 1_715_000_000 };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ saved_queries: [saved], next_page_token: "next" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ saved_query: saved }), { status: 200 }))
      .mockResolvedValueOnce(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);

    await expect(client.listSavedQueries("page-token", 20)).resolves.toMatchObject({ saved_queries: [{ id: "saved-query-1" }], next_page_token: "next" });
    await client.saveQuery({ id: "saved-query-1", name: "Recent completions", query: saved.query });
    await client.deleteSavedQuery("saved-query-1", 1);

    const [listUrl] = fetchMock.mock.calls[0] as [URL];
    expect(listUrl.searchParams.get("page_token")).toBe("page-token");
    const [saveUrl, saveInit] = fetchMock.mock.calls[1] as [URL, RequestInit];
    expect(saveUrl.pathname).toBe("/v1/saved-queries");
    expect(JSON.parse(String(saveInit.body))).toMatchObject({ id: "saved-query-1", name: "Recent completions" });
    const [deleteUrl, deleteInit] = fetchMock.mock.calls[2] as [URL, RequestInit];
    expect(deleteUrl.pathname).toBe("/v1/saved-queries/saved-query-1");
    expect(deleteUrl.searchParams.get("expected_version")).toBe("1");
    expect(deleteInit.method).toBe("DELETE");
  });
});

describe("BpmpApiClient prototype module support APIs", () => {
  it("loads live role and assignment-group catalogs", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify([{ code: "reviewer", name: "Reviewer" }]), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ groups: [{ group_id: "approvers", policy_count: 2 }] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);

    await expect(client.listAuthorizationRoles()).resolves.toEqual([{ code: "reviewer", name: "Reviewer" }]);
    await expect(client.listAccessQueues()).resolves.toEqual([{ group_id: "approvers", policy_count: 2 }]);
    expect((fetchMock.mock.calls[0]?.[0] as URL).pathname).toBe("/v1/access/catalog/roles");
    expect((fetchMock.mock.calls[1]?.[0] as URL).pathname).toBe("/v1/access/queues");
  });

  it("posts a bounded aggregate query for the live Optimize dashboard", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      rows: [{
        dimensions: [{ name: "PROCESS_FIELD_WORKFLOW_TYPE", value: { string_value: "approval" } }],
        metrics: [{ name: "instances", value: { integer_value: 9 } }],
      }],
      policy_version: "policy-4",
    }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new BpmpApiClient(config, () => identity);
    const request = {
      scope: "QUERY_SCOPE_ACTIVE" as const,
      group_by: ["PROCESS_FIELD_WORKFLOW_TYPE" as const],
      metrics: [{ alias: "instances", operation: "AGGREGATE_OPERATION_COUNT" as const }],
      limit: 100,
    };

    await expect(client.aggregateProcesses(request)).resolves.toMatchObject({
      rows: [{ dimensions: [{ value: { string_value: "approval" } }], metrics: [{ value: { integer_value: 9 } }] }],
      policy_version: "policy-4",
    });
    const [url, init] = fetchMock.mock.calls[0] as [URL, RequestInit];
    expect(url.pathname).toBe("/v1/process-instances/aggregate");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual(request);
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
