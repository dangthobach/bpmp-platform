import { expect, test } from "@playwright/test";

test.skip(!process.env.CASE_LOCAL_E2E, "requires the loopback Case API and PostgreSQL");

test("creates and reads a Case through the local Shell", async ({ page }) => {
  const customerRef = `case-browser-${Date.now()}`;
  const failedResponses: string[] = [];
  page.on("response", (response) => {
    if (response.status() >= 400) failedResponses.push(`${response.status()} ${response.url()}`);
  });
  await page.goto("http://localhost:4300/");
  await expect(page.getByRole("heading", { name: "Customer Cases" })).toBeVisible();
  await page.getByRole("textbox", { name: "Customer reference", exact: true }).fill(customerRef);
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await expect(page.getByText(`Case 360 · ${customerRef}`)).toBeVisible();
  await expect(page.getByText(/Workflow start: PENDING/)).toBeVisible();
  await expect(page.getByText(/CaseCreated/)).toBeVisible();
  expect(failedResponses).toEqual([]);
});
