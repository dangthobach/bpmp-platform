import { expect, test } from "@playwright/test";

test("loads the independent Task remote and completes through its idempotent BFF contract", async ({ page }) => {
  let workItemVersion = 2;
  await page.route("**/v1/work-items?*", async (route) => {
    expect(new URL(route.request().url()).searchParams.get("scope")).toBe("mine");
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
  await page.route("**/v1/work-items/task-e2e", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { "X-Request-ID": "e2e-detail", "X-Correlation-ID": "e2e-correlation" },
      body: JSON.stringify({ work_item: {
        work_item_id: "task-e2e",
        task_name: "Federated review task",
        status: "ACTIVE",
        assignee_id: "officer-e2e",
        version: workItemVersion,
      } }),
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
  await expect(page.getByRole("heading", { name: /Worklist/i })).toBeVisible({ timeout: 15_000 });
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

test("runs Authoring draft through check-in, deployment detail and rollback", async ({ page }) => {
  let revision = 1;
  let validationStatus = "INVALID";
  let checkedIn = false;
  let deployed = false;
  await page.route("**/v1/work-items?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ work_items: [] }) }));
  await page.route("**/v1/authoring/process-models?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ models: revision > 1 ? [{ id: "process-e2e", model_key: "claims", name: "Claims" }] : [], next_page_token: "" }) }));
  await page.route("**/v1/authoring/process-models", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/);
    await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ model: { id: "process-e2e", model_key: "claims", name: "Claims" }, draft: { process_model_id: "process-e2e", revision, bpmn_xml: route.request().postDataJSON().initial_bpmn_xml, validation_status: validationStatus, diagnostics: [] } }) });
  });
  await page.route("**/v1/authoring/process-models/process-e2e", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ model: { id: "process-e2e", model_key: "claims", name: "Claims" }, draft: { process_model_id: "process-e2e", revision, bpmn_xml: "<definitions><process id='claims'/></definitions>", validation_status: validationStatus, diagnostics: [] } }) }));
  await page.route("**/v1/authoring/process-models/process-e2e/draft", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/);
    expect(route.request().postDataJSON().expected_revision).toBe(revision);
    revision += 1;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ draft: { process_model_id: "process-e2e", revision, bpmn_xml: route.request().postDataJSON().bpmn_xml, validation_status: validationStatus, diagnostics: [] } }) });
  });
  await page.route("**/v1/authoring/process-models/process-e2e/draft/validate", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/);
    validationStatus = "VALID";
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ process_model_id: "process-e2e", revision, valid: true, diagnostics: [] }) });
  });
  await page.route("**/v1/authoring/process-models/process-e2e/versions?*", async (route) => {
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ versions: checkedIn ? [{ id: "version-e2e", version_tag: "v1", version_num: 1 }] : [] }) });
  });
  await page.route("**/v1/authoring/process-models/process-e2e/versions", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/);
    expect(route.request().postDataJSON().revision_digest).toMatch(/^[0-9a-f]{64}$/);
    checkedIn = true;
    await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ version: { id: "version-e2e", version_tag: "v1", version_num: 1 } }) });
  });
  await page.route("**/v1/authoring/process-models/process-e2e/deployments?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ deployments: deployed ? [{ id: "deployment-e2e", status: "ACTIVE" }] : [] }) }));
  await page.route("**/v1/authoring/process-models/process-e2e/deployments", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/);
    expect(route.request().postDataJSON()).toEqual({ process_model_version_id: "version-e2e", target_environment: "stage" });
    deployed = true;
    await route.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ deployment_id: "deployment-e2e" }) });
  });
  await page.route("**/v1/authoring/deployments/deployment-e2e/rollback", async (route) => { expect(route.request().method()).toBe("POST"); await route.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ deployment_id: "rollback-e2e" }) }); });
  await page.route("**/v1/authoring/deployments/deployment-e2e", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ deployment: { id: "deployment-e2e", status: "ACTIVE" } }) }));

  await page.goto("/");
  await page.getByRole("button", { name: "studio" }).click();
  await page.getByLabel("New model key").fill("claims");
  await page.getByLabel("New model name").fill("Claims");
  await page.getByRole("button", { name: "Create process model" }).click();
  await page.getByRole("button", { name: "BPMN source" }).click();
  await page.getByLabel("BPMN XML draft").fill("<definitions><process id='claims'/></definitions>");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/Draft save succeeded/)).toBeVisible();
  await page.getByLabel("Version tag").fill("v1");
  await page.getByLabel("Version summary").fill("First release");
  await page.getByRole("button", { name: "Validate draft" }).click();
  await expect(page.getByRole("button", { name: "Check in version" })).toBeEnabled();
  await page.getByRole("button", { name: "Check in version" }).click();
  await expect(page.getByText(/Version check-in succeeded/)).toBeVisible();
  await page.getByRole("button", { name: "Select for deploy" }).click();
  await page.getByRole("button", { name: "Deploy selected version" }).click();
  await expect(page.getByText(/Deployment command accepted/)).toBeVisible();
  await page.getByRole("button", { name: "Inspect deployment" }).click();
  await expect(page.getByText(/"status": "ACTIVE"/)).toBeVisible();
  await page.getByRole("button", { name: "Rollback this deployment" }).click();
  await expect(page.getByText(/Rollback succeeded/)).toBeVisible();
});

