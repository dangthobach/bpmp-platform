import { z } from "zod";

export const authorizationCatalogEntrySchema = z.object({
  owner_id: z.string().min(1),
  manifest_version: z.string().min(1),
  manifest_digest: z.string().min(1),
  resource_type: z.string().min(1),
  action: z.string().min(1),
  capability: z.string().min(1),
  supported_scope_types: z.array(z.string()),
  readable_fields: z.array(z.string()),
  writable_fields: z.array(z.string()),
});
export type AuthorizationCatalogEntry = z.infer<typeof authorizationCatalogEntrySchema>;

export const policyGrantSchema = z.object({
  grant_id: z.string().min(1),
  owner_id: z.string().min(1),
  actor_ids: z.array(z.string()).default([]),
  roles: z.array(z.string()).default([]),
  required_capabilities: z.array(z.string()).default([]),
  resource_type: z.string().min(1),
  action: z.string().min(1),
  effect: z.enum(["ALLOW", "DENY"]),
  branch_scope_ids: z.array(z.string()).default([]),
  branch_includes_descendants: z.boolean().default(false),
  queue_scope_ids: z.array(z.string()).default([]),
  own_scope: z.boolean().default(false),
  resource_scope_ids: z.array(z.string()).default([]),
  tenant_scope: z.boolean().default(false),
  readable_fields: z.array(z.string()).default([]),
  writable_fields: z.array(z.string()).default([]),
});

export const catalogAuditEntrySchema = z.object({
  catalog_version_id: z.string().uuid(),
  version_num: z.number().int().positive(),
  actor_ref: z.string(),
  request_id: z.string(),
  action: z.enum(["CREATE_DRAFT", "UPDATE_DRAFT", "VALIDATE", "PUBLISH", "RETIRE"]),
  reason: z.string().nullable(),
  occurred_at: z.string(),
});
export type CatalogAuditEntry = z.infer<typeof catalogAuditEntrySchema>;

export const catalogAcknowledgementSchema = z.object({
  catalog_version_id: z.string().uuid(),
  version_num: z.number().int().positive(),
  bundle_sequence: z.number().int().positive(),
  consumer_id: z.string(),
  consumer_type: z.string(),
  bundle_content_hash: z.string().length(64),
  acknowledged_at_epoch_ms: z.number().int().positive(),
});
export type CatalogAcknowledgement = z.infer<typeof catalogAcknowledgementSchema>;

export const assignmentGroupSchema = z.object({
  group_id: z.string().min(1),
  policy_count: z.number().int().nonnegative(),
});

export const authorizationCatalogVersionSchema = z.object({
  id: z.string().uuid(),
  version_num: z.number().int().positive(),
  draft_revision: z.number().int().positive().default(1),
  state: z.enum(["DRAFT", "VALIDATED", "PUBLISHED", "RETIRED"]),
  snapshot: z.object({
    schema_version: z.number().int(),
    entries: z.array(z.record(z.string(), z.unknown())),
    policy_grants: z.array(z.record(z.string(), z.unknown())).default([]),
  }),
  content_hash: z.array(z.number().int().min(0).max(255)).nullable().optional(),
  signing_key_id: z.string().nullable().optional(),
  created_by: z.string(),
  created_at: z.string(),
  validated_by: z.string().nullable().optional(),
  validated_at: z.string().nullable().optional(),
  published_by: z.string().nullable().optional(),
  published_at: z.string().nullable().optional(),
});
export type AuthorizationCatalogVersion = z.infer<typeof authorizationCatalogVersionSchema>;

export const catalogDraftResponseSchema = z.object({
  id: z.string().uuid(),
  version_num: z.number().int().positive(),
  draft_revision: z.number().int().positive().default(1),
  state: z.literal("DRAFT"),
});

export const catalogLifecycleResponseSchema = z.object({
  id: z.string().uuid(),
  state: z.enum(["VALIDATED", "PUBLISHED"]),
  version_num: z.number().int().positive().optional(),
  policy_version: z.string().optional(),
  bundle_sequence: z.number().int().positive().optional(),
  catalog_version: z.number().int().positive().optional(),
  signing_key_id: z.string().optional(),
});
