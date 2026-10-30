import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SWRConfig } from "swr";
import { describe, expect, it, vi } from "vitest";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";

const sampleWorkItems = [
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
      if (method === "GET" && path.startsWith("/v1/work-items")) {
        return {
          ok: true,
          data: { work_items: workItems },
          requestId: "req-1",
          correlationId: "corr-1",
        };
      }
      if (method === "GET" && path.startsWith("/v1/audit-records")) {
        return {
          ok: true,
          data: {
            records: [
              {
                audit_id: "audit-1",
                actor_id: "system",
                action: "WORK_ITEM_ACTIVATED",
                occurred_at_epoch_ms: Date.now() - 100000,
                from_version: 0,
                to_version: 1,
              },
            ],
          },
          requestId: "req-audit",
          correlationId: "corr-audit",
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
      if (method === "GET" && path === "/v1/work-items?page_size=50") {
        return {
          ok: true,
          data: { work_items: [sampleWorkItems[0]], next_page_token: "page-2" },
          requestId: "req-page-1",
          correlationId: "corr-page",
        };
      }
      if (method === "GET" && path === "/v1/work-items?page_size=50&page_token=page-2") {
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

  it("shows audit history in task detail modal", async () => {
    const platform = createMockPlatform();
    renderWorklist(platform);

    await waitFor(() => expect(screen.getByText("Personal Review Application")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Personal Review Application"));

    await waitFor(() => {
      expect(screen.getByText("Audit History")).toBeInTheDocument();
      expect(screen.getByText(/WORK_ITEM_ACTIVATED by system/)).toBeInTheDocument();
    });
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
