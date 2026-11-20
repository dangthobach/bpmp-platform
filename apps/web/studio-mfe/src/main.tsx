import { createRoot } from "react-dom/client";
import { createCallApi } from "@ods/call-api";
import { OdsThemeProvider } from "@ods/shared-ui";
import moduleContract from "./route";

const root = document.getElementById("root");
if (!root) throw new Error("BPMP Studio root element is missing");
const Component = moduleContract.route.Component;
createRoot(root).render(<OdsThemeProvider><Component platform={{
  callApi: createCallApi({ defaultTimeoutMs: 15_000, maxResponseBytes: 4 * 1024 * 1024 }),
  navigate: (path) => history.pushState({}, "", path),
  reportError: (error, context) => console.error("BPMP Studio failure", context, error),
}} /></OdsThemeProvider>);
