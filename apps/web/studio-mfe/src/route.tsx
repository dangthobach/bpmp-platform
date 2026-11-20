import { useState, type ReactNode } from "react";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { Page, Stack, Button } from "@ods/shared-ui";
import { AccessWorkspace } from "./AccessWorkspace";
import { AuthoringWorkspace } from "./AuthoringWorkspace";
import { ConfigurationWorkspace } from "./ConfigurationWorkspace";

type Area = "authoring" | "access" | "configuration";
const labels: Record<Area, string> = { authoring: "Authoring & deployment", access: "Access", configuration: "Configuration" };
function StudioRoute({ platform }: RemoteRouteProps): ReactNode {
  const [area, setArea] = useState<Area>("authoring");
  return <Page title="BPMP Studio">
    <Stack direction="row" spacing={1}>
      {(Object.keys(labels) as Area[]).map((item) => <Button key={item} type="button" variant={area === item ? "contained" : "outlined"} onClick={() => setArea(item)}>{labels[item]}</Button>)}
    </Stack>
    {area === "authoring" ? <AuthoringWorkspace platform={platform} /> : null}
    {area === "access" ? <AccessWorkspace platform={platform} /> : null}
    {area === "configuration" ? <ConfigurationWorkspace platform={platform} /> : null}
  </Page>;
}

export const contractVersion = "1.0";
export const route = { path: "/studio", title: "BPMP Studio", Component: StudioRoute };
const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
