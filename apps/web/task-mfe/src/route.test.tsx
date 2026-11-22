import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SWRConfig } from "swr";
import { describe, expect, it, vi } from "vitest";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route, type WorkItem } from "./route";

const sampleWorkItems: WorkItem[] = [
  {
    work_item_id: "task-1",
    task_name: "Personal Review Application",
    workflow_type: "MortgageWorkflow",
    workflow_version: "1.0",
    node_id: "node-review",
    task_type: "USER_TASK",
    status: "ACTIVE",
    assignee_id: "officer-alice",
    version: 2,
    sla_deadline_epoch_ms: Date.now() + 3600 * 1000 * 5,
  },
  {
    work_item_id: "task-2",
    task_name: "Group Credit Verification",
    workflow_type: "CreditWorkflow",
    workflow_version: "2.1",
    node_id: "node-credit",
    task_type: "USER_TASK",
    status: "ACTIVE",
    candidate_group: "underwriters",
    version: 1,
    sla_deadline_epoch_ms: Date.now() - 3600 * 1000 * 2,
  },
];

function createMockPlatform(workItems = sampleWorkItems): PlatformServices {
  return {
    callApi: vi.fn().mockImplementation(async ({ method, path }: { method: string; path: string }) => {
      if (method === "GET" && path.startsWith("/v1/work-items?")) {
        return {
          ok: true,
          data: { work_items: workItems },
          requestId: "req-1",
          correlationId: "corr-1",
        };
      }
      if (method === "GET" && path.startsWith("/v1/work-items/") && path.includes("/history?")) {
        const secondPage = path.includes("page_token=history-next");
        return {
          ok: true,
          data: {
            entries: [{
              source: secondPage ? "HUMAN_RUNTIME" : "ENGINE",
              audit_id: secondPage ? "audit-2" : "",
              engine_sequence: secondPage ? 0 : 1,
              actor_id: secondPage ? "officer-alice" : "system",
              action: secondPage ? "DELEGATED" : "USER_TASK_ACTIVATED",
              occurred_at_epoch_ms: Date.now() - 100000,
            }],
            next_page_token: secondPage ? "" : "history-next",
            engine_watermark: 1,
          },
          requestId: "req-history",
          correlationId: "corr-history",
        };
      }
      if (method === "GET" && path.startsWith("/v1/work-items/")) {
        const taskId = path.split("/").at(-1);
        return {
          ok: true,
          data: { work_item: workItems.find((item) => item.work_item_id === taskId) ?? sampleWorkItems[0] },
          requestId: "req-detail",
          correlationId: "corr-detail",
        };
      }
      if (method === "GET" && path.startsWith("/v1/authoring/published-forms/")) {
        return {
          ok: true,
          data: {
            form: { form_key: "task-form", name: "Task form" },
            published_version: {
              id: "form-version-1",
              schema_json: JSON.stringify({ type: "object", properties: { applicant: { type: "string", title: "Applicant name", minLength: 1 } }, required: ["applicant"], additionalProperties: false }),
              ui_schema_json: "{}",
            },
          },
          requestId: "req-form",
          correlationId: "corr-form",
        };
      }
      if (method === "POST" && path.endsWith("/claim")) {
        return {
          ok: true,
          data: { command_id: "cmd-claim", work_item_version: 2 },
          requestId: "req-claim",
          correlationId: "corr-claim",
        };
      }
      if (method === "POST" && path.endsWith("/unclaim")) {
        return {
          ok: true,
          data: { command_id: "cmd-unclaim", work_item_version: 3 },
          requestId: "req-unclaim",
          correlationId: "corr-unclaim",
        };
      }
      if (method === "POST" && path.includes("/complete")) {
        return {
          ok: true,
          data: { command_id: "cmd-complete", work_item_version: 3 },
          requestId: "req-complete",
          correlationId: "corr-complete",
        };
      }
      if (method === "POST" && path.includes("/delegate")) {
        return {
          ok: true,
          data: { command_id: "cmd-delegate", work_item_version: 3 },
          requestId: "req-del",
          correlationId: "corr-del",
        };
      }
      return {
        ok: true,
        data: {},
        requestId: "req-def",
        correlationId: "corr-def",
      };
    }),
    navigate: vi.fn(),
    reportError: vi.fn(),
  };
}