test("validates and publishes an Access catalog draft through the Authz API", async ({ page }) => {
  let status = "DRAFT";
  let created = false;
  await page.route("**/v1/work-items?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ work_items: [] }) }));
  await page.route("**/v1/authoring/process-models?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ models: [], next_page_token: "" }) }));
  await page.route("**/v1/access/catalog/versions", async (route) => {
    if (route.request().method() === "POST") { expect(route.request().postDataJSON()).toEqual({ snapshot: { schema_version: 1, entries: [], policy_grants: [] }, reason: "Approve catalog" }); created = true; return route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "authz-v1", version_num: 1, draft_revision: 1, state: "DRAFT" }) }); }
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(created ? [{ id: "authz-v1", version_num: 1, draft_revision: 1, state: status, snapshot: { schema_version: 1, entries: [], policy_grants: [] } }] : []) });
  });
  await page.route("**/v1/access/catalog/versions/authz-v1", async (route) => { expect(route.request().postDataJSON().expected_draft_revision).toBe(1); await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "authz-v1", state: "DRAFT", draft_revision: 2 }) }); });
  await page.route("**/v1/access/catalog/entries", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) }));
  await page.route("**/v1/access/catalog/audit?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) }));
  await page.route("**/v1/access/catalog/acknowledgements?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) }));
  await page.route("**/v1/access/catalog/roles", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([{ code: "reviewer", name: "Reviewer" }]) }));
  await page.route("**/v1/access/users?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ users: [], next_page_token: "" }) }));
  await page.route("**/v1/access/queues", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ groups: [{ group_id: "reviewers", policy_count: 1 }] }) }));
  const assignmentPolicy = { policy_ref: "review-approval", workflow_type: "claims", workflow_version: "1", node_id: "review", assignee_id: "", candidate_group: "reviewers", sla_duration_ms: 86_400_000, escalation_policy_ref: "", config_version: "policy-1", version: 2, is_deleted: false };
  await page.route("**/v1/assignment-policies?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ policies: [assignmentPolicy], next_page_token: "" }) }));
  await page.route("**/v1/assignment-policies", async (route) => { expect(route.request().method()).toBe("PUT"); expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/); expect(route.request().postDataJSON()).toEqual({ policy_ref: "review-approval", workflow_type: "claims", workflow_version: "1", node_id: "review", assignee_id: "", candidate_group: "reviewers", sla_duration_ms: 3_600_000, escalation_policy_ref: "", config_version: "policy-1", expected_version: 2 }); await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ policy: { ...assignmentPolicy, sla_duration_ms: 3_600_000, version: 3 }, duplicate: false }) }); });
  await page.route("**/v1/access/catalog/versions/authz-v1/validate", async (route) => { expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/); status = "VALIDATED"; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "authz-v1", state: status }) }); });
  await page.route("**/v1/access/catalog/versions/authz-v1/publish", async (route) => { expect(route.request().postDataJSON()).toEqual({ expected_version_num: 1, reason: "Approve catalog" }); status = "PUBLISHED"; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "authz-v1", state: status }) }); });

  await page.goto("/"); await page.getByRole("button", { name: "studio" }).click(); await page.getByRole("button", { name: "Access" }).click();
  await page.getByLabel("Access change reason").fill("Approve catalog");
  await page.getByRole("button", { name: "Create draft" }).click();
  await page.getByRole("button", { name: /v1 · DRAFT/ }).click();
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/Access draft updated succeeded/)).toBeVisible();
  await page.getByRole("button", { name: "Validate draft" }).click();
  await expect(page.getByText(/Access catalog validation succeeded/)).toBeVisible();
  await page.getByRole("button", { name: "Publish catalog" }).click();
  await expect(page.getByText(/Access catalog publish succeeded/)).toBeVisible();
  await page.getByRole("button", { name: /review-approval/ }).click();
  await page.getByLabel("Policy SLA milliseconds").fill("3600000");
  await page.getByRole("button", { name: "Save assignment policy" }).click();
  await expect(page.getByText(/Assignment policy saved succeeded/)).toBeVisible();
});

