import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { SWRConfig } from "swr";
import { describe, expect, it, vi } from "vitest";
import { OdsThemeProvider } from "@ods/shared-ui";
import type { PlatformServices } from "@ods/mfe-contract";
import { route } from "./route";
import { diagnoseTemplateSources, applyDiagnosticQuickFix } from "./utils/handlebarsDiagnostics";
import { computeSmsMetrics, schemaJsonToVariables, variablesToSchemaJson } from "./types";

function createMockPlatform(): PlatformServices {
  return {
    callApi: vi.fn().mockImplementation(async ({ method, path, body }: { method: string; path: string; body?: Record<string, unknown> }) => {
      if (method === "POST" && path === "/v1/notifications/preview") {
        return {
          ok: true,
          data: {
            channel: "EMAIL",
            locale: "vi-VN",
            subject: "[BPMP] Ho so HS-2026-00918 da tiep nhan",
            html: "<main><h2>Safe Sandboxed Preview</h2><p>HS-2026-00918</p></main>",
            text: "BPMP: Ho so HS-2026-00918 da duoc tiep nhan.",
            sms_encoding: "GSM-7",
            sms_char_count: 42,
            sms_segments: 1,
            html_bytes: 64,
            rendered_with_sample_data: false,
            warnings: [],
          },
          requestId: "req-preview",
          correlationId: "corr-preview",
        };
      }
      if (method === "POST" && path === "/v1/notifications/templates/batch-actions") {
        const ids = Array.isArray(body?.ids) ? (body.ids as string[]) : [];
        return {
          ok: true,
          data: {
            results: ids.map((id) => ({ id, ok: true })),
          },
          requestId: "req-batch",
          correlationId: "corr-batch",
        };
      }
      if (method === "POST" && path.endsWith("/releases/rollback")) {
        return {
          ok: true,
          data: {
            release: {
              id: "rel-case-m1-ord4",
              template_id: "tmpl-case-submitted",
              schema_major: 1,
              template_version_id: "ver-case-v1",
              ordinal: 4,
              previous_ordinal: 3,
              status: "ACTIVE",
            },
          },
          requestId: "req-rollback",
          correlationId: "corr-rollback",
        };
      }
      if (method === "POST" && path.endsWith("/keys/rotate")) {
        return {
          ok: true,
          data: {
            api_key_secret: "ntf_live_one_time_secret_998877",
            key_prefix: "ntf_live_one_ti",
          },
          requestId: "req-rotate",
          correlationId: "corr-rotate",
        };
      }
      if (method === "POST" && path === "/v1/notifications/send" && body?.dry_run === true) {
        return {
          ok: true,
          data: {
            status: "PLANNED",
            plan: {
              template_version_id: "ver-case-v1",
              release_ordinal: 3,
              variant: { channel: "EMAIL", locale: "vi-VN", variant_kind: "INLINE" },
              schema_errors: [],
              quota: { remaining: 208720, limit: 250000 },
              routing: {
                candidates: [
                  { provider: "ses-smtp-primary", breaker_state: "CLOSED" },
                  { provider: "smtp-internal-backup", breaker_state: "CLOSED" },
                ],
                selected: "ses-smtp-primary",
              },
              warnings: [],
            },
          },
          requestId: "req-dry-run",
          correlationId: "corr-dry-run",
        };
      }
      if (method === "GET" && path.startsWith("/v1/notifications/deliveries?")) {
        return {
          ok: true,
          data: {
            deliveries: [
              {
                id: "dlv-page1-tail",
                request_id: "req-1",
                template_key: "case.submitted@1",
                template_version_id: "ver-case-v1",
                channel: "EMAIL",
                lane: "NORMAL",
                status: "FAILED",
                masked_address: "n***a@example.vn",
                selected_provider_id: "ses-smtp-primary",
                correlation_id: "corr-case-8812",
                attempt_count: 2,
                created_at: "2026-11-26T18:45:10Z",
              },
            ],
            next_before_created_at: "2026-11-26T18:45:10Z",
            next_before_id: "dlv-page1-tail",
          },
          requestId: "req-deliveries",
          correlationId: "corr-deliveries",
        };
      }
      return {
        ok: true,
        data: {},
        requestId: "req-default",
        correlationId: "corr-default",
      };
    }),
    navigate: vi.fn(),
    reportError: vi.fn(),
  };
}

function renderNotificationMfe(platform: PlatformServices) {
  const Component = route.Component;
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <OdsThemeProvider>
        <Component platform={platform} />
      </OdsThemeProvider>
    </SWRConfig>,
  );
}

