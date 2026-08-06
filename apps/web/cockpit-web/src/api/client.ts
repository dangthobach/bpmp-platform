import type { RuntimeConfig } from "../config/runtime";
import { z } from "zod";
import {
  addNodeResponseSchema,
  createOrganizationResponseSchema,
  emptyResponseSchema,
  organizationDetailSchema,
  organizationListSchema,
  type AddOrganizationNodeInput,
  type CreateOrganizationInput,
  type MoveOrganizationNodeInput,
} from "../features/organizations/types";
import {
  configurationDetailSchema,
  configurationPageSchema,
  configurationProfileSchema,
  configurationVersionDiffSchema,
  type CreateConfigurationInput,
  type DraftConfigurationInput,
} from "../features/configuration/types";
import type {
  AuditPage,
  CaseResponse,
  CommandReceipt,
  CompleteWorkItemInput,
  DelegateWorkItemInput,
  StartWorkflowInput,
  WorkItemPage,
  WorkItemResponse,
} from "./types";

export interface RequestIdentity {
  tenantId: string;
  accessToken: string;
}

const browserConfigurationSchema = z.object({
  config_version: z.string().min(1),
  policy_version: z.string().min(1),
  batch_chunk_size: z.number().int().positive(),
  batch_concurrency: z.number().int().positive(),
}).refine(
  (value) => value.batch_concurrency <= value.batch_chunk_size,
  "batch concurrency must not exceed chunk size",
);

