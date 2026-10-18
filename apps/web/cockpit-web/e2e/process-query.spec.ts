import { expect, test, type Page } from "@playwright/test";

async function mockCockpit(page: Page) {
  await page.addInitScript(() => {
    sessionStorage.setItem("bpmp.cockpit.session.v1", JSON.stringify({
      tenantId: "tenant-visual",
      accessToken: "visual-test-token",
    }));
  });
  await page.route("**/v1/runtime/browser-configuration", (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      config_version: "visual-config-v1",
      policy_version: "visual-policy-v1",
      batch_chunk_size: 25,
      batch_concurrency: 4,
    }),
  }));
  await page.route("**/realtime/v1/events**", (route) => route.fulfill({
    status: 200,
    contentType: "text/event-stream",
    body: "",
  }));
  await page.route("**/v1/process-instances/query-capabilities", (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({
      schema_version: "bpmp.projection.query.v1", config_version: "visual-config-v1", policy_version: "visual-policy-v1",
      fields: [], event_fields: [], default_page_size: 50, max_page_size: 100, max_filter_depth: 8,
      max_predicates: 20, max_sort_fields: 4, max_relation_joins: 4, max_in_values: 20, max_string_bytes: 512,
      scopes: ["QUERY_SCOPE_ALL", "QUERY_SCOPE_ACTIVE", "QUERY_SCOPE_COMPLETED", "QUERY_SCOPE_DELETED"], default_sort: [],
    }),
  }));
  await page.route("**/v1/process-instances/query", async (route) => {
    const request = route.request().postDataJSON() as Record<string, unknown>;
    expect(request).toHaveProperty("filter.group.expressions");
    expect(request).toHaveProperty("sort.1.text_transform", "TEXT_TRANSFORM_TRIM_LOWER");
    expect(request).toHaveProperty("scope", "QUERY_SCOPE_COMPLETED");
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        processes: [
          {
            instance: {
              instance_id: "approval-2026-001",
              workflow_type: "Approval",
              workflow_version: "7",
              status: "COMPLETED",
              last_event_sequence: 18,
              completed_at_epoch_ms: 1792202400000,
              initiator_actor_id: "ops-manager",
            },
            duration_ms: 83421,
            event_count: 18,
          },
          {
            instance: {
              instance_id: "approval-2026-002",
              workflow_type: "Approval",
              workflow_version: "7",
              status: "COMPLETED",
              last_event_sequence: 12,
              completed_at_epoch_ms: 1792198800000,
              initiator_actor_id: "branch-lead",
            },
            duration_ms: 12500,
            event_count: 12,
          },
        ],
        next_page_token: "signed-next-page",
      }),
    });
  });
}

test.beforeEach(async ({ page }) => {
  await mockCockpit(page);
  await page.goto("/processes");
  await expect(page.getByRole("heading", { name: "Process explorer" })).toBeVisible();
});

test("builds, collapses and executes a nested query", async ({ page }) => {
  const values = page.getByLabel("Predicate value");
  await values.nth(0).fill("approval");
  await page.getByLabel("Text transform").nth(0).selectOption("TEXT_TRANSFORM_TRIM_LOWER");
  await values.nth(1).fill("COMPLETED");

  await page.getByRole("button", { name: "Event join" }).click();
  await page.getByLabel("Predicate value").nth(2).fill("workflow_completed");
  await page.getByLabel("Predicate value").nth(3).fill("approval-node");

  await page.getByRole("button", { name: "Add sort" }).click();
  await page.getByLabel("Sort field").nth(1).selectOption("PROCESS_FIELD_WORKFLOW_TYPE");
  await page.getByLabel("Sort text transform").selectOption("TEXT_TRANSFORM_TRIM_LOWER");

  const collapse = page.getByRole("button", { name: "Collapse event join" });
  await collapse.click();
  await expect(page.getByRole("button", { name: "Expand event join" })).toBeVisible();
  await page.getByRole("button", { name: "Expand event join" }).click();

  await page.getByRole("button", { name: "Run query" }).click();
  await expect(page.getByText("approval-2026-001")).toBeVisible();
  await expect(page.getByRole("button", { name: "Next page" })).toBeEnabled();
  await page.locator(".query-tree").evaluate((element) => { element.scrollTop = 0; });
  await expect(page).toHaveScreenshot("process-query.png", { fullPage: true });
});