describe("NotificationWorkspace Prototype Alignment & ADR-013 Verification", () => {
  it("renders Left Navigation Rail with 3 groups and 9 sections plus risk & breaker badges", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const nav = await screen.findByRole("navigation", { name: "Notification navigation" });
    expect(within(nav).getByText("Management")).toBeInTheDocument();
    expect(within(nav).getByText("Monitoring")).toBeInTheDocument();
    expect(within(nav).getByText("Operations")).toBeInTheDocument();

    const expectedSections = [
      "Templates",
      "Variable Mapping",
      "Applications",
      "Service Providers",
      "Delivery Monitoring",
      "Metrics Dashboard",
      "Audit Logs",
      "Circuit Breaker",
      "Send Notification",
    ];
    for (const label of expectedSections) {
      expect(within(nav).getByRole("button", { name: new RegExp(label, "i") })).toBeInTheDocument();
    }

    // Risk badge on Variable Mapping and non-CLOSED badge on Circuit Breaker
    expect(within(nav).getByLabelText(/Variable Mapping alerts:/i)).toBeInTheDocument();
    expect(within(nav).getByLabelText(/Circuit Breaker alerts:/i)).toBeInTheDocument();
  });

  it("renders HTML preview in strict <iframe sandbox='' srcdoc> without allow-scripts or allow-same-origin", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const iframe = await screen.findByTitle("Notification HTML Preview");
    expect(iframe).toHaveAttribute("sandbox", "");
    expect(iframe.getAttribute("sandbox")).not.toContain("allow-scripts");
    expect(iframe.getAttribute("sandbox")).not.toContain("allow-same-origin");
    expect(iframe.getAttribute("srcdoc")).toContain("Safe Sandboxed Preview");
  });

  it("detects Handlebars syntax diagnostics and applies quick fixes in the Templates editor", async () => {
    // Unit check for Handlebars linter & quick fix
    const report = diagnoseTemplateSources({
      subjectOrTitle: "Hello {{customerName",
      htmlBody: "<p>{{#if branchName}}Branch: {{branchName}}</p>",
      textBody: "",
    });
    expect(report.diagnostics.some((d) => d.code === "UNCLOSED_MUSTACHE")).toBe(true);
    expect(report.diagnostics.some((d) => d.code === "UNCLOSED_BLOCK")).toBe(true);

    const unclosedBlockDiag = report.diagnostics.find((d) => d.code === "UNCLOSED_BLOCK")!;
    const fixedHtml = applyDiagnosticQuickFix("<p>{{#if branchName}}Branch: {{branchName}}</p>", unclosedBlockDiag.quickFix!);
    expect(fixedHtml).toContain("{{/if}}");

    // UI integration check: inject unclosed block and click Quick Fix
    const platform = createMockPlatform();
    renderNotificationMfe(platform);
    const htmlField = await screen.findByLabelText(/HTML \/ Handlebars Body/i);
    fireEvent.change(htmlField, { target: { value: "<p>{{#if branchName}}Missing close</p>" } });

    expect(await screen.findByText("UNCLOSED_BLOCK")).toBeInTheDocument();
    const quickFixBtn = screen.getByRole("button", { name: /Quick Fix: Append \{\{\/if\}\}/i });
    fireEvent.click(quickFixBtn);

    await waitFor(() => {
      expect((htmlField as HTMLTextAreaElement).value).toContain("{{/if}}");
    });
  });

  it("submits batch actions payload to /v1/notifications/templates/batch-actions and displays per-item results", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const checkbox = await screen.findByLabelText("Select case.submitted");
    fireEvent.click(checkbox);

    fireEvent.click(screen.getByRole("button", { name: "Batch Set Status" }));

    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "POST",
          path: "/v1/notifications/templates/batch-actions",
          commandId: expect.stringMatching(/^ntf-batch-cmd-/),
          idempotencyKey: expect.stringMatching(/^ntf-batch-key-/),
          body: expect.objectContaining({
            action: "SET_STATUS",
            ids: ["tmpl-case-submitted"],
            status: "ARCHIVED",
          }),
        }),
      );
    });
    expect(await screen.findByText("tmpl-case-submitted: OK")).toBeInTheDocument();
  });

  it("performs rollback by creating a new release ordinal+1 without mutating immutable versions", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const rollbackBtn = await screen.findByRole("button", { name: "Rollback" });
    fireEvent.click(rollbackBtn);

    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "POST",
          path: "/v1/notifications/templates/tmpl-case-submitted/releases/rollback",
          body: expect.objectContaining({
            template_version_id: "ver-case-v1",
            schema_major: 1,
          }),
        }),
      );
    });

    expect(await screen.findByText(/Active @1 -> ver-case-v1 \(ordinal 4\)/i)).toBeInTheDocument();
    // Ensure PUT draft was NOT called during rollback
    const putCalls = (platform.callApi as ReturnType<typeof vi.fn>).mock.calls
      .map(([req]) => req)
      .filter((req) => req.method === "PUT");
    expect(putCalls).toHaveLength(0);
  });

  it("filters Variable Mapping risks and executes draft-only fix actions without implicit publish", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const nav = await screen.findByRole("navigation", { name: "Notification navigation" });
    fireEvent.click(within(nav).getByRole("button", { name: /Variable Mapping/i }));

    await screen.findByText(/Variable Mapping & Process Context Dependencies/i);
    fireEvent.click(screen.getByRole("button", { name: "Risks Only" }));

    expect(screen.getByText("MISSING_IN_CONTEXT")).toBeInTheDocument();
    expect(screen.getByText("vars.branchName")).toBeInTheDocument();

    // Execute draft-only trigger rule fix
    fireEvent.click(screen.getByRole("button", { name: "Draft: Add Exported Field" }));
    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "PUT",
          path: "/v1/notifications/trigger-rules/rule-case-intake/revisions",
        }),
      );
    });

    // Execute draft-only template variable declaration
    fireEvent.click(screen.getByRole("button", { name: "Draft: Declare in Template" }));
    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "PUT",
          path: "/v1/notifications/templates/tmpl-case-submitted/versions/draft",
        }),
      );
    });

    // Verify neither publish nor activate was called
    const publishCalls = (platform.callApi as ReturnType<typeof vi.fn>).mock.calls
      .map(([req]) => req)
      .filter((req) => typeof req.path === "string" && (req.path.includes("/publish") || req.path.includes("/activate")));
    expect(publishCalls).toHaveLength(0);
  });

  it("reveals rotated API key exactly once and never re-displays it after dismissal", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const nav = await screen.findByRole("navigation", { name: "Notification navigation" });
    fireEvent.click(within(nav).getByRole("button", { name: /Applications/i }));

    const rotateBtn = await screen.findByRole("button", { name: "Rotate API Key" });
    fireEvent.click(rotateBtn);

    expect(await screen.findByText("ntf_live_one_time_secret_998877")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss Key" }));

    await waitFor(() => {
      expect(screen.queryByText("ntf_live_one_time_secret_998877")).not.toBeInTheDocument();
    });
  });

  it("enforces mandatory reason + bounded TTL on breaker override and enables Simulate failure only for mock driver", async () => {
    const platform = createMockPlatform();
    renderNotificationMfe(platform);

    const nav = await screen.findByRole("navigation", { name: "Notification navigation" });
    fireEvent.click(within(nav).getByRole("button", { name: /Circuit Breaker/i }));

    await screen.findByText(/Manual Breaker Override/i);

    // Check Simulate failure buttons: only Zalo ZNS (mock) should be enabled; smtp/http/inapp disabled
    const simButtons = screen.getAllByRole("button", { name: "Simulate failure" });
    expect(simButtons.filter((btn) => !btn.hasAttribute("disabled"))).toHaveLength(1);
    expect(simButtons.filter((btn) => btn.hasAttribute("disabled"))).toHaveLength(3);

    // Override button disabled until reason and valid TTL are provided
    const overrideBtn = screen.getByRole("button", { name: "Apply Breaker Override" });
    expect(overrideBtn).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Mandatory Override Reason"), {
      target: { value: "Carrier maintenance window" },
    });
    fireEvent.change(screen.getByLabelText("Override TTL (ttl_ms)"), {
      target: { value: "0" },
    });
    expect(overrideBtn).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Override TTL (ttl_ms)"), {
      target: { value: "600000" },
    });
    expect(overrideBtn).toBeEnabled();
    fireEvent.click(overrideBtn);

    await waitFor(() => {
      expect(platform.callApi).toHaveBeenCalledWith(
        expect.objectContaining({
          method: "POST",
          path: "/v1/notifications/providers/prv-http-sms/circuit-breaker/override",
          body: {
            override_mode: "FORCE_OPEN",
            reason: "Carrier maintenance window",
            ttl_ms: 600000,
          },
        }),
      );
    });
  });

  it("renders Dry-Run plan and reuses Idempotency-Key + X-Command-ID on Send Notification retry", async () => {
    let sendAttempts = 0;
    const platform = createMockPlatform();
    const baseCallApi = platform.callApi;
    platform.callApi = vi.fn().mockImplementation(async (req) => {
      if (req.method === "POST" && req.path === "/v1/notifications/send" && !req.body?.dry_run) {
        sendAttempts += 1;
        if (sendAttempts === 1) {
          return {
            ok: false,
            problem: {
              type: "https://docs.bpmp.dev/problems/upstream_unavailable",
              title: "Notification API temporarily unavailable",
              status: 503,
              code: "upstream_unavailable",
              retryable: true,
              request_id: "req-send-fail",
              correlation_id: "corr-manual-001",
            },
            requestId: "req-send-fail",
            correlationId: "corr-manual-001",
          };
        }
        return {
          ok: true,
          data: { request_id: "req-accepted-9001", status: "ACCEPTED", replayed: false },
          requestId: "req-send-ok",
          correlationId: "corr-manual-001",
        };
      }
      return baseCallApi(req);
    });

    renderNotificationMfe(platform);
    const nav = await screen.findByRole("navigation", { name: "Notification navigation" });
    fireEvent.click(within(nav).getByRole("button", { name: /Send Notification/i }));

    // 1. Dry-Run plan rendering
    fireEvent.click(await screen.findByRole("button", { name: "Dry-Run Plan" }));
    expect(await screen.findByText(/Dry-Run Execution Plan/i)).toBeInTheDocument();
    expect(screen.getByText(/ses-smtp-primary \(CLOSED\)/i)).toBeInTheDocument();
    expect(screen.getByText(/208,720 \/ 250,000 daily/i)).toBeInTheDocument();

    // 2. Idempotent Send + Retry reusing exact commandId and idempotencyKey
    const sendButtons = screen.getAllByRole("button", { name: "Send Notification" });
    fireEvent.click(sendButtons[sendButtons.length - 1]!);

    expect(await screen.findByText("Notification API temporarily unavailable")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/Notification request ACCEPTED/i)).toBeInTheDocument();

    const actualSendCalls = (platform.callApi as ReturnType<typeof vi.fn>).mock.calls
      .map(([req]) => req)
      .filter((req) => req.method === "POST" && req.path === "/v1/notifications/send" && !req.body?.dry_run);

    expect(actualSendCalls).toHaveLength(2);
    expect(actualSendCalls[0]?.commandId).toMatch(/^ntf-send-cmd-/);
    expect(actualSendCalls[0]?.idempotencyKey).toMatch(/^ntf-send-key-/);
    expect(actualSendCalls[1]?.commandId).toBe(actualSendCalls[0]?.commandId);
    expect(actualSendCalls[1]?.idempotencyKey).toBe(actualSendCalls[0]?.idempotencyKey);
    expect(actualSendCalls[1]?.body).toEqual(actualSendCalls[0]?.body);
  });

  it("computes true GSM-7 vs UCS-2 SMS segments and maps variable table fields to JSON Schema", () => {
    expect(computeSmsMetrics("Hello world")).toEqual({ encoding: "GSM-7", charCount: 11, segments: 1 });
    expect(computeSmsMetrics("a".repeat(161))).toEqual({ encoding: "GSM-7", charCount: 161, segments: 2 });
    expect(computeSmsMetrics("Xin chào Việt Nam")).toEqual({ encoding: "UCS-2", charCount: 17, segments: 1 });
    expect(computeSmsMetrics("ệ".repeat(71))).toEqual({ encoding: "UCS-2", charCount: 71, segments: 2 });

    const schemaJson = variablesToSchemaJson([
      { key: "amount", label: "Loan Amount", sampleValue: "1000", required: true, type: "currency" },
      { key: "tenor", label: "Tenor Months", sampleValue: "12", required: false, type: "number" },
    ]);
    const parsed = JSON.parse(schemaJson) as { properties: Record<string, Record<string, unknown>> };
    expect(parsed.properties.amount?.title).toBe("Loan Amount");
    expect(parsed.properties.amount?.example).toBe("1000");
    expect(parsed.properties.amount?.format).toBe("money");
    expect(parsed.properties.tenor?.format).toBe("decimal");
    expect(schemaJsonToVariables(schemaJson)).toHaveLength(2);
  });
});
