import { z } from "zod";

export const configurationScopeTypeSchema = z.enum([
  "PLATFORM",
  "ENVIRONMENT",
  "TENANT",
  "WORKFLOW_TYPE",
  "WORKFLOW_VERSION",
  "APPROVED_INSTANCE_OVERRIDE",
]);
export type ConfigurationScopeType = z.infer<typeof configurationScopeTypeSchema>;

export const configurationOwnerSchema = z.enum([
  "ENGINE",
  "API_GATEWAY",
  "HUMAN_RUNTIME",
  "PROJECTION",
  "GOVERNANCE",
]);
export type ConfigurationOwner = z.infer<typeof configurationOwnerSchema>;

export const configurationStatusSchema = z.enum(["DRAFT", "PUBLISHED", "RETIRED"]);
export type ConfigurationStatus = z.infer<typeof configurationStatusSchema>;

const configurationVersionSummarySchema = z.object({
  id: z.string().uuid(),
  ordinal: z.number().int().positive(),
  config_version: z.string(),
  policy_version: z.string(),
  schema_version: z.number().int().positive(),
  status: configurationStatusSchema,
  reason: z.string(),
  created_at: z.string(),
  created_by: z.string(),
});

export const configurationProfileSchema = z.object({
  id: z.string().uuid(),
  tenant_id: z.string(),
  owner: configurationOwnerSchema,
  name: z.string(),
  scope: z.object({
    type: configurationScopeTypeSchema,
    reference: z.string(),
  }),
  aggregate_version: z.number().int().positive(),
  current_published_version_id: z.string().uuid().optional(),
  is_deleted: z.boolean(),
  created_at: z.string(),
  updated_at: z.string(),
  latest: configurationVersionSummarySchema.optional(),
});
export type ConfigurationProfile = z.infer<typeof configurationProfileSchema>;

export const configurationPageSchema = z.object({
  profiles: z.array(configurationProfileSchema),
  next_page_token: z.string(),
});

export const configurationVersionSchema = configurationVersionSummarySchema.extend({
  values: z.record(z.string(), z.unknown()),
  content_hash: z.string(),
  published_at: z.string().nullable().optional(),
  published_by: z.string().optional(),
});
export type ConfigurationVersion = z.infer<typeof configurationVersionSchema>;

export const configurationDetailSchema = z.object({
  profile: configurationProfileSchema,
  versions: z.array(configurationVersionSchema),
});

export interface EngineConfigurationPolicy {
  snapshot_interval_events: number;
  max_events_per_decision: number;
  command_timeout_ms: string;
  event_payload_key_scope: string;
  optimistic_conflict_retry: {
    max_attempts: number;
    initial_backoff_ms: string;
    max_backoff_ms: string;
    multiplier_millis: number;
  };
  local_wasm: Record<string, number | string>;
  authorization_audit_key_scope: string;
  max_multi_instance_cardinality: number;
  default_multi_instance_parallelism: number;
  boundary_runtime: Record<string, number | string>;
}

export type ConfigurationPolicy = Record<string, unknown> | EngineConfigurationPolicy;

export interface CreateConfigurationInput {
  name: string;
  owner: ConfigurationOwner;
  scope: { type: ConfigurationScopeType; reference: string };
  schema_version: number;
  policy_version: string;
  reason: string;
  values: ConfigurationPolicy;
}

export interface DraftConfigurationInput {
  expected_version: number;
  schema_version: number;
  policy_version: string;
  reason: string;
  values: ConfigurationPolicy;
}
