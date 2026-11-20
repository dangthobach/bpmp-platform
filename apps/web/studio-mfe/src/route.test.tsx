import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SWRConfig } from "swr";
import type { CallApi } from "@ods/call-api";
import { OdsThemeProvider } from "@ods/shared-ui";
import moduleContract from "./route";

describe("BPMP Studio", () => {
  it("provisions a tenant access profile with an idempotency key", async () => {
    const callApi = vi.fn(async (_options: { method: string; path: string; body?: unknown; idempotencyKey?: string }) => ({
      ok: true as const, data: { profiles: [], users: [], versions: [], entries: [], roles: [], records: [] },
      requestId: "r-profile", correlationId: "c-profile",
    }));
    const Component = moduleContract.route.Component;
    render(<SWRConfig value={{ provider: () => new Map() }}><OdsThemeProvider><Component platform={{ callApi: callApi as unknown as CallApi, navigate: vi.fn(), reportError: vi.fn() }} /></OdsThemeProvider></SWRConfig>);
    fireEvent.click(screen.getByRole("button", { name: "Access" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Identity username" }), { target: { value: "jane.doe" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Keycloak issuer" }), { target: { value: "https://idp.example/realms/bpmp" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Keycloak subject" }), { target: { value: "subject-17" } });
    fireEvent.click(screen.getByRole("button", { name: "Provision profile" }));
    await screen.findByText(/Tenant access profile provisioned succeeded/);
    expect(callApi).toHaveBeenCalledWith(expect.objectContaining({
      method: "POST", path: "/v1/access/identity/profiles",
      body: { username: "jane.doe", issuer: "https://idp.example/realms/bpmp", subject: "subject-17" },
      idempotencyKey: expect.any(String),
    }));
  });

  it("lists immutable versions, deploys a selected version, inspects and rolls back a deployment", async () => {
    const callApi = vi.fn(async (options: { method: string; path: string; body?: unknown; idempotencyKey?: string }) => {
      if (options.method === "GET" && options.path === "/v1/authoring/process-models?page_size=50") return { ok: true as const, data: { models: [{ id: "model-1", name: "Claims" }] }, requestId: "r-m", correlationId: "c" };
      if (options.method === "GET" && options.path === "/v1/authoring/process-models/model-1") return { ok: true as const, data: { model: { id: "model-1", model_key: "claims" }, draft: { revision: 1, bpmn_xml: '<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p"><bpmn:startEvent id="start"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="f" sourceRef="start" targetRef="end"/></bpmn:process></bpmn:definitions>', validation_status: "VALID", diagnostics: [] } }, requestId: "r-detail", correlationId: "c" };
      if (options.method === "GET" && options.path.includes("/versions?")) return { ok: true as const, data: { versions: [{ id: "version-1", version_tag: "v1" }] }, requestId: "r-v", correlationId: "c" };
      if (options.method === "GET" && options.path.includes("/deployments?") ) return { ok: true as const, data: { deployments: [{ id: "deployment-1", status: "ACTIVE" }] }, requestId: "r-d", correlationId: "c" };
      if (options.method === "GET" && options.path.includes("/deployments/deployment-1")) return { ok: true as const, data: { deployment: { id: "deployment-1", status: "ACTIVE" } }, requestId: "r-detail", correlationId: "c" };
      return { ok: true as const, data: { deployment_id: "deployment-1" }, requestId: "r-post", correlationId: "c" };
    });
    const Component = moduleContract.route.Component;
    render(<OdsThemeProvider><Component platform={{ callApi: callApi as unknown as CallApi, navigate: vi.fn(), reportError: vi.fn() }} /></OdsThemeProvider>);
    fireEvent.click(await screen.findByRole("button", { name: "Claims" }));
    expect(await screen.findByText("Editable path")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Start event/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "BPMN source" }));
    expect(screen.getByRole("textbox", { name: "BPMN XML draft" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Visual canvas" }));
    fireEvent.click(await screen.findByRole("button", { name: "Select for deploy" }));
    fireEvent.click(screen.getByRole("button", { name: "Deploy selected version" }));
    await screen.findByText(/Deployment command accepted/);
    const deployCall = callApi.mock.calls.find(([input]) => input.method === "POST" && input.path.endsWith("/deployments"));
    expect(deployCall?.[0]).toEqual(expect.objectContaining({ body: { process_model_version_id: "version-1", target_environment: "stage" }, idempotencyKey: expect.any(String) }));
    fireEvent.click(await screen.findByRole("button", { name: "Inspect deployment" }));
    await waitFor(() => expect(callApi).toHaveBeenCalledWith(expect.objectContaining({ path: "/v1/authoring/deployments/deployment-1" })));
    fireEvent.click(screen.getByRole("button", { name: "Rollback this deployment" }));
    await screen.findByText(/Rollback succeeded/);
    expect(callApi).toHaveBeenCalledWith(expect.objectContaining({ method: "POST", path: "/v1/authoring/deployments/deployment-1/rollback", idempotencyKey: expect.any(String) }));
  });
});
