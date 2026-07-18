// Zod schemas mirror the JSON shapes from `authz-app` handlers.
// These are the single source of truth for query/mutation typing.
import { z } from "zod";

export const NodeKindSchema = z.enum([
  "GROUP",
  "SUBSIDIARY",
  "BRANCH",
  "DEPARTMENT",
]);
export type NodeKind = z.infer<typeof NodeKindSchema>;

export const OrganizationRowSchema = z.object({
  id: z.string().uuid(),
  code: z.string(),
  name: z.string(),
  root_path: z.string(),
  node_count: z.number().int().nonnegative(),
  version: z.number().int().nonnegative(),
});
export type OrganizationRow = z.infer<typeof OrganizationRowSchema>;

export const OrganizationListSchema = z.array(OrganizationRowSchema);

export const CreateOrganizationResponseSchema = z.object({
  id: z.string().uuid(),
  version: z.number().int(),
});

export const AddNodeResponseSchema = z.object({
  version: z.number().int(),
});

export const EmptySchema = z.object({}).passthrough();