test("creates, versions, publishes, rolls back and retires a configuration profile", async ({ page }) => {
  let aggregateVersion = 1;
  let versions: Array<{ id: string; config_version: string; status: string }> = [];
  let created = false;
  await page.route("**/v1/work-items?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ work_items: [] }) }));
  await page.route("**/v1/authoring/process-models?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ models: [], next_page_token: "" }) }));
  await page.route("**/v1/configuration/profiles?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ profiles: created ? [{ id: "profile-1", name: "Engine settings", aggregate_version: aggregateVersion }] : [], next_page_token: "" }) }));
  await page.route("**/v1/configuration/profiles", async (route) => { expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/); created = true; versions = [{ id: "config-v1", config_version: "1", status: "PUBLISHED" }]; await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "profile-1", name: "Engine settings", aggregate_version: aggregateVersion }) }); });
  await page.route("**/v1/configuration/profiles/profile-1", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ profile: { id: "profile-1", name: "Engine settings", aggregate_version: aggregateVersion }, versions }) }));
  await page.route("**/v1/configuration/profiles/profile-1/versions", async (route) => { expect(route.request().postDataJSON().expected_version).toBe(aggregateVersion); aggregateVersion += 1; versions = [...versions, { id: "config-v2", config_version: "2", status: "DRAFT" }]; await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ id: "profile-1" }) }); });
  for (const action of ["publish", "rollback", "restore", "retire"] as const) {
    await page.route(`**/v1/configuration/profiles/profile-1/versions/config-v2/${action}`, async (route) => { expect(route.request().headers()["idempotency-key"]).toMatch(/^[0-9a-f]{32}$/); aggregateVersion += 1; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "profile-1", aggregate_version: aggregateVersion }) }); });
  }
  await page.route("**/v1/configuration/profiles/profile-1/retire", async (route) => { aggregateVersion += 1; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "profile-1", aggregate_version: aggregateVersion }) }); });
  await page.route("**/v1/configuration/profiles/profile-1/diff?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ differences: [] }) }));

  await page.goto("/"); await page.getByRole("button", { name: "studio" }).click(); await page.getByRole("button", { name: "Configuration" }).click();
  await page.getByLabel("Profile name").fill("Engine settings"); await page.getByLabel("Scope reference").fill("tenant-demo"); await page.getByLabel("Configuration reason").fill("Initial configuration");
  await page.getByLabel("Policy version").fill("policy-1");
  await page.getByRole("button", { name: "Create profile" }).click();
  await page.getByRole("button", { name: /Engine settings/ }).click();
  await page.getByRole("button", { name: "Add draft version" }).click();
  await expect(page.getByText(/Configuration draft created/)).toBeVisible();
  await page.getByRole("listitem").filter({ hasText: "config-v2" }).getByRole("button", { name: "Select version" }).click();
  await page.getByRole("button", { name: "Publish selected" }).click(); await expect(page.getByText(/Configuration publish succeeded/)).toBeVisible();
  await page.getByLabel("Policy version").fill("policy-1");
  await page.getByRole("button", { name: "Rollback to selected" }).click(); await expect(page.getByText(/Configuration rollback succeeded/)).toBeVisible();
  await page.getByRole("button", { name: "Restore selected" }).click(); await expect(page.getByText(/Configuration restore succeeded/)).toBeVisible();
  await page.getByLabel("Diff from version").selectOption("config-v1"); await page.getByLabel("Diff to version").selectOption("config-v2"); await expect(page.getByText(/differences/)).toBeVisible();
  await page.getByRole("button", { name: "Retire profile" }).click(); await expect(page.getByText(/Configuration retire succeeded/)).toBeVisible();
});