function renderWorklist(platform: PlatformServices) {
  const Component = route.Component;
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <OdsThemeProvider>
        <Component platform={platform} />
      </OdsThemeProvider>
    </SWRConfig>
  );
}

describe("TaskWorklist Vertical Slice", () => {
  it("renders personal worklist by default with assigned tasks", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    expect(screen.getByRole("status")).toHaveTextContent("Loading work items...");
    await waitFor(() => {
      expect(screen.getByText("Personal Review Application")).toBeInTheDocument();
    });
    expect(screen.queryByText("Group Credit Verification")).not.toBeInTheDocument();
  });

  it("switches to group worklist tab and displays unassigned tasks", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    await waitFor(() => {
      expect(screen.getByText("Personal Review Application")).toBeInTheDocument();
    });

    const groupTab = screen.getByRole("tab", { name: /Group Tasks/i });
    fireEvent.click(groupTab);

    await waitFor(() => {
      expect(screen.getByText("Group Credit Verification")).toBeInTheDocument();
      expect(screen.queryByText("Personal Review Application")).not.toBeInTheDocument();
    });
    expect(platform.callApi).toHaveBeenCalledWith(expect.objectContaining({ method: "GET", path: "/v1/work-items?page_size=50&scope=group" }));
  });

  it("claims an unassigned group task with idempotent key", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    await waitFor(() => {
      expect(screen.getByText("Personal Review Application")).toBeInTheDocument();
    });

    const groupTab = screen.getByRole("tab", { name: /Group Tasks/i });
    fireEvent.click(groupTab);

    await waitFor(() => expect(screen.getByText("Group Credit Verification")).toBeInTheDocument());

    const claimButton = screen.getByRole("button", { name: "Claim" });
    fireEvent.click(claimButton);

    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "POST",
          path: "/v1/work-items/task-2/claim",
          body: { expected_version: 1 },
          commandId: expect.stringMatching(/^task-command-/),
          idempotencyKey: expect.stringMatching(/^task-intent-/),
        })
      );
    });
  });

  it("retries only unresolved bulk claims with the same operation and shows per-item receipts", async () => {
    const secondGroupTask = { ...sampleWorkItems[1], work_item_id: "task-3", task_name: "Second group task", version: 2 };
    const platform = createMockPlatform([...sampleWorkItems, secondGroupTask]);
    const baseCallApi = platform.callApi;
    let attempts = 0;
    platform.callApi = vi.fn().mockImplementation(async (request) => {
      if (request.method === "POST" && request.path === "/v1/work-items/bulk") {
        attempts += 1;
        return {
          ok: true,
          data: {
            operation_id: request.commandId,
            status: attempts === 1 ? "PARTIALLY_SUCCEEDED" : "SUCCEEDED",
            receipts: [
              { work_item_id: "task-2", status: "SUCCEEDED" },
              { work_item_id: "task-3", status: attempts === 1 ? "FAILED" : "SUCCEEDED", error_code: attempts === 1 ? "DEPENDENCY_TIMEOUT" : "", retryable: attempts === 1 },
            ],
          },
          requestId: `bulk-${attempts}`,
          correlationId: "bulk-correlation",
        };
      }
      return baseCallApi(request);
    });
    renderWorklist(platform);

    await screen.findByText("Personal Review Application");
    fireEvent.click(screen.getByRole("tab", { name: /Group Tasks/i }));
    await screen.findByText("Second group task");
    for (const button of screen.getAllByRole("button", { name: "Select" })) fireEvent.click(button);
    const submit = screen.getByRole("button", { name: "Claim selected" });
    fireEvent.click(submit);
    fireEvent.click(submit);

    await waitFor(() => expect(screen.getByText("Bulk claim: PARTIALLY_SUCCEEDED")).toBeInTheDocument());
    expect(attempts).toBe(1);
    expect(screen.getByText("task-3: FAILED (DEPENDENCY_TIMEOUT)")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry unresolved items" }));
    await waitFor(() => expect(screen.getByText("Bulk claim: SUCCEEDED")).toBeInTheDocument());

    const calls = (platform.callApi as ReturnType<typeof vi.fn>).mock.calls.map(([request]) => request)
      .filter((request) => request.method === "POST" && request.path === "/v1/work-items/bulk");
    expect(calls).toHaveLength(2);
    expect(calls[0]).toEqual(expect.objectContaining({
      body: { mode: "PARTIAL", action: "CLAIM", items: [
        { work_item_id: "task-2", expected_version: 1 },
        { work_item_id: "task-3", expected_version: 2 },
      ] },
    }));
    expect(calls[1].commandId).toBe(calls[0].commandId);
    expect(calls[1].idempotencyKey).toBe(calls[0].idempotencyKey);
    expect(calls[1].body).toEqual(calls[0].body);
  });

  it("completes a personal task with decision selection", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());

    const completeButton = screen.getByRole("button", { name: "Complete" });
    fireEvent.click(completeButton);

    await waitFor(() => expect(screen.getByRole("heading", { name: "Complete Task" })).toBeInTheDocument());

    const submitButton = screen.getByRole("button", { name: "Submit Decision" });
    fireEvent.click(submitButton);

    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "POST",
          path: "/v1/work-items/task-1/complete",
          body: { decision: "APPROVED", expected_version: 2 },
          commandId: expect.stringMatching(/^task-command-/),
          idempotencyKey: expect.stringMatching(/^task-intent-/),
        })
      );
    });
  });

  it("loads the current detail and published form, then submits typed outputs with its immutable version", async () => {
    const task = { ...sampleWorkItems[0], form_key: "task-form" };
    const platform = createMockPlatform([task]);
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Complete" }));
    const applicantField = await screen.findByLabelText(/Applicant name/);
    expect(screen.getByRole("button", { name: "Submit Decision" })).toBeDisabled();
    fireEvent.change(applicantField, { target: { value: "Nguyen Van A" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit Decision" }));

    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(expect.objectContaining({
        method: "POST",
        path: "/v1/work-items/task-1/complete",
        body: {
          decision: "APPROVED",
          expected_version: 2,
          form_version_id: "form-version-1",
          outputs: { applicant: "Nguyen Van A" },
        },
      }));
    });
  });

  it("reuses the same idempotency key and payload when a command is retried", async () => {
    let completeAttempts = 0;
    const platform = createMockPlatform();
    const baseCallApi = platform.callApi;
    platform.callApi = vi.fn().mockImplementation(async (request) => {
      if (request.method === "POST" && request.path.endsWith("/complete")) {
        completeAttempts += 1;
        if (completeAttempts === 1) {
          return {
            ok: false,
            problem: {
              type: "https://docs.bpmp.dev/problems/upstream_unavailable",
              title: "Upstream unavailable",
              status: 503,
              code: "upstream_unavailable",
              retryable: true,
              request_id: "req-failed",
              correlation_id: "corr-failed",
            },
            requestId: "req-failed",
            correlationId: "corr-failed",
          };
        }
      }
      return baseCallApi(request);
    });
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Complete" }));
    fireEvent.click(await screen.findByRole("button", { name: "Submit Decision" }));

    await waitFor(() => expect(screen.getByText("Upstream unavailable")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() => expect(completeAttempts).toBe(2));
    const completeCalls = (platform.callApi as ReturnType<typeof vi.fn>).mock.calls
      .map(([request]) => request)
      .filter((request) => request.method === "POST" && request.path.endsWith("/complete"));
    expect(completeCalls).toHaveLength(2);
    expect(completeCalls[1].commandId).toBe(completeCalls[0].commandId);
    expect(completeCalls[1].idempotencyKey).toBe(completeCalls[0].idempotencyKey);
    expect(completeCalls[1].body).toEqual(completeCalls[0].body);
  });

  it("does not retry an optimistic version conflict and reconciles the worklist", async () => {
    const platform = createMockPlatform();
    const baseCallApi = platform.callApi;
    platform.callApi = vi.fn().mockImplementation(async (request) => {
      if (request.method === "POST" && request.path.endsWith("/claim")) {
        return {
          ok: false,
          problem: {
            type: "https://docs.bpmp.dev/problems/version_conflict",
            title: "Task version conflict",
            status: 409,
            code: "version_conflict",
            retryable: false,
            request_id: "req-conflict",
            correlation_id: "corr-conflict",
          },
          requestId: "req-conflict",
          correlationId: "corr-conflict",
        };
      }
      return baseCallApi(request);
    });
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("tab", { name: /Group Tasks/i }));
    fireEvent.click(await screen.findByRole("button", { name: "Claim" }));

    await waitFor(() => expect(screen.getByText("Task version conflict")).toBeInTheDocument());
    expect(screen.getByText(/changed before this command was committed/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
    await waitFor(() => {
      const listCalls = (platform.callApi as ReturnType<typeof vi.fn>).mock.calls
        .map(([request]) => request)
        .filter((request) => request.method === "GET" && request.path.startsWith("/v1/work-items"));
      expect(listCalls.length).toBeGreaterThanOrEqual(2);
    });
  });

  it("filters tasks by search input", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());

    const searchInput = screen.getByPlaceholderText("Search current page...");
    fireEvent.change(searchInput, { target: { value: "NonExistent" } });

    await waitFor(() => {
      expect(screen.getByText("No assigned personal tasks")).toBeInTheDocument();
    });
  });

  it("uses bounded server pagination instead of materializing the whole worklist", async () => {
    const platform = createMockPlatform();
    platform.callApi = vi.fn().mockImplementation(async ({ method, path }) => {
      if (method === "GET" && path === "/v1/work-items?page_size=50&scope=mine") {
        return {
          ok: true,
          data: { work_items: [sampleWorkItems[0]], next_page_token: "page-2" },
          requestId: "req-page-1",
          correlationId: "corr-page",
        };
      }
      if (method === "GET" && path === "/v1/work-items?page_size=50&scope=mine&page_token=page-2") {
        return {
          ok: true,
          data: { work_items: [{ ...sampleWorkItems[0], work_item_id: "task-3", task_name: "Second Page Review" }] },
          requestId: "req-page-2",
          correlationId: "corr-page",
        };
      }
      throw new Error(`unexpected request: ${method} ${path}`);
    });
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(screen.getByText("Second Page Review")).toBeInTheDocument());
    expect(screen.queryByText("Personal Review Application")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());
  });

  it("shows Engine and Human Runtime history with cursor navigation", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Personal Review Application"));

    await waitFor(() => {
      expect(screen.getByText("Task History")).toBeInTheDocument();
      expect(screen.getByText(/USER_TASK_ACTIVATED by system/)).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "Next history page" }));
    await waitFor(() => expect(screen.getByText(/DELEGATED by officer-alice/)).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Previous history page" }));
    await waitFor(() => expect(screen.getByText(/USER_TASK_ACTIVATED by system/)).toBeInTheDocument());
  });

  it("shows scoped retry for retryable API failures", async () => {
    const callApi = vi.fn().mockResolvedValue({
      ok: false,
      problem: { title: "Upstream unavailable", code: "upstream_unavailable", retryable: true },
      requestId: "request-1",
      correlationId: "correlation-1",
    });
    renderWorklist({ callApi, navigate: vi.fn(), reportError: vi.fn() });

    await waitFor(() => expect(screen.getByText("Upstream unavailable")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
