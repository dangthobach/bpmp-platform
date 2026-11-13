// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiProvider } from "../../api/ApiContext";
import { AuthProvider } from "../../auth/AuthContext";
import { ConfigProvider } from "../../config/ConfigContext";
import type { RuntimeConfig } from "../../config/runtime";
import { ModelerPage } from "./ModelerPage";

const config: RuntimeConfig = {
  apiBaseUrl: "https://gateway.example.test/", organizationApiBaseUrl: "https://authz.example.test/",
  realtimeBaseUrl: "https://cockpit.example.test/", realtimePath: "/events", realtimeSignalNames: [],
  realtimeReconnectInitialMs: 500, realtimeReconnectMaxMs: 5000, defaultPageSize: 50, maxPageSize: 100,
  batchChunkSize: 25, batchConcurrency: 4, requestTimeoutMs: 5000, staleTimeMs: 0,
};
const tenantId = "d7b6e94b-317f-4a61-a223-ac78069380a1";
const modelId = "74636a01-8143-4a03-b3fd-20cbbbc42bf7";
const xml = "<bpmn:definitions xmlns:bpmn=\"http://www.omg.org/spec/BPMN/20100524/MODEL\"/>";

function detail(revision: number, source: string) {
  return {
    model: { id: modelId, tenant_id: tenantId, model_key: "invoice", name: "Invoice", category: "Finance", description: "", status: "DRAFT", revision, latest_version_id: "", created_at_epoch_ms: 1, updated_at_epoch_ms: 1 },
    draft: { process_model_id: modelId, revision, base_version_id: "", bpmn_xml: source, validation_status: "NOT_VALIDATED", diagnostics: [], updated_at_epoch_ms: 1 },
  };
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<ConfigProvider config={config}><AuthProvider><QueryClientProvider client={queryClient}><ApiProvider><ModelerPage /></ApiProvider></QueryClientProvider></AuthProvider></ConfigProvider>);
}

