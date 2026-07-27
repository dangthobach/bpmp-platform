import { z } from "zod";

export const nodeKindSchema = z.enum([
  "GROUP",
  "SUBSIDIARY",
  "BRANCH",
  "DEPARTMENT",
]);
export type NodeKind = z.infer<typeof nodeKindSchema>;

export const organizationRowSchema = z.object({
  id: z.string().uuid(),
  code: z.string(),
  name: z.string(),
  root_path: z.string(),
  node_count: z.number().int().nonnegative(),
  version: z.number().int().nonnegative(),
});
export type OrganizationRow = z.infer<typeof organizationRowSchema>;

export const organizationListSchema = z.array(organizationRowSchema);

export const organizationNodeSchema = z.object({
  id: z.string().uuid(),
  parent_id: z.string().uuid().nullable(),
  code: z.string(),
  name: z.string(),
  kind: nodeKindSchema,
  path: z.string(),
  is_active: z.boolean(),
});
export type OrganizationNode = z.infer<typeof organizationNodeSchema>;

export const organizationDetailSchema = z.object({
  id: z.string().uuid(),
  root_node_id: z.string().uuid(),
  version: z.number().int().nonnegative(),
  nodes: z.array(organizationNodeSchema),
});
export type OrganizationDetail = z.infer<typeof organizationDetailSchema>;

export const createOrganizationResponseSchema = z.object({
  id: z.string().uuid(),
  version: z.number().int().nonnegative(),
});

export const addNodeResponseSchema = z.object({
  version: z.number().int().nonnegative(),
});

export const emptyResponseSchema = z.object({}).passthrough();

export interface CreateOrganizationInput {
  code: string;
  name: string;
}

export interface AddOrganizationNodeInput {
  organizationId: string;
  parentId: string;
  kind: NodeKind;
  code: string;
  name: string;
  expectedVersion: number;
}

export interface MoveOrganizationNodeInput {
  organizationId: string;
  nodeId: string;
  newParentId: string;
  expectedVersion: number;
}