test("authors a DMN decision and publishes a form through Authoring APIs", async ({ page }) => {
  let decisionCreated = false; let decisionRevision = 1; let decisionValid = false; let decisionCheckedIn = false;
  let formCreated = false; let formRevision = 1; let formValid = false; let formPublished = false;
  await page.route("**/v1/work-items?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ work_items: [] }) }));
  await page.route("**/v1/authoring/process-models?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ models: [], next_page_token: "" }) }));
  await page.route("**/v1/authoring/decision-models?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ models: decisionCreated ? [{ id: "decision-e2e", name: "Eligibility", model_key: "eligibility" }] : [], next_page_token: "" }) }));
  await page.route("**/v1/authoring/decision-models", async (route) => { decisionCreated = true; await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ model: { id: "decision-e2e", name: "Eligibility", model_key: "eligibility" }, draft: { revision: 1, dmn_xml: route.request().postDataJSON().initial_dmn_xml, validation_status: "INVALID" } }) }); });
  await page.route("**/v1/authoring/decision-models/decision-e2e", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ model: { id: "decision-e2e", name: "Eligibility", model_key: "eligibility", revision: decisionRevision }, draft: { revision: decisionRevision, dmn_xml: '<definitions id="eligibility"/>', validation_status: decisionValid ? "VALID" : "INVALID", diagnostics: [] } }) }));
  await page.route("**/v1/authoring/decision-models/decision-e2e/draft", async (route) => { expect(route.request().postDataJSON().expected_revision).toBe(decisionRevision); decisionRevision += 1; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ draft: { revision: decisionRevision } }) }); });
  await page.route("**/v1/authoring/decision-models/decision-e2e/draft/validate", async (route) => { decisionValid = true; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ revision: decisionRevision, valid: true, diagnostics: [] }) }); });
  await page.route("**/v1/authoring/decision-models/decision-e2e/versions?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ versions: decisionCheckedIn ? [{ id: "decision-version-1", version_tag: "d1" }] : [] }) }));
  await page.route("**/v1/authoring/decision-models/decision-e2e/versions", async (route) => { expect(route.request().postDataJSON().revision_digest).toMatch(/^[0-9a-f]{64}$/); decisionCheckedIn = true; await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ version: { id: "decision-version-1" } }) }); });
  await page.route("**/v1/authoring/forms?page_size=50", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ forms: formCreated ? [{ id: "form-e2e", form_key: "review", name: "Review" }] : [], next_page_token: "" }) }));
  await page.route("**/v1/authoring/forms", async (route) => { const body = route.request().postDataJSON(); expect(JSON.parse(body.schema_json).type).toBe("object"); expect(JSON.parse(body.ui_schema_json).type).toBe("VerticalLayout"); formCreated = true; await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ form: { id: "form-e2e", form_key: "review", name: "Review", revision: 1 }, draft: { revision: 1, validation_status: "INVALID" } }) }); });
  await page.route("**/v1/authoring/forms/form-e2e", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ form: { id: "form-e2e", form_key: "review", name: "Review", revision: formRevision }, draft: { revision: formRevision, schema_json: '{"type":"object","properties":{}}', ui_schema_json: '{"type":"VerticalLayout","elements":[]}', validation_status: formValid ? "VALID" : "INVALID", diagnostics: [] } }) }));
  await page.route("**/v1/authoring/forms/form-e2e/draft", async (route) => { expect(route.request().postDataJSON().expected_revision).toBe(formRevision); formRevision += 1; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ draft: { revision: formRevision } }) }); });
  await page.route("**/v1/authoring/forms/form-e2e/draft/validate", async (route) => { formValid = true; await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ revision: formRevision, valid: true, diagnostics: [] }) }); });
  await page.route("**/v1/authoring/forms/form-e2e/versions?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ versions: formPublished ? [{ id: "form-version-1", version_tag: "f1" }] : [] }) }));
  await page.route("**/v1/authoring/forms/form-e2e/versions", async (route) => { expect(route.request().postDataJSON().revision_digest).toMatch(/^[0-9a-f]{64}$/); formPublished = true; await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ version: { id: "form-version-1" } }) }); });

  await page.goto("/"); await page.getByRole("button", { name: "studio" }).click();
  await page.getByRole("button", { name: "DMN decision" }).click();
  await page.getByLabel("decision key").fill("eligibility"); await page.getByLabel("decision name").fill("Eligibility"); await page.getByRole("button", { name: "Create decision model" }).click();
  await page.getByRole("button", { name: "Save draft" }).click(); await page.getByRole("button", { name: "Validate draft" }).click();
  await page.getByLabel("Artifact version tag").fill("d1"); await page.getByLabel("Artifact version summary").fill("First decision revision");
  await expect(page.getByRole("button", { name: "Check in decision version" })).toBeEnabled(); await page.getByRole("button", { name: "Check in decision version" }).click();
  await expect(page.getByText(/Version check-in succeeded/)).toBeVisible();
  await page.getByRole("button", { name: "Forms" }).click();
  await page.getByLabel("form key").fill("review"); await page.getByLabel("form name").fill("Review"); await page.getByRole("button", { name: "Create form" }).click();
  await page.getByRole("button", { name: "Save draft" }).click(); await page.getByRole("button", { name: "Validate draft" }).click();
  await page.getByLabel("Artifact version tag").fill("f1"); await page.getByLabel("Artifact version summary").fill("First form version");
  await expect(page.getByRole("button", { name: "Publish form version" })).toBeEnabled(); await page.getByRole("button", { name: "Publish form version" }).click();
  await expect(page.getByText(/Form publish succeeded/)).toBeVisible();
});
