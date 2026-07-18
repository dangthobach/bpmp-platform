// HTTP adapter for the OrganizationRepository port. Only file in the feature
// that knows about HttpClient or URL paths.
import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { map, Observable } from 'rxjs';
import { z } from 'zod';

import type { Organization } from '../domain/organization.model';
import {
  OrganizationRepository,
  type AddNodeInput,
  type CreateOrganizationInput,
  type ListParams,
  type MoveNodeInput,
} from '../domain/organization.repository';
import { ApiException, EnvelopeSchema } from '../../shared/envelope';

const Row = z.object({
  id: z.string(),
  code: z.string(),
  name: z.string(),
  root_path: z.string(),
  node_count: z.number(),
  version: z.number(),
});
const RowList = z.array(Row);
const CreateRes = z.object({ id: z.string(), version: z.number() });
const AddRes = z.object({ version: z.number() });
const Empty = z.object({}).passthrough();

@Injectable({ providedIn: 'root' })
export class HttpOrganizationRepository extends OrganizationRepository {
  private readonly http = inject(HttpClient);
  private readonly base = '/api/v1/organizations';

  override list(params: ListParams): Observable<readonly Organization[]> {
    return this.http
      .get<unknown>(this.base, {
        params: { offset: params.offset, limit: params.limit },
      })
      .pipe(map((raw) => unwrap(raw, RowList).map(toDomain)));
  }

  override create(
    input: CreateOrganizationInput,
  ): Observable<{ id: string; version: number }> {
    return this.http
      .post<unknown>(this.base, input)
      .pipe(map((raw) => unwrap(raw, CreateRes)));
  }

  override addNode(input: AddNodeInput): Observable<{ version: number }> {
    const { orgId, parentId, expectedVersion, ...rest } = input;
    return this.http
      .post<unknown>(`${this.base}/${orgId}/nodes`, {
        ...rest,
        parent_id: parentId,
        expected_version: expectedVersion,
      })
      .pipe(map((raw) => unwrap(raw, AddRes)));
  }

  override moveNode(input: MoveNodeInput): Observable<void> {
    const { orgId, nodeId, newParentId, expectedVersion } = input;
    return this.http
      .post<unknown>(`${this.base}/${orgId}/nodes/${nodeId}/move`, {
        new_parent_id: newParentId,
        expected_version: expectedVersion,
      })
      .pipe(map((raw) => { unwrap(raw, Empty); }));
  }
}

function unwrap<T>(raw: unknown, schema: z.ZodType<T>): T {
  const env = EnvelopeSchema(schema).safeParse(raw);
  if (!env.success) {
    throw new ApiException(0, 'INVALID_RESPONSE', env.error.message, '');
  }
  if (!env.data.success || !env.data.data) {
    const err = env.data.error ?? { code: 'UNKNOWN', message: 'empty' };
    throw new ApiException(0, err.code, err.message, env.data.request_id);
  }
  return env.data.data as T;
}

function toDomain(r: z.infer<typeof Row>): Organization {
  return {
    id: r.id,
    code: r.code,
    name: r.name,
    rootPath: r.root_path,
    nodeCount: r.node_count,
    version: r.version,
  };
}
