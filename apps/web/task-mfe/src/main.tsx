import { createRoot } from "react-dom/client";
import { createCallApi } from "@ods/call-api";
import { OdsThemeProvider } from "@ods/shared-ui";
import { route } from "./route";

const root = document.getElementById("root");
if (!root) throw new Error("Task MFE root element is missing");
const Component = route.Component;
createRoot(root).render(<OdsThemeProvider><Component platform={{
  callApi: createCallApi({ defaultTimeoutMs: 15_000, maxResponseBytes: 4 * 1024 * 1024 }),
  navigate: (path) => history.pushState({}, "", path),
  reportError: (error, context) => console.error("Task MFE failure", context, error),
}} /></OdsThemeProvider>);
