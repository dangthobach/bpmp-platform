// Single OrganizationsApi instance bound to the shared HttpClient.
// Injected via React context to keep features loosely coupled from wiring.
import { createContext, useContext } from "react";
import { OrganizationsApi } from "./organizationsApi";

export const OrgApiContext = createContext<OrganizationsApi | null>(null);

export function useOrgApi(): OrganizationsApi {
  const v = useContext(OrgApiContext);
  if (!v) throw new Error("OrgApiContext not provided");
  return v;
}
