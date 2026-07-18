// Adapter over the HttpClient. One file per feature; pure functions, no React.
// This boundary lets us swap fetch for MSW in tests without touching hooks.
import type { HttpClient } from "@shared/api/http";
import {
  AddNodeResponseSchema,
  CreateOrganizationResponseSchema,
  EmptySchema,
  OrganizationListSchema,
  type NodeKind,
} from "./types";

export interface ListParams {
  offset: number;
  limit: number;
}

export interface CreateOrgInput {
  code: string;
  name: string;
}

export interface AddNodeInput {
  orgId: string;
  parentId: string;
  kind: NodeKind;
  code: string;
  name: string;
  expectedVersion: number;
}

export interface MoveNodeInput {
  orgId: string;
  nodeId: string;
  newParentId: string;
  expectedVersion: number;
}

export class OrganizationsApi {
  constructor(private readonly http: HttpClient) {}

  list(params: ListParams) {
    const qs = new URLSearchParams({
      offset: String(params.offset),
      limit: String(params.limit),
    });
    return this.http.get(
      `/api/v1/organizations?${qs.toString()}`,
      OrganizationListSchema,
    );
  }

  create(body: CreateOrgInput) {
    return this.http.post(
      `/api/v1/organizations`,
      body,
      CreateOrganizationResponseSchema,
    );
  }

  addNode(input: AddNodeInput) {
    const { orgId, expectedVersion, ...rest } = input;
    return this.http.post(
      `/api/v1/organizations/${orgId}/nodes`,
      { ...rest, parent_id: rest.parentId, expected_version: expectedVersion },
      AddNodeResponseSchema,
    );
  }

  moveNode(input: MoveNodeInput) {
    const { orgId, nodeId, newParentId, expectedVersion } = input;
    return this.http.post(
      `/api/v1/organizations/${orgId}/nodes/${nodeId}/move`,
      { new_parent_id: newParentId, expected_version: expectedVersion },
      EmptySchema,
    );
  }
}
