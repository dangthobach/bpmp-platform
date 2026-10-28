import { expect, test } from "@playwright/test";

test("loads the independently served Task remote and keeps API errors scoped", async ({ page }) => {
  await page.route("**/v1/work-items?*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { "X-Request-ID": "e2e-request", "X-Correlation-ID": "e2e-correlation" },
      body: JSON.stringify({
        work_items: [{ work_item_id: "task-e2e", task_name: "Federated review task", status: "OPEN" }],
      }),
    });
  });

  await page.goto("/");

  await expect(page.getByText("ODS Platform")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Personal Worklist" })).toBeVisible();
  await expect(page.getByText("Federated review task")).toBeVisible();
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