export type BrowserConfiguration = z.infer<typeof browserConfigurationSchema>;

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly correlationId: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class BpmpApiClient {
  constructor(
    private readonly config: RuntimeConfig,
    private readonly getIdentity: () => RequestIdentity,
  ) {}

  async getBrowserConfiguration(): Promise<BrowserConfiguration> {
    const raw = await this.request<unknown>("/v1/runtime/browser-configuration");
    const parsed = browserConfigurationSchema.safeParse(raw);
    if (!parsed.success) {
      throw new ApiError("Invalid browser configuration", 502, "");
    }
    return parsed.data;
  }

  async listWorkItems(pageToken = "", pageSize = this.config.defaultPageSize): Promise<WorkItemPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (pageToken) query.set("page_token", pageToken);
    const response = await this.request<Partial<WorkItemPage>>(
      `/v1/work-items?${query.toString()}`,
    );
    return {
      // Protobuf omits an empty repeated field, which produces `{}` rather than
      // `{ work_items: [] }` on the current gateway. Keep the UI's list contract stable.
      work_items: Array.isArray(response.work_items) ? response.work_items : [],
      next_page_token:
        typeof response.next_page_token === "string" ? response.next_page_token : "",
    };
  }

  getWorkItem(workItemId: string): Promise<WorkItemResponse> {
    return this.request(`/v1/work-items/${encodeURIComponent(workItemId)}`);
  }

  completeWorkItem(input: CompleteWorkItemInput): Promise<CommandReceipt> {
    return this.request(`/v1/work-items/${encodeURIComponent(input.workItemId)}/complete`, {
        method: "POST",
        body: {
          decision: input.decision,
          expected_version: input.expectedVersion,
        },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      });
  }

  delegateWorkItem(input: DelegateWorkItemInput): Promise<CommandReceipt> {
    return this.request(`/v1/work-items/${encodeURIComponent(input.workItemId)}/delegate`, {
        method: "POST",
        body: {
          expected_version: input.expectedVersion,
          assignee_id: input.assigneeId ?? "",
          candidate_group: input.candidateGroup ?? "",
        },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      });
  }

  startWorkflow(input: StartWorkflowInput): Promise<CommandReceipt> {
    return this.request(
      `/v1/workflows/${encodeURIComponent(input.workflowType)}/instances`,
      {
        method: "POST",
        body: {
          instance_id: input.instanceId,
          workflow_version: input.workflowVersion,
          start_node_id: input.startNodeId,
        },
      },
    );
  }

  getCase(caseId: string): Promise<CaseResponse> {
    return this.request(`/v1/cases/${encodeURIComponent(caseId)}`);
  }

  listAuditRecords(
    filters: { workItemId?: string; caseId?: string },
    pageToken = "",
    pageSize = this.config.defaultPageSize,
  ): Promise<AuditPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (filters.workItemId) query.set("work_item_id", filters.workItemId);
    if (filters.caseId) query.set("case_id", filters.caseId);
    if (pageToken) query.set("page_token", pageToken);
    return this.request(`/v1/audit-records?${query.toString()}`);
  }

  listOrganizations(offset = 0, limit = this.config.defaultPageSize) {
    const query = new URLSearchParams({
      offset: String(offset),
      limit: String(Math.min(limit, this.config.maxPageSize)),
    });
    return this.organizationRequest(
      `/api/v1/organizations?${query.toString()}`,
      organizationListSchema,
    );
  }

  getOrganization(organizationId: string) {
    return this.organizationRequest(
      `/api/v1/organizations/${encodeURIComponent(organizationId)}`,
      organizationDetailSchema,
    );
  }

  createOrganization(input: CreateOrganizationInput) {
    return this.organizationRequest(
      "/api/v1/organizations",
      createOrganizationResponseSchema,
      { method: "POST", body: input },
    );
  }

  addOrganizationNode(input: AddOrganizationNodeInput) {
    return this.organizationRequest(
      `/api/v1/organizations/${encodeURIComponent(input.organizationId)}/nodes`,
      addNodeResponseSchema,
      {
        method: "POST",
        body: {
          parent_id: input.parentId,
          kind: input.kind,
          code: input.code,
          name: input.name,
          expected_version: input.expectedVersion,
        },
      },
    );
  }

  moveOrganizationNode(input: MoveOrganizationNodeInput) {
    return this.organizationRequest(
      `/api/v1/organizations/${encodeURIComponent(input.organizationId)}/nodes/${encodeURIComponent(input.nodeId)}/move`,
      emptyResponseSchema,
      {
        method: "POST",
        body: {
          new_parent_id: input.newParentId,
          expected_version: input.expectedVersion,
        },
      },
    );
  }

  listConfigurationProfiles(pageToken = "", pageSize = this.config.defaultPageSize) {
    const query = new URLSearchParams({
      page_size: String(Math.min(pageSize, this.config.maxPageSize)),
    });
    if (pageToken) query.set("page_token", pageToken);
    return this.configurationRequest(
      `/v1/configuration/profiles?${query.toString()}`,
      configurationPageSchema,
    );
  }

  getConfigurationProfile(profileId: string) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}`,
      configurationDetailSchema,
    );
  }

  createConfigurationProfile(
    input: CreateConfigurationInput,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      "/v1/configuration/profiles",
      configurationProfileSchema,
      { method: "POST", body: input, idempotencyKey },
    );
  }

  addConfigurationDraft(
    profileId: string,
    input: DraftConfigurationInput,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions`,
      configurationProfileSchema,
      { method: "POST", body: input, idempotencyKey },
    );
  }

  publishConfiguration(
    profileId: string,
    versionId: string,
    expectedVersion: number,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/publish`,
      configurationProfileSchema,
      {
        method: "POST",
        body: { expected_version: expectedVersion, reason },
        idempotencyKey,
      },
    );
  }

  rollbackConfiguration(
    profileId: string,
    versionId: string,
    expectedVersion: number,
    policyVersion: string,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/rollback`,
      configurationProfileSchema,
      {
        method: "POST",
        body: {
          expected_version: expectedVersion,
          policy_version: policyVersion,
          reason,
        },
        idempotencyKey,
      },
    );
  }

  restoreConfiguration(
    profileId: string,
    versionId: string,
    expectedVersion: number,
    policyVersion: string,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/restore`,
      configurationProfileSchema,
      {
        method: "POST",
        body: {
          expected_version: expectedVersion,
          policy_version: policyVersion,
          reason,
        },
        idempotencyKey,
      },
    );
  }

  retireConfiguration(
    profileId: string,
    expectedVersion: number,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/retire`,
      configurationProfileSchema,
      {
        method: "POST",
        body: { expected_version: expectedVersion, reason },
        idempotencyKey,
      },
    );
  }

  diffConfiguration(profileId: string, fromVersion: string, toVersion: string) {
    const query = new URLSearchParams({
      from_version: fromVersion,
      to_version: toVersion,
    });
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/diff?${query.toString()}`,
      configurationVersionDiffSchema,
    );
  }

  private async request<T>(
    path: string,
    options: {
      method?: "GET" | "POST";
      body?: unknown;
      idempotencyKey?: string;
    } = {},
  ): Promise<T> {
    const identity = this.getIdentity();
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      this.config.requestTimeoutMs,
    );
    const correlationId = crypto.randomUUID();
    const commandId = crypto.randomUUID();
    const method = options.method ?? "GET";
    const headers = new Headers({
      Accept: "application/json",
      Authorization: `Bearer ${identity.accessToken}`,
      "X-BPMP-Tenant-ID": identity.tenantId,
      "X-Correlation-ID": correlationId,
    });
    if (method !== "GET") {
      headers.set("Content-Type", "application/json");
      headers.set("X-Command-ID", commandId);
      headers.set("Idempotency-Key", options.idempotencyKey ?? crypto.randomUUID());
    }
    try {
      const requestInit: RequestInit = {
        method,
        headers,
        signal: controller.signal,
      };
      if (options.body !== undefined) {
        requestInit.body = JSON.stringify(options.body);
      }
      const response = await fetch(new URL(path, this.config.apiBaseUrl), requestInit);
      const responseCorrelation =
        response.headers.get("X-Correlation-ID") ?? correlationId;
      const body = await response.json().catch(() => ({})) as Record<string, unknown>;
      if (!response.ok) {
        throw new ApiError(
          typeof body.error === "string" ? body.error : "Request failed",
          response.status,
          responseCorrelation,
        );
      }
      return body as T;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new ApiError("Request timed out", 408, correlationId);
      }
      throw new ApiError("API is unavailable", 0, correlationId);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  private async organizationRequest<T>(
    path: string,
    schema: z.ZodType<T>,
    options: {
      method?: "GET" | "POST";
      body?: unknown;
      idempotencyKey?: string;
    } = {},
  ): Promise<T> {
    const identity = this.getIdentity();
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      this.config.requestTimeoutMs,
    );
    const requestId = crypto.randomUUID();
    const method = options.method ?? "GET";
    const headers = new Headers({
      Accept: "application/json",
      Authorization: `Bearer ${identity.accessToken}`,
      "X-Request-ID": requestId,
      "X-Tenant-ID": identity.tenantId,
    });
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    try {
      const response = await fetch(
        new URL(path, this.config.organizationApiBaseUrl),
        {
          method,
          headers,
          signal: controller.signal,
          ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
        },
      );
      const raw: unknown = await response.json().catch(() => null);
      const envelope = z.object({
        data: z.unknown().nullable(),
        error_code: z.string(),
        message: z.string(),
        request_id: z.string(),
        timestamp: z.number(),
      }).safeParse(raw);
      const correlationId = response.headers.get("X-Request-ID") ?? requestId;
      if (!envelope.success) {
        throw new ApiError("Invalid organization API response", 502, correlationId);
      }
      if (
        !response.ok ||
        envelope.data.error_code !== "OK" ||
        envelope.data.data === null
      ) {
        throw new ApiError(
          envelope.data.message || "Organization request failed",
          response.status,
          envelope.data.request_id || correlationId,
        );
      }
      const parsed = schema.safeParse(envelope.data.data);
      if (!parsed.success) {
        throw new ApiError("Invalid organization response data", 502, correlationId);
      }
      return parsed.data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new ApiError("Request timed out", 408, requestId);
      }
      throw new ApiError("Organization API is unavailable", 0, requestId);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  private async configurationRequest<T>(
    path: string,
    schema: z.ZodType<T>,
    options: {
      method?: "GET" | "POST";
      body?: unknown;
      idempotencyKey?: string;
    } = {},
  ): Promise<T> {
    const identity = this.getIdentity();
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      this.config.requestTimeoutMs,
    );
    const correlationId = crypto.randomUUID();
    const headers = new Headers({
      Accept: "application/json",
      Authorization: `Bearer ${identity.accessToken}`,
      "X-BPMP-Tenant-ID": identity.tenantId,
      "X-Correlation-ID": correlationId,
    });
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
      headers.set("X-Command-ID", crypto.randomUUID());
      headers.set("Idempotency-Key", options.idempotencyKey ?? crypto.randomUUID());
    }
    try {
      const response = await fetch(
        new URL(path, this.config.apiBaseUrl),
        {
          method: options.method ?? "GET",
          headers,
          signal: controller.signal,
          ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
        },
      );
      const raw: unknown = await response.json().catch(() => null);
      const responseCorrelation =
        response.headers.get("X-Correlation-ID") ?? correlationId;
      if (!response.ok) {
        const error = z.object({ error: z.string() }).safeParse(raw);
        throw new ApiError(
          error.success ? error.data.error : "Configuration request failed",
          response.status,
          responseCorrelation,
        );
      }
      const parsed = schema.safeParse(raw);
      if (!parsed.success) {
        throw new ApiError("Invalid configuration response data", 502, responseCorrelation);
      }
      return parsed.data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new ApiError("Request timed out", 408, correlationId);
      }
      throw new ApiError("Configuration API is unavailable", 0, correlationId);
    } finally {
      window.clearTimeout(timeout);
    }
  }
}
