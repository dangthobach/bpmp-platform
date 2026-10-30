import { expect, test } from "@playwright/test";

test("loads the independent Task remote and completes through its idempotent BFF contract", async ({ page }) => {
  let workItemVersion = 2;
  await page.route("**/v1/work-items?*", async (route) => {
    expect(route.request().headers()["traceparent"]).toMatch(
      /^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$/,
    );
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { "X-Request-ID": "e2e-request", "X-Correlation-ID": "e2e-correlation" },
      body: JSON.stringify({
        work_items: [{
          work_item_id: "task-e2e",
          task_name: "Federated review task",
          status: "ACTIVE",
          assignee_id: "officer-e2e",
          version: workItemVersion,
        }],
      }),
    });
  });
  await page.route("**/v1/work-items/task-e2e/complete", async (route) => {
    const request = route.request();
    expect(request.headers()["x-command-id"]).toMatch(/^task-command-/);
    expect(request.headers()["idempotency-key"]).toMatch(/^task-intent-/);
    expect(request.postDataJSON()).toEqual({ decision: "APPROVED", expected_version: 2 });
    workItemVersion = 3;
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      headers: { "X-Request-ID": "e2e-command", "X-Correlation-ID": "e2e-correlation" },
      body: JSON.stringify({ command_id: "command-e2e", work_item_version: 3 }),
    });
  });

  await page.goto("/");

  await expect(page.getByText("ODS Platform")).toBeVisible();
  await expect(page.getByRole("heading", { name: /Worklist/i })).toBeVisible();
  await expect(page.getByText("Federated review task")).toBeVisible();
  await page.getByRole("button", { name: "Complete" }).click();
  await page.getByRole("button", { name: "Submit Decision" }).click();
  await expect(page.getByText("Command accepted and reconciled")).toBeVisible();
  await expect(page.getByText(/command-e2e/)).toBeVisible();
});

test("a missing remote manifest does not take down the Shell", async ({ page }) => {
  await page.route("**/mfe-catalog.json", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        schemaVersion: "1",
        catalogVersion: "broken-remote",
        generatedAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
        remotes: [{
          name: "task-mfe",
          entry: "http://127.0.0.1:4301/missing/mf-manifest.json",
          exposedModule: "route",
          contractVersion: "1.0",
          minShellVersion: "1.0",
          requiredCapabilities: ["task.read"],
        }],
      }),
    });
  });

  await page.goto("/");

  await expect(page.getByText("ODS Platform")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Module unavailable" })).toBeVisible();
  await expect(page.getByText("Could not load task-mfe")).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();
});