describe("Modeler API workflow", () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
  beforeEach(() => {
    sessionStorage.setItem("bpmp.cockpit.session.v1", JSON.stringify({ tenantId, accessToken: "signed-token" }));
  });

  it("creates a model, saves a revisioned draft, then validates that persisted revision", async () => {
    let current = detail(1, xml);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      const method = init?.method ?? "GET";
      let status = 200;
      let body: unknown;
      if (url.pathname === "/v1/authoring/process-models" && method === "GET") body = { models: current.model.revision > 1 ? [current.model] : [], next_page_token: "" };
      else if (url.pathname === "/v1/authoring/process-models" && method === "POST") { current = detail(1, (JSON.parse(String(init?.body)) as { initial_bpmn_xml: string }).initial_bpmn_xml); body = current; status = 201; }
      else if (url.pathname === `/v1/authoring/process-models/${modelId}` && method === "GET") body = current;
      else if (url.pathname.endsWith("/draft") && method === "PUT") { const request = JSON.parse(String(init?.body)) as { expected_revision: number; bpmn_xml: string }; current = detail(request.expected_revision + 1, request.bpmn_xml); body = { draft: current.draft, replayed: false }; }
      else if (url.pathname.endsWith("/draft/validate") && method === "POST") body = { process_model_id: modelId, revision: current.draft.revision, valid: true, diagnostics: [] };
      else throw new Error(`Unexpected request ${method} ${url.pathname}`);
      return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPage();

    await screen.findByText("No process models");
    fireEvent.click(screen.getByRole("button", { name: "New process model" }));
    fireEvent.change(screen.getByLabelText("Model key"), { target: { value: "invoice" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Invoice" } });
    fireEvent.click(screen.getByRole("button", { name: "Create draft" }));
    await screen.findByRole("heading", { name: "Invoice" });

    const editor = screen.getByRole("textbox", { name: "BPMN draft source" });
    fireEvent.click(screen.getByRole("button", { name: "Service task" }));
    expect((editor as HTMLTextAreaElement).value).toContain(":serviceTask");
    fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "Invoice review" } });
    expect((editor as HTMLTextAreaElement).value).toContain('name="Invoice review"');
    fireEvent.change(editor, { target: { value: `${(editor as HTMLTextAreaElement).value}\n<!-- edited -->` } });
    fireEvent.click(screen.getByRole("button", { name: "Save draft" }));
    await screen.findByText(/Draft saved at revision 2/);
    fireEvent.click(screen.getByRole("button", { name: "Validate saved draft" }));
    await screen.findByText(/Revision 2: Valid/);

    const writes = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST" || init?.method === "PUT");
    expect(writes.map(([, init]) => init?.method)).toEqual(["POST", "PUT", "POST"]);
    const createHeaders = new Headers(writes[0]?.[1]?.headers);
    expect(createHeaders.get("Authorization")).toBe("Bearer signed-token");
    expect(createHeaders.get("X-BPMP-Tenant-ID")).toBe(tenantId);
    expect(createHeaders.get("Idempotency-Key")).toBeTruthy();
    expect(JSON.parse(String(writes[1]?.[1]?.body))).toMatchObject({ expected_revision: 1, bpmn_xml: expect.stringContaining("edited") });
    expect(JSON.parse(String(writes[2]?.[1]?.body))).toEqual({ expected_revision: 2 });
  });

  it("creates and edits a DMN table through Authoring APIs", async () => {
    const decisionId = "c7cb5db9-5b55-4717-a6af-cdfb5e4216e1";
    let dmn = `<?xml version="1.0"?><definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="definitions" name="Decision model" namespace="https://bpmp.example/decision"><decision id="decision" name="Decision"><decisionTable id="decisionTable" hitPolicy="FIRST"><input id="input" label="Input"><inputExpression id="inputExpression"><text>input</text></inputExpression></input><output id="output" name="result" typeRef="string"/></decisionTable></decision></definitions>`;
    let revision = 1;
    const decisionDetail = () => ({ model:{id:decisionId,tenant_id:tenantId,model_key:"decision.generic",name:"Generic decision",description:"",status:"DRAFT",revision,created_at_epoch_ms:1,updated_at_epoch_ms:1}, draft:{decision_model_id:decisionId,revision,dmn_xml:dmn,validation_status:"NOT_VALIDATED",diagnostics:[],updated_at_epoch_ms:1} });
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input)); const method = init?.method ?? "GET"; let status=200; let body:unknown;
      if(url.pathname==="/v1/authoring/process-models") body={models:[],next_page_token:""};
      else if(url.pathname==="/v1/authoring/decision-models"&&method==="GET") body={models:revision>1?[decisionDetail().model]:[],next_page_token:""};
      else if(url.pathname==="/v1/authoring/decision-models"&&method==="POST"){dmn=(JSON.parse(String(init?.body)) as {initial_dmn_xml:string}).initial_dmn_xml;body=decisionDetail();status=201;}
      else if(url.pathname===`/v1/authoring/decision-models/${decisionId}`&&method==="GET") body=decisionDetail();
      else if(url.pathname.endsWith("/draft")&&method==="PUT"){const request=JSON.parse(String(init?.body)) as {expected_revision:number;dmn_xml:string};expect(request.expected_revision).toBe(revision);dmn=request.dmn_xml;revision++;body={draft:decisionDetail().draft,replayed:false};}
      else if(url.pathname.endsWith("/draft/validate")&&method==="POST") body={decision_model_id:decisionId,revision,valid:true,diagnostics:[]};
      else throw new Error(`Unexpected request ${method} ${url.pathname}`);
      return new Response(JSON.stringify(body),{status,headers:{"Content-Type":"application/json"}});
    });
    vi.stubGlobal("fetch",fetchMock); renderPage();
    await screen.findByText("No process models"); fireEvent.click(screen.getByRole("button",{name:"DMN"})); await screen.findByText("No decision models");
    fireEvent.click(screen.getByRole("button",{name:"New decision model"})); fireEvent.change(screen.getByLabelText("Model key"),{target:{value:"decision.generic"}}); fireEvent.change(screen.getByLabelText("Name"),{target:{value:"Generic decision"}}); fireEvent.click(screen.getByRole("button",{name:"Create draft"})); await screen.findByRole("heading",{name:"Generic decision"});
    const importedDmn=`<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="imported"><decision id="decision" name="Decision"><decisionTable id="table" hitPolicy="FIRST"><input id="input" label="Input"><inputExpression id="expression"><text>input</text></inputExpression></input><output id="output" name="result" typeRef="string"/></decisionTable></decision></definitions>`;
    fireEvent.change(document.querySelector('input[type="file"]')!,{target:{files:[new File([importedDmn],"generic.dmn",{type:"application/xml"})]}});await screen.findByRole("button",{name:"Add rule"});
    fireEvent.click(screen.getByRole("button",{name:"Add rule"})); fireEvent.change(screen.getByRole("textbox",{name:"Rule 1 input"}),{target:{value:"> 10"}}); fireEvent.change(screen.getByRole("textbox",{name:"Rule 1 output"}),{target:{value:"eligible"}}); fireEvent.click(screen.getByRole("button",{name:"Save draft"})); await screen.findByText(/Draft saved at revision 2/); fireEvent.click(screen.getByRole("button",{name:"Validate saved draft"})); await screen.findByText(/Revision 2: Valid/);
    expect(dmn).toContain("imported");expect(dmn).toContain("eligible"); expect(fetchMock.mock.calls.some(([,init])=>init?.method==="PUT")).toBe(true);
  });

  it("imports a BPMN file then persists it through the revision-checked API",async()=>{
    let current=detail(1,xml);const imported=`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p"><bpmn:startEvent id="s"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="f" sourceRef="s" targetRef="e"/></bpmn:process></bpmn:definitions>`;
    const fetchMock=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{const url=new URL(String(input));const method=init?.method??"GET";let body:unknown;let status=200;
      if(url.pathname==="/v1/authoring/process-models"&&method==="GET")body={models:current.model.revision>1?[current.model]:[],next_page_token:""};
      else if(url.pathname==="/v1/authoring/process-models"&&method==="POST"){current=detail(1,(JSON.parse(String(init?.body)) as {initial_bpmn_xml:string}).initial_bpmn_xml);body=current;status=201;}
      else if(url.pathname===`/v1/authoring/process-models/${modelId}`)body=current;
      else if(url.pathname.endsWith("/draft")&&method==="PUT"){const request=JSON.parse(String(init?.body)) as {expected_revision:number;bpmn_xml:string};expect(request.expected_revision).toBe(current.draft.revision);current=detail(request.expected_revision+1,request.bpmn_xml);body={draft:current.draft,replayed:false};}
      else throw new Error(`Unexpected request ${method} ${url.pathname}`);
      return new Response(JSON.stringify(body),{status,headers:{"Content-Type":"application/json"}});
    });
    vi.stubGlobal("fetch",fetchMock);renderPage();await screen.findByText("No process models");fireEvent.click(screen.getByRole("button",{name:"New process model"}));fireEvent.change(screen.getByLabelText("Model key"),{target:{value:"invoice"}});fireEvent.change(screen.getByLabelText("Name"),{target:{value:"Invoice"}});fireEvent.click(screen.getByRole("button",{name:"Create draft"}));await screen.findByRole("heading",{name:"Invoice"});
    fireEvent.change(document.querySelector('input[type="file"]')!,{target:{files:[new File([imported],"imported.bpmn",{type:"application/xml"})]}});const sourceEditor=await screen.findByLabelText("BPMN draft source");expect((sourceEditor as HTMLTextAreaElement).value).toBe(imported);fireEvent.click(screen.getByRole("button",{name:"Save draft"}));await screen.findByText(/Draft saved at revision 2/);
    const saveRequest=fetchMock.mock.calls.find(([,init])=>init?.method==="PUT")?.[1];expect(JSON.parse(String(saveRequest?.body))).toMatchObject({expected_revision:1,bpmn_xml:imported});
  });
});
