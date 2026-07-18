// Repository port. Application depends on this abstract token; concrete
// HTTP implementation is provided in the infrastructure layer.
import { InjectionToken } from '@angular/core';
import { Observable } from 'rxjs';
import type { NodeKind, Organization } from './organization.model';

export interface ListParams {
  offset: number;
  limit: number;
}

export interface CreateOrganizationInput {
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

export abstract class OrganizationRepository {
  abstract list(params: ListParams): Observable<readonly Organization[]>;
  abstract create(
    input: CreateOrganizationInput,
  ): Observable<{ id: string; version: number }>;
  abstract addNode(input: AddNodeInput): Observable<{ version: number }>;
  abstract moveNode(input: MoveNodeInput): Observable<void>;
}

export const ORGANIZATION_REPOSITORY = new InjectionToken<OrganizationRepository>(
  'OrganizationRepository',
);
