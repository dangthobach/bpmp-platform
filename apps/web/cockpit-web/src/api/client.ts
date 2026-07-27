import type { RuntimeConfig } from "../config/runtime";
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

  listWorkItems(pageToken = "", pageSize = this.config.defaultPageSize): Promise<WorkItemPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (pageToken) query.set("page_token", pageToken);
    return this.request(`/v1/work-items?${query.toString()}`);
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
}
