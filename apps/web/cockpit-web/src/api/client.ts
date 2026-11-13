import type { RuntimeConfig } from "../config/runtime";
import { z } from "zod";
import {
  addNodeResponseSchema,
  createOrganizationResponseSchema,
  emptyResponseSchema,
  organizationDetailSchema,
  organizationListSchema,
  type AddOrganizationNodeInput,
  type CreateOrganizationInput,
  type MoveOrganizationNodeInput,
} from "../features/organizations/types";
import {
  configurationDetailSchema,
  configurationPageSchema,
  configurationProfileSchema,
  configurationVersionDiffSchema,
  type CreateConfigurationInput,
  type DraftConfigurationInput,
} from "../features/configuration/types";
import type {
  AuditPage,
  CaseResponse,
  CommandReceipt,
  CompleteWorkItemInput,
  ClaimWorkItemInput,
  DelegateWorkItemInput,
  UnclaimWorkItemInput,
  StartWorkflowInput,
  WorkItemPage,
  WorkItemListScope,
  WorkItemResponse,
  ProcessModelPage, ProcessModelDetail, ProcessModelDraft, CreateProcessModelInput,
  SaveProcessModelDraftInput, ValidateProcessModelDraftInput, CheckinProcessModelInput, ProcessModelVersion, ProcessModelVersionPage, CompilerDiagnostic,
  DecisionModelPage, DecisionModelDetail, DecisionModelDraft, CreateDecisionModelInput, SaveDecisionModelDraftInput, CheckinDecisionModelInput, DecisionModelVersion, DecisionModelVersionPage,
  FormPage, FormDefinition, FormDraft, FormDetail, PublishedForm, FormVersion, FormVersionPage, CreateFormInput, SaveFormDraftInput, PublishFormInput,
  CompletedProcessPage,
  CompletedProcessQueryInput,
  ProcessQueryInput,
  QueryCapabilities,
  AssignmentPolicyPage,
  UpsertAssignmentPolicyInput,
  IdentityUserPage,
  AggregateQueryInput, AggregatePage, EventQueryInput, EventPage, SavedQuery, SavedQueryPage, SavedDashboard, SavedDashboardPage, QueryExport, QueryPlan,
} from "./types";
import {
  authorizationCatalogEntrySchema,
  authorizationCatalogVersionSchema,
  assignmentGroupSchema,
  catalogAuditEntrySchema,
  catalogAcknowledgementSchema,
  catalogDraftResponseSchema,
  catalogLifecycleResponseSchema,
  type CatalogAuditEntry,
  type AuthorizationCatalogEntry,
  type AuthorizationCatalogVersion,
} from "../features/access/types";

export interface RequestIdentity {
  tenantId: string;
  accessToken: string;
}

const browserConfigurationSchema = z.object({
  config_version: z.string().min(1),
  policy_version: z.string().min(1),
  batch_chunk_size: z.number().int().positive(),
  batch_concurrency: z.number().int().positive(),
}).refine(
  (value) => value.batch_concurrency <= value.batch_chunk_size,
  "batch concurrency must not exceed chunk size",
);

export type BrowserConfiguration = z.infer<typeof browserConfigurationSchema>;

const problemDetailsSchema = z.object({
  title: z.string().optional(),
  detail: z.string().optional(),
  code: z.string().optional(),
  error: z.string().optional(),
});

const workflowInstanceSchema = z.object({
  tenant_id: z.string().optional(), instance_id: z.string().min(1), workflow_type: z.string().optional(),
  workflow_version: z.string().optional(), status: z.string().optional(), active_node_id: z.string().optional(),
  last_event_sequence: z.number().int().nonnegative().optional(), started_at_epoch_ms: z.number().int().nonnegative().optional(),
  updated_at_epoch_ms: z.number().int().nonnegative().optional(), completed_at_epoch_ms: z.number().int().nonnegative().optional(),
  config_version: z.string().optional(), policy_version: z.string().optional(), initiator_actor_id: z.string().optional(),
  is_deleted: z.boolean().optional(), version: z.number().int().nonnegative().optional(),
});
const assignmentPolicySchema = z.object({
  policy_ref: z.string().min(1), workflow_type: z.string().min(1), workflow_version: z.string().min(1),
  node_id: z.string().min(1), assignee_id: z.string().optional().default(""), candidate_group: z.string().optional().default(""),
  sla_duration_ms: z.number().int().nonnegative().optional().default(0), escalation_policy_ref: z.string().optional().default(""),
  config_version: z.string().min(1), version: z.number().int().positive(), is_deleted: z.boolean().optional().default(false),
});
const processModelSchema = z.object({
  id: z.string().uuid(), tenant_id: z.string().uuid(), model_key: z.string(), name: z.string(),
  category: z.string().optional().default(""), description: z.string().optional().default(""), status: z.string(),
  revision: z.number().int().positive(), latest_version_id: z.string().optional().default(""),
  latest_version_source_revision: z.number().int().nonnegative().optional().default(0),
  created_at_epoch_ms: z.number().int().nonnegative(), updated_at_epoch_ms: z.number().int().nonnegative(), is_deleted: z.boolean().optional().default(false),
});
const compilerDiagnosticSchema = z.object({code:z.string(),severity:z.string(),message:z.string(),element_id:z.string().optional().default(""),line:z.number().int().nonnegative().optional().default(0),column:z.number().int().nonnegative().optional().default(0)});
const processModelDraftSchema = z.object({process_model_id:z.string().uuid(),revision:z.number().int().positive(),base_version_id:z.string().optional().default(""),bpmn_xml:z.string(),validation_status:z.string(),diagnostics:z.array(compilerDiagnosticSchema).default([]),updated_at_epoch_ms:z.number().int().nonnegative()});
const processModelPageSchema = z.object({models:z.array(processModelSchema).default([]),next_page_token:z.string().optional().default("")});
const processModelDetailSchema = z.object({model:processModelSchema,draft:processModelDraftSchema});
const processVersionSummarySchema=z.object({id:z.string().uuid(),tenant_id:z.string().uuid(),process_model_id:z.string().uuid(),version_num:z.number().int().positive(),source_revision:z.number().int().positive(),created_by:z.string().uuid(),created_at_epoch_ms:z.number().int().nonnegative(),version_tag:z.string(),summary:z.string()});
const processVersionSchema=processVersionSummarySchema.extend({bpmn_xml:z.string(),content_hash:z.string()});
const processVersionPageSchema=z.object({versions:z.array(processVersionSummarySchema).default([]),next_page_token:z.string().optional().default("")});
const decisionModelSchema=z.object({id:z.string().uuid(),tenant_id:z.string().uuid(),model_key:z.string(),name:z.string(),description:z.string().optional().default(""),status:z.string(),revision:z.number().int().positive(),latest_version_source_revision:z.number().int().nonnegative().optional().default(0),created_at_epoch_ms:z.number().int().nonnegative(),updated_at_epoch_ms:z.number().int().nonnegative()});
const decisionModelDraftSchema=z.object({decision_model_id:z.string().uuid(),revision:z.number().int().positive(),dmn_xml:z.string(),validation_status:z.string(),diagnostics:z.array(compilerDiagnosticSchema).default([]),updated_at_epoch_ms:z.number().int().nonnegative()});
const decisionModelDetailSchema=z.object({model:decisionModelSchema,draft:decisionModelDraftSchema});
const decisionVersionSummarySchema=z.object({id:z.string().uuid(),tenant_id:z.string().uuid(),decision_model_id:z.string().uuid(),version_num:z.number().int().positive(),source_revision:z.number().int().positive(),created_by:z.string().uuid(),created_at_epoch_ms:z.number().int().nonnegative(),version_tag:z.string(),summary:z.string()});
const decisionVersionSchema=decisionVersionSummarySchema.extend({dmn_xml:z.string(),content_hash:z.string()});
const decisionVersionPageSchema=z.object({versions:z.array(decisionVersionSummarySchema).default([]),next_page_token:z.string().optional().default("")});
const formDefinitionSchema=z.object({id:z.string().uuid(),tenant_id:z.string().uuid(),form_key:z.string(),name:z.string(),revision:z.number().int().positive(),status:z.string(),latest_version_id:z.string().optional().default(""),updated_at_epoch_ms:z.number().int().nonnegative()});
const formDraftSchema=z.object({form_id:z.string().uuid(),revision:z.number().int().positive(),schema_json:z.string(),ui_schema_json:z.string().optional().default("{}"),validation_status:z.string(),diagnostics:z.array(compilerDiagnosticSchema).default([]),updated_at_epoch_ms:z.number().int().nonnegative()});
const formVersionSummarySchema=z.object({id:z.string().uuid(),tenant_id:z.string().uuid(),form_id:z.string().uuid(),version_num:z.number().int().positive(),source_revision:z.number().int().positive(),created_by:z.string().uuid(),created_at_epoch_ms:z.number().int().nonnegative(),version_tag:z.string(),summary:z.string()});
const formVersionSchema=formVersionSummarySchema.extend({schema_json:z.string(),ui_schema_json:z.string().optional().default("{}"),content_hash:z.string()});
const formPageSchema=z.object({forms:z.array(formDefinitionSchema).default([]),next_page_token:z.string().optional().default("")});
const formDetailSchema=z.object({form:formDefinitionSchema,draft:formDraftSchema});
const publishedFormSchema=z.object({form:formDefinitionSchema,published_version:formVersionSchema});
const formVersionPageSchema=z.object({versions:z.array(formVersionSummarySchema).default([]),next_page_token:z.string().optional().default("")});
const processFieldSchema = z.enum([
  "PROCESS_FIELD_INSTANCE_ID", "PROCESS_FIELD_WORKFLOW_TYPE", "PROCESS_FIELD_WORKFLOW_VERSION", "PROCESS_FIELD_STATUS",
  "PROCESS_FIELD_ACTIVE_NODE_ID", "PROCESS_FIELD_LAST_EVENT_SEQUENCE", "PROCESS_FIELD_STARTED_AT", "PROCESS_FIELD_UPDATED_AT",
  "PROCESS_FIELD_COMPLETED_AT", "PROCESS_FIELD_DURATION_MS", "PROCESS_FIELD_CONFIG_VERSION", "PROCESS_FIELD_POLICY_VERSION",
  "PROCESS_FIELD_VERSION", "PROCESS_FIELD_INITIATOR_ACTOR_ID", "PROCESS_FIELD_EVENT_COUNT", "PROCESS_FIELD_IS_DELETED",
]);
const eventFieldSchema = z.enum(["EVENT_FIELD_EVENT_TYPE", "EVENT_FIELD_NODE_ID", "EVENT_FIELD_ACTOR_ID", "EVENT_FIELD_SEQUENCE", "EVENT_FIELD_OCCURRED_AT", "EVENT_FIELD_CONFIG_VERSION", "EVENT_FIELD_POLICY_VERSION"]);
const comparisonOperatorSchema = z.enum([
  "COMPARISON_OPERATOR_EQUAL", "COMPARISON_OPERATOR_NOT_EQUAL", "COMPARISON_OPERATOR_GREATER_THAN", "COMPARISON_OPERATOR_GREATER_THAN_OR_EQUAL",
  "COMPARISON_OPERATOR_LESS_THAN", "COMPARISON_OPERATOR_LESS_THAN_OR_EQUAL", "COMPARISON_OPERATOR_IN", "COMPARISON_OPERATOR_NOT_IN",
  "COMPARISON_OPERATOR_CONTAINS", "COMPARISON_OPERATOR_STARTS_WITH", "COMPARISON_OPERATOR_ENDS_WITH", "COMPARISON_OPERATOR_IS_NULL",
  "COMPARISON_OPERATOR_IS_NOT_NULL", "COMPARISON_OPERATOR_BETWEEN", "COMPARISON_OPERATOR_NOT_BETWEEN",
]);
const textTransformSchema = z.enum(["TEXT_TRANSFORM_NONE", "TEXT_TRANSFORM_TRIM", "TEXT_TRANSFORM_LOWER", "TEXT_TRANSFORM_UPPER", "TEXT_TRANSFORM_TRIM_LOWER", "TEXT_TRANSFORM_TRIM_UPPER"]);
const numericTransformSchema = z.enum(["NUMERIC_TRANSFORM_NONE", "NUMERIC_TRANSFORM_ABS"]);
const dateTransformSchema = z.enum(["DATE_TRANSFORM_NONE", "DATE_TRANSFORM_UTC_DAY", "DATE_TRANSFORM_UTC_WEEK", "DATE_TRANSFORM_UTC_MONTH", "DATE_TRANSFORM_UTC_YEAR"]);
const processSortSchema = z.object({
  field: processFieldSchema,
  direction: z.enum(["SORT_DIRECTION_ASCENDING", "SORT_DIRECTION_DESCENDING"]),
  text_transform: textTransformSchema.optional(), numeric_transform: numericTransformSchema.optional(),
  date_transform: dateTransformSchema.optional(), coalesce_value: z.record(z.string(), z.unknown()).optional(),
});
const completedProcessPageSchema = z.object({
  processes: z.array(z.object({
    instance: workflowInstanceSchema,
    duration_ms: z.number().int().nonnegative().optional(),
    event_count: z.number().int().nonnegative().optional(),
    masked_fields: z.array(z.string()).optional(),
  })).default([]),
  next_page_token: z.string().default(""),
  normalized_sort: z.array(processSortSchema).optional(),
  policy_version: z.string().optional(),
  query_fingerprint: z.string().optional(),
});
const queryValueSchema = z.union([z.object({string_value:z.string()}),z.object({integer_value:z.number().int()}),z.object({boolean_value:z.boolean()}),z.object({timestamp_epoch_ms:z.number().int().nonnegative()}),z.object({decimal_value:z.string().regex(/^-?\d+(\.\d+)?$/)})]);
const eventSchema=z.object({instance_id:z.string().optional(),sequence:z.number().int().nonnegative().optional(),event_type:z.string().optional(),node_id:z.string().optional(),actor_id:z.string().optional(),occurred_at_epoch_ms:z.number().int().nonnegative().optional(),config_version:z.string().optional(),policy_version:z.string().optional(),is_deleted:z.boolean().optional(),masked_fields:z.array(z.string()).optional()});
const eventPageSchema=z.object({events:z.array(eventSchema).default([]),next_page_token:z.string().default(""),policy_version:z.string().optional()});
const aggregatePageSchema=z.object({rows:z.array(z.object({dimensions:z.array(z.object({name:z.string(),value:queryValueSchema.optional()})).default([]),metrics:z.array(z.object({name:z.string(),value:queryValueSchema.optional()})).default([])})).default([]),policy_version:z.string().optional()});
const savedQuerySchema=z.object({id:z.string().min(1),name:z.string().min(1),version:z.number().int().positive(),query:z.record(z.string(),z.unknown()),created_at_epoch_ms:z.number().int().nonnegative(),updated_at_epoch_ms:z.number().int().nonnegative()});
const savedQueryPageSchema=z.object({saved_queries:z.array(savedQuerySchema).default([]),next_page_token:z.string().default("")});
const savedDashboardSchema=z.object({id:z.string().min(1),name:z.string().min(1),version:z.number().int().positive(),config_json:z.string().min(1),created_at_epoch_ms:z.number().int().nonnegative(),updated_at_epoch_ms:z.number().int().nonnegative()});
const savedDashboardPageSchema=z.object({dashboards:z.array(savedDashboardSchema).default([]),next_page_token:z.string().default("")});
const identityUserPageSchema=z.object({users:z.array(z.object({user_id:z.string().uuid(),username:z.string().min(1),external_id:z.string().optional(),is_active:z.boolean(),query_revoked:z.boolean(),attributes_version:z.number().int().nonnegative(),roles:z.array(z.string()).default([]),version:z.number().int().nonnegative(),created_at_epoch_ms:z.number().int().nonnegative(),updated_at_epoch_ms:z.number().int().nonnegative()})).default([]),next_page_token:z.string().default("")});
const queryExportSchema=z.object({id:z.string().min(1),status:z.enum(["QUERY_EXPORT_STATUS_PENDING","QUERY_EXPORT_STATUS_COMPLETED","QUERY_EXPORT_STATUS_FAILED","QUERY_EXPORT_STATUS_CANCELLED"]),row_count:z.number().int().nonnegative().optional(),csv:z.string().optional().transform(decodeBase64Utf8),error_code:z.string().optional(),created_at_epoch_ms:z.number().int().nonnegative(),expires_at_epoch_ms:z.number().int().nonnegative()});

function decodeBase64Utf8(value?: string): string | undefined {
  if (value === undefined || value === "") return value;
  try {
    const binary = atob(value);
    return new TextDecoder().decode(Uint8Array.from(binary, (character) => character.charCodeAt(0)));
  } catch {
    throw new Error("Invalid base64 CSV payload");
  }
}
const queryPlanSchema=z.object({redacted_plan_json:z.string(),query_fingerprint:z.string().min(1)});
const queryCapabilitiesSchema = z.object({
  schema_version: z.string().min(1), config_version: z.string().min(1), policy_version: z.string().min(1),
  fields: z.array(z.object({ field: processFieldSchema, value_kind: z.string(), filterable: z.boolean(), sortable: z.boolean(), selectable: z.boolean(), operators: z.array(comparisonOperatorSchema).default([]), text_transforms: z.array(textTransformSchema).optional(), numeric_transforms: z.array(numericTransformSchema).optional(), date_transforms: z.array(dateTransformSchema).optional() })),
  event_fields: z.array(z.object({ field: eventFieldSchema, value_kind: z.string(), operators: z.array(comparisonOperatorSchema).default([]), text_transforms: z.array(textTransformSchema).optional(), numeric_transforms: z.array(numericTransformSchema).optional(), date_transforms: z.array(dateTransformSchema).optional() })),
  default_page_size: z.number().int().positive(), max_page_size: z.number().int().positive(), max_filter_depth: z.number().int().positive(),
  max_predicates: z.number().int().positive(), max_sort_fields: z.number().int().positive(), max_relation_joins: z.number().int().nonnegative(),
  max_in_values: z.number().int().positive(), max_string_bytes: z.number().int().positive(), scopes: z.array(z.string()),
  default_sort: z.array(processSortSchema),
});

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly correlationId: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class BpmpApiClient {
  constructor(
    private readonly config: RuntimeConfig,
    private readonly getIdentity: () => RequestIdentity,
  ) {}

  async getBrowserConfiguration(): Promise<BrowserConfiguration> {
    const raw = await this.request<unknown>("/v1/runtime/browser-configuration");
    const parsed = browserConfigurationSchema.safeParse(raw);
    if (!parsed.success) {
      throw new ApiError("Invalid browser configuration", 502, "");
    }
    return parsed.data;
  }

  async listProcessModels(search = "", pageToken = "", pageSize = this.config.defaultPageSize): Promise<ProcessModelPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (search) query.set("search", search);
    if (pageToken) query.set("page_token", pageToken);
    const parsed = processModelPageSchema.safeParse(await this.request<unknown>(`/v1/authoring/process-models?${query.toString()}`));
    if (!parsed.success) throw new ApiError("Invalid process model page", 502, "");
    return parsed.data as ProcessModelPage;
  }

  async createProcessModel(input: CreateProcessModelInput, idempotencyKey?: string): Promise<ProcessModelDetail> {
    const parsed = processModelDetailSchema.safeParse(await this.request<unknown>("/v1/authoring/process-models", {
      method: "POST", body: input, ...(idempotencyKey ? { idempotencyKey } : {}),
    }));
    if (!parsed.success) throw new ApiError("Invalid process model response", 502, "");
    return parsed.data as ProcessModelDetail;
  }

  async getProcessModel(modelId: string): Promise<ProcessModelDetail> {
    const parsed = processModelDetailSchema.safeParse(await this.request<unknown>(`/v1/authoring/process-models/${encodeURIComponent(modelId)}`));
    if (!parsed.success) throw new ApiError("Invalid process model response", 502, "");
    return parsed.data as ProcessModelDetail;
  }

  async saveProcessModelDraft(modelId: string, input: SaveProcessModelDraftInput, idempotencyKey?: string): Promise<{ draft: ProcessModelDraft; replayed: boolean }> {
    const schema = z.object({ draft: processModelDraftSchema, replayed: z.boolean().optional().default(false) });
    const parsed = schema.safeParse(await this.request<unknown>(`/v1/authoring/process-models/${encodeURIComponent(modelId)}/draft`, {
      method: "PUT", body: input, ...(idempotencyKey ? { idempotencyKey } : {}),
    }));
    if (!parsed.success) throw new ApiError("Invalid saved draft response", 502, "");
    return parsed.data as { draft: ProcessModelDraft; replayed: boolean };
  }

  async validateProcessModelDraft(modelId: string, input: ValidateProcessModelDraftInput, idempotencyKey?: string): Promise<{ process_model_id: string; revision: number; valid: boolean; diagnostics: CompilerDiagnostic[] }> {
    const schema = z.object({ process_model_id: z.string().uuid(), revision: z.number().int().positive(), valid: z.boolean().optional().default(false), diagnostics: z.array(compilerDiagnosticSchema).default([]) });
    const parsed = schema.safeParse(await this.request<unknown>(`/v1/authoring/process-models/${encodeURIComponent(modelId)}/draft/validate`, {
      method: "POST", body: input, ...(idempotencyKey ? { idempotencyKey } : {}),
    }));
    if (!parsed.success) throw new ApiError("Invalid validation response", 502, "");
    return parsed.data;
  }

  async checkinProcessModel(modelId: string, input: CheckinProcessModelInput, idempotencyKey?: string): Promise<{ version: ProcessModelVersion; replayed: boolean }> {
    const schema = z.object({ version: z.object({ id: z.string().uuid(), tenant_id: z.string().uuid(), process_model_id: z.string().uuid(), version_num: z.number().int().positive(), source_revision: z.number().int().positive(), bpmn_xml: z.string(), content_hash: z.string(), created_by: z.string().uuid(), created_at_epoch_ms: z.number().int().nonnegative(), version_tag: z.string(), summary: z.string() }), replayed: z.boolean().optional().default(false) });
    const parsed = schema.safeParse(await this.request<unknown>(`/v1/authoring/process-models/${encodeURIComponent(modelId)}/versions`, { method: "POST", body: input, ...(idempotencyKey ? { idempotencyKey } : {}) }));
    if (!parsed.success) throw new ApiError("Invalid process model version response", 502, "");
    return parsed.data;
  }
  async listProcessModelVersions(modelId:string,pageToken="",pageSize=this.config.defaultPageSize):Promise<ProcessModelVersionPage>{const query=new URLSearchParams({page_size:String(pageSize)});if(pageToken)query.set("page_token",pageToken);const parsed=processVersionPageSchema.safeParse(await this.request<unknown>(`/v1/authoring/process-models/${encodeURIComponent(modelId)}/versions?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid process version page",502,"");return parsed.data;}
  async getProcessModelVersion(modelId:string,versionId:string):Promise<ProcessModelVersion>{const parsed=z.object({version:processVersionSchema}).safeParse(await this.request<unknown>(`/v1/authoring/process-models/${encodeURIComponent(modelId)}/versions/${encodeURIComponent(versionId)}`));if(!parsed.success)throw new ApiError("Invalid process version response",502,"");return parsed.data.version;}

  async listDecisionModels(search = "", pageToken = "", pageSize = this.config.defaultPageSize): Promise<DecisionModelPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) }); if (search) query.set("search",search); if (pageToken) query.set("page_token",pageToken);
    const schema=z.object({models:z.array(decisionModelSchema).default([]),next_page_token:z.string().optional().default("")});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/decision-models?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid decision model page",502,"");return parsed.data;
  }

  async createDecisionModel(input: CreateDecisionModelInput,idempotencyKey?:string):Promise<DecisionModelDetail>{const parsed=decisionModelDetailSchema.safeParse(await this.request<unknown>("/v1/authoring/decision-models",{method:"POST",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid decision model response",502,"");return parsed.data;}
  async getDecisionModel(modelId:string):Promise<DecisionModelDetail>{const parsed=decisionModelDetailSchema.safeParse(await this.request<unknown>(`/v1/authoring/decision-models/${encodeURIComponent(modelId)}`));if(!parsed.success)throw new ApiError("Invalid decision model response",502,"");return parsed.data;}
  async saveDecisionModelDraft(modelId:string,input:SaveDecisionModelDraftInput,idempotencyKey?:string):Promise<{draft:DecisionModelDraft;replayed:boolean}>{const schema=z.object({draft:decisionModelDraftSchema,replayed:z.boolean().optional().default(false)});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/decision-models/${encodeURIComponent(modelId)}/draft`,{method:"PUT",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid saved decision draft response",502,"");return parsed.data;}
  async validateDecisionModelDraft(modelId:string,input:ValidateProcessModelDraftInput,idempotencyKey?:string):Promise<{decision_model_id:string;revision:number;valid:boolean;diagnostics:CompilerDiagnostic[]}>{const schema=z.object({decision_model_id:z.string().uuid(),revision:z.number().int().positive(),valid:z.boolean().optional().default(false),diagnostics:z.array(compilerDiagnosticSchema).default([])});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/decision-models/${encodeURIComponent(modelId)}/draft/validate`,{method:"POST",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid decision validation response",502,"");return parsed.data;}
  async checkinDecisionModel(modelId:string,input:CheckinDecisionModelInput,idempotencyKey?:string):Promise<{version:DecisionModelVersion;replayed:boolean}>{const schema=z.object({version:z.object({id:z.string().uuid(),tenant_id:z.string().uuid(),decision_model_id:z.string().uuid(),version_num:z.number().int().positive(),source_revision:z.number().int().positive(),dmn_xml:z.string(),content_hash:z.string(),created_by:z.string().uuid(),created_at_epoch_ms:z.number().int().nonnegative(),version_tag:z.string(),summary:z.string()}),replayed:z.boolean().optional().default(false)});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/decision-models/${encodeURIComponent(modelId)}/versions`,{method:"POST",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid decision version response",502,"");return parsed.data;}
  async listDecisionModelVersions(modelId:string,pageToken="",pageSize=this.config.defaultPageSize):Promise<DecisionModelVersionPage>{const query=new URLSearchParams({page_size:String(pageSize)});if(pageToken)query.set("page_token",pageToken);const parsed=decisionVersionPageSchema.safeParse(await this.request<unknown>(`/v1/authoring/decision-models/${encodeURIComponent(modelId)}/versions?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid decision version page",502,"");return parsed.data;}
  async getDecisionModelVersion(modelId:string,versionId:string):Promise<DecisionModelVersion>{const parsed=z.object({version:decisionVersionSchema}).safeParse(await this.request<unknown>(`/v1/authoring/decision-models/${encodeURIComponent(modelId)}/versions/${encodeURIComponent(versionId)}`));if(!parsed.success)throw new ApiError("Invalid decision version response",502,"");return parsed.data.version;}

  async listForms(search="",pageToken="",pageSize=this.config.defaultPageSize):Promise<FormPage>{const query=new URLSearchParams({page_size:String(pageSize)});if(search)query.set("search",search);if(pageToken)query.set("page_token",pageToken);const parsed=formPageSchema.safeParse(await this.request<unknown>(`/v1/authoring/forms?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid form page",502,"");return parsed.data;}
  async createForm(input:CreateFormInput,idempotencyKey?:string):Promise<{form:FormDefinition;draft:FormDraft;replayed:boolean}>{const schema=z.object({form:formDefinitionSchema,draft:formDraftSchema,replayed:z.boolean().optional().default(false)});const parsed=schema.safeParse(await this.request<unknown>("/v1/authoring/forms",{method:"POST",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid form creation response",502,"");return parsed.data;}
  async getForm(formId:string):Promise<FormDetail>{const parsed=formDetailSchema.safeParse(await this.request<unknown>(`/v1/authoring/forms/${encodeURIComponent(formId)}`));if(!parsed.success)throw new ApiError("Invalid form detail",502,"");return parsed.data;}
  async getPublishedFormByKey(formKey:string):Promise<PublishedForm>{const parsed=publishedFormSchema.safeParse(await this.request<unknown>(`/v1/authoring/forms/by-key/${encodeURIComponent(formKey)}`));if(!parsed.success)throw new ApiError("Invalid published form",502,"");return parsed.data;}
  async saveFormDraft(formId:string,input:SaveFormDraftInput,idempotencyKey?:string):Promise<{draft:FormDraft;replayed:boolean}>{const schema=z.object({draft:formDraftSchema,replayed:z.boolean().optional().default(false)});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/forms/${encodeURIComponent(formId)}/draft`,{method:"PUT",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid saved form draft",502,"");return parsed.data;}
  async validateFormDraft(formId:string,expectedRevision:number):Promise<{form_id:string;revision:number;valid:boolean;diagnostics:CompilerDiagnostic[]}>{const schema=z.object({form_id:z.string().uuid(),revision:z.number().int().positive(),valid:z.boolean(),diagnostics:z.array(compilerDiagnosticSchema).default([])});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/forms/${encodeURIComponent(formId)}/draft/validate`,{method:"POST",body:{expected_revision:expectedRevision}}));if(!parsed.success)throw new ApiError("Invalid form validation response",502,"");return parsed.data;}
  async publishForm(formId:string,input:PublishFormInput,idempotencyKey?:string):Promise<{version:FormVersion;replayed:boolean}>{const schema=z.object({version:formVersionSchema,replayed:z.boolean().optional().default(false)});const parsed=schema.safeParse(await this.request<unknown>(`/v1/authoring/forms/${encodeURIComponent(formId)}/versions`,{method:"POST",body:input,...(idempotencyKey?{idempotencyKey}:{})}));if(!parsed.success)throw new ApiError("Invalid published form version",502,"");return parsed.data;}
  async listFormVersions(formId:string,pageToken="",pageSize=this.config.defaultPageSize):Promise<FormVersionPage>{const query=new URLSearchParams({page_size:String(pageSize)});if(pageToken)query.set("page_token",pageToken);const parsed=formVersionPageSchema.safeParse(await this.request<unknown>(`/v1/authoring/forms/${encodeURIComponent(formId)}/versions?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid form version page",502,"");return parsed.data;}
  async getFormVersion(formId:string,versionId:string):Promise<FormVersion>{const parsed=z.object({version:formVersionSchema}).safeParse(await this.request<unknown>(`/v1/authoring/forms/${encodeURIComponent(formId)}/versions/${encodeURIComponent(versionId)}`));if(!parsed.success)throw new ApiError("Invalid form version",502,"");return parsed.data.version;}

  async listWorkItems(pageToken = "", pageSize = this.config.defaultPageSize, scope: WorkItemListScope = "ALL"): Promise<WorkItemPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (pageToken) query.set("page_token", pageToken);
    query.set("scope", scope === "ASSIGNED_TO_ME" ? "mine" : scope === "GROUP_QUEUE" ? "group" : "all");
    const response = await this.request<Partial<WorkItemPage>>(
      `/v1/work-items?${query.toString()}`,
    );
    return {
      // Protobuf omits an empty repeated field, which produces `{}` rather than
      // `{ work_items: [] }` on the current gateway. Keep the UI's list contract stable.
      work_items: Array.isArray(response.work_items) ? response.work_items : [],
      next_page_token:
        typeof response.next_page_token === "string" ? response.next_page_token : "",
    };
  }

  getWorkItem(workItemId: string): Promise<WorkItemResponse> {
    return this.request(`/v1/work-items/${encodeURIComponent(workItemId)}`);
  }

  completeWorkItem(input: CompleteWorkItemInput): Promise<CommandReceipt> {
    return this.request(`/v1/work-items/${encodeURIComponent(input.workItemId)}/complete`, {
        method: "POST",
        body: {
          decision: input.decision,
          expected_version: input.expectedVersion,
          ...(input.outputs ? { outputs: input.outputs } : {}),
          ...(input.formVersionId ? { form_version_id: input.formVersionId } : {}),
        },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      });
  }

  claimWorkItem(input: ClaimWorkItemInput): Promise<CommandReceipt> {
    return this.request(`/v1/work-items/${encodeURIComponent(input.workItemId)}/claim`, {
      method: "POST",
      body: { expected_version: input.expectedVersion },
      ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
    });
  }

  unclaimWorkItem(input: UnclaimWorkItemInput): Promise<CommandReceipt> {
    return this.request(`/v1/work-items/${encodeURIComponent(input.workItemId)}/unclaim`, {
      method: "POST",
      body: { expected_version: input.expectedVersion, candidate_group: input.candidateGroup },
      ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
    });
  }

  delegateWorkItem(input: DelegateWorkItemInput): Promise<CommandReceipt> {
    return this.request(`/v1/work-items/${encodeURIComponent(input.workItemId)}/delegate`, {
        method: "POST",
        body: {
          expected_version: input.expectedVersion,
          assignee_id: input.assigneeId ?? "",
          candidate_group: input.candidateGroup ?? "",
        },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      });
  }

  startWorkflow(input: StartWorkflowInput): Promise<CommandReceipt> {
    return this.request(
      `/v1/workflows/${encodeURIComponent(input.workflowType)}/instances`,
      {
        method: "POST",
        body: {
          instance_id: input.instanceId,
          workflow_version: input.workflowVersion,
          start_node_id: input.startNodeId,
          ...(input.variables ? { variables: input.variables } : {}),
        },
      },
    );
  }

  getCase(caseId: string): Promise<CaseResponse> {
    return this.request(`/v1/cases/${encodeURIComponent(caseId)}`);
  }

  listAuditRecords(
    filters: { workItemId?: string; caseId?: string },
    pageToken = "",
    pageSize = this.config.defaultPageSize,
  ): Promise<AuditPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (filters.workItemId) query.set("work_item_id", filters.workItemId);
    if (filters.caseId) query.set("case_id", filters.caseId);
    if (pageToken) query.set("page_token", pageToken);
    return this.request(`/v1/audit-records?${query.toString()}`);
  }

  async queryCompletedProcesses(input: CompletedProcessQueryInput): Promise<CompletedProcessPage> {
    const response = await this.request<unknown>(
      "/v1/process-instances/completed/query",
      { method: "POST", body: input },
    );
    const parsed = completedProcessPageSchema.safeParse(response);
    if (!parsed.success) throw new ApiError("Invalid process query response", 502, "");
    return parsed.data as unknown as CompletedProcessPage;
  }

  async queryProcesses(input: ProcessQueryInput): Promise<CompletedProcessPage> {
    const response = await this.request<unknown>("/v1/process-instances/query", { method: "POST", body: input });
    const parsed = completedProcessPageSchema.safeParse(response);
    if (!parsed.success) throw new ApiError("Invalid process query response", 502, "");
    return parsed.data as unknown as CompletedProcessPage;
  }

  async getProcessQueryCapabilities(): Promise<QueryCapabilities> {
    const response = await this.request<unknown>("/v1/process-instances/query-capabilities");
    const parsed = queryCapabilitiesSchema.safeParse(response);
    if (!parsed.success) throw new ApiError("Invalid process query capabilities", 502, "");
    return parsed.data as unknown as QueryCapabilities;
  }

  async aggregateProcesses(input: AggregateQueryInput): Promise<AggregatePage> { const parsed=aggregatePageSchema.safeParse(await this.request<unknown>("/v1/process-instances/aggregate",{method:"POST",body:input}));if(!parsed.success)throw new ApiError("Invalid aggregate response",502,"");return parsed.data as AggregatePage; }
  async queryEvents(input: EventQueryInput): Promise<EventPage> { const parsed=eventPageSchema.safeParse(await this.request<unknown>("/v1/process-events/query",{method:"POST",body:input}));if(!parsed.success)throw new ApiError("Invalid event query response",502,"");return parsed.data as EventPage; }
  async saveQuery(input:{id:string;name:string;expected_version?:number;query:ProcessQueryInput}):Promise<SavedQuery>{const raw=await this.request<unknown>("/v1/saved-queries",{method:"POST",body:input});const parsed=z.object({saved_query:savedQuerySchema}).safeParse(raw);if(!parsed.success)throw new ApiError("Invalid saved query response",502,"");return parsed.data.saved_query as unknown as SavedQuery;}
  async listSavedQueries(pageToken="",pageSize=this.config.defaultPageSize):Promise<SavedQueryPage>{const query=new URLSearchParams({page_size:String(pageSize)});if(pageToken)query.set("page_token",pageToken);const parsed=savedQueryPageSchema.safeParse(await this.request<unknown>(`/v1/saved-queries?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid saved query page",502,"");return parsed.data as unknown as SavedQueryPage;}
  async deleteSavedQuery(id:string,expectedVersion:number):Promise<void>{await this.request(`/v1/saved-queries/${encodeURIComponent(id)}?expected_version=${expectedVersion}`,{method:"DELETE"});}
  async saveDashboard(input:{id:string;name:string;expected_version?:number;config_json:string}):Promise<SavedDashboard>{const raw=await this.request<unknown>("/v1/saved-dashboards",{method:"POST",body:input});const parsed=z.object({dashboard:savedDashboardSchema}).safeParse(raw);if(!parsed.success)throw new ApiError("Invalid dashboard response",502,"");return parsed.data.dashboard as SavedDashboard;}
  async listDashboards(pageToken="",pageSize=this.config.defaultPageSize):Promise<SavedDashboardPage>{const query=new URLSearchParams({page_size:String(pageSize)});if(pageToken)query.set("page_token",pageToken);const parsed=savedDashboardPageSchema.safeParse(await this.request<unknown>(`/v1/saved-dashboards?${query.toString()}`));if(!parsed.success)throw new ApiError("Invalid dashboard page",502,"");return parsed.data as SavedDashboardPage;}
  async deleteDashboard(id:string,expectedVersion:number):Promise<void>{await this.request(`/v1/saved-dashboards/${encodeURIComponent(id)}?expected_version=${expectedVersion}`,{method:"DELETE"});}
  async createQueryExport(query:ProcessQueryInput,maxRows=10000):Promise<QueryExport>{const raw=await this.request<unknown>("/v1/query-exports",{method:"POST",body:{query,max_rows:maxRows}});const parsed=z.object({export:queryExportSchema}).safeParse(raw);if(!parsed.success)throw new ApiError("Invalid query export response",502,"");return parsed.data.export as QueryExport;}
  async getQueryExport(id:string):Promise<QueryExport>{const raw=await this.request<unknown>(`/v1/query-exports/${encodeURIComponent(id)}`);const parsed=z.object({export:queryExportSchema}).safeParse(raw);if(!parsed.success)throw new ApiError("Invalid query export response",502,"");return parsed.data.export as QueryExport;}
  async cancelQueryExport(id:string):Promise<QueryExport>{const raw=await this.request<unknown>(`/v1/query-exports/${encodeURIComponent(id)}`,{method:"DELETE"});const parsed=z.object({export:queryExportSchema}).safeParse(raw);if(!parsed.success)throw new ApiError("Invalid query export response",502,"");return parsed.data.export as QueryExport;}
  async explainQueryPlan(query:ProcessQueryInput,analyze=false):Promise<QueryPlan>{const parsed=queryPlanSchema.safeParse(await this.request<unknown>("/v1/ops/query-plan",{method:"POST",body:{query,analyze}}));if(!parsed.success)throw new ApiError("Invalid query plan response",502,"");return parsed.data;}

  async listAuthorizationCatalogEntries(): Promise<AuthorizationCatalogEntry[]> {
    const raw = await this.request<unknown>("/v1/access/catalog/entries");
    const parsed = z.array(authorizationCatalogEntrySchema).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization catalog entries", 502, "");
    return parsed.data;
  }

  async listAuthorizationRoles(): Promise<Array<{ code: string; name: string }>> {
    const parsed = z.array(z.object({ code: z.string().min(1), name: z.string().min(1) })).safeParse(
      await this.request<unknown>("/v1/access/catalog/roles"),
    );
    if (!parsed.success) throw new ApiError("Invalid authorization role catalog", 502, "");
    return parsed.data;
  }

  async listIdentityUsers(afterUserId = "", limit = this.config.defaultPageSize): Promise<IdentityUserPage> {
    const query = new URLSearchParams({ limit: String(limit) });
    if (afterUserId) query.set("after_user_id", afterUserId);
    const parsed = identityUserPageSchema.safeParse(await this.request<unknown>(`/v1/access/users?${query.toString()}`));
    if (!parsed.success) throw new ApiError("Invalid tenant user directory response", 502, "");
    return parsed.data;
  }

  async listAuthorizationCatalogVersions(): Promise<AuthorizationCatalogVersion[]> {
    const raw = await this.request<unknown>("/v1/access/catalog/versions");
    const parsed = z.array(authorizationCatalogVersionSchema).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization catalog versions", 502, "");
    return parsed.data;
  }

  async listAuthorizationCatalogAudit(limit = 100): Promise<CatalogAuditEntry[]> {
    const query = new URLSearchParams({ limit: String(Math.min(Math.max(limit, 1), 200)) });
    const raw = await this.request<unknown>(`/v1/access/catalog/audit?${query.toString()}`);
    const parsed = z.array(catalogAuditEntrySchema).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization catalog audit response", 502, "");
    return parsed.data;
  }

  async listAuthorizationCatalogAcknowledgements(catalogVersionId: string) {
    const query = new URLSearchParams({ catalog_version_id: catalogVersionId, limit: "500" });
    const raw = await this.request<unknown>(`/v1/access/catalog/acknowledgements?${query.toString()}`);
    const parsed = z.array(catalogAcknowledgementSchema).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization bundle acknowledgement response", 502, "");
    return parsed.data;
  }

  async listAccessQueues() {
    const raw = await this.request<unknown>("/v1/access/queues");
    const parsed = z.object({ groups: z.array(assignmentGroupSchema) }).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid assignment group response", 502, "");
    return parsed.data.groups;
  }

  async listAssignmentPolicies(pageToken = "", pageSize = this.config.defaultPageSize): Promise<AssignmentPolicyPage> {
    const query = new URLSearchParams({ page_size: String(pageSize) });
    if (pageToken) query.set("page_token", pageToken);
    const raw = await this.request<unknown>(`/v1/assignment-policies?${query.toString()}`);
    const parsed = z.object({ policies: z.array(assignmentPolicySchema).default([]), next_page_token: z.string().default("") }).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid assignment policy response", 502, "");
    return parsed.data;
  }

  async upsertAssignmentPolicy(input: UpsertAssignmentPolicyInput, idempotencyKey?: string) {
    const raw = await this.request<unknown>("/v1/assignment-policies", { method: "PUT", body: input, ...(idempotencyKey ? { idempotencyKey } : {}) });
    const parsed = z.object({ policy: assignmentPolicySchema, duplicate: z.boolean().default(false) }).safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid assignment policy mutation response", 502, "");
    return parsed.data;
  }

  async createAuthorizationCatalogDraft(snapshot: Record<string, unknown>, reason: string) {
    const raw = await this.request<unknown>("/v1/access/catalog/versions", {
      method: "POST", body: { snapshot, reason },
    });
    const parsed = catalogDraftResponseSchema.safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization draft response", 502, "");
    return parsed.data;
  }

  async updateAuthorizationCatalogDraft(id: string, expectedDraftRevision: number, snapshot: Record<string, unknown>, reason: string) {
    const raw = await this.request<unknown>(
      `/v1/access/catalog/versions/${encodeURIComponent(id)}`,
      { method: "PUT", body: { expected_draft_revision: expectedDraftRevision, snapshot, reason } },
    );
    const parsed = catalogDraftResponseSchema.safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization draft response", 502, "");
    return parsed.data;
  }

  async validateAuthorizationCatalog(id: string) {
    const raw = await this.request<unknown>(
      `/v1/access/catalog/versions/${encodeURIComponent(id)}/validate`,
      { method: "POST", body: {} },
    );
    const parsed = catalogLifecycleResponseSchema.safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization validation response", 502, "");
    return parsed.data;
  }

  async publishAuthorizationCatalog(id: string, expectedVersionNum: number, reason: string) {
    const raw = await this.request<unknown>(
      `/v1/access/catalog/versions/${encodeURIComponent(id)}/publish`,
      { method: "POST", body: { expected_version_num: expectedVersionNum, reason } },
    );
    const parsed = catalogLifecycleResponseSchema.safeParse(raw);
    if (!parsed.success) throw new ApiError("Invalid authorization publication response", 502, "");
    return parsed.data;
  }

  listOrganizations(offset = 0, limit = this.config.defaultPageSize) {
    const query = new URLSearchParams({
      offset: String(offset),
      limit: String(Math.min(limit, this.config.maxPageSize)),
    });
    return this.organizationRequest(
      `/api/v1/organizations?${query.toString()}`,
      organizationListSchema,
    );
  }

  getOrganization(organizationId: string) {
    return this.organizationRequest(
      `/api/v1/organizations/${encodeURIComponent(organizationId)}`,
      organizationDetailSchema,
    );
  }

  createOrganization(input: CreateOrganizationInput) {
    return this.organizationRequest(
      "/api/v1/organizations",
      createOrganizationResponseSchema,
      { method: "POST", body: input },
    );
  }

  addOrganizationNode(input: AddOrganizationNodeInput) {
    return this.organizationRequest(
      `/api/v1/organizations/${encodeURIComponent(input.organizationId)}/nodes`,
      addNodeResponseSchema,
      {
        method: "POST",
        body: {
          parent_id: input.parentId,
          kind: input.kind,
          code: input.code,
          name: input.name,
          expected_version: input.expectedVersion,
        },
      },
    );
  }

  moveOrganizationNode(input: MoveOrganizationNodeInput) {
    return this.organizationRequest(
      `/api/v1/organizations/${encodeURIComponent(input.organizationId)}/nodes/${encodeURIComponent(input.nodeId)}/move`,
      emptyResponseSchema,
      {
        method: "POST",
        body: {
          new_parent_id: input.newParentId,
          expected_version: input.expectedVersion,
        },
      },
    );
  }

  listConfigurationProfiles(pageToken = "", pageSize = this.config.defaultPageSize) {
    const query = new URLSearchParams({
      page_size: String(Math.min(pageSize, this.config.maxPageSize)),
    });
    if (pageToken) query.set("page_token", pageToken);
    return this.configurationRequest(
      `/v1/configuration/profiles?${query.toString()}`,
      configurationPageSchema,
    );
  }

  getConfigurationProfile(profileId: string) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}`,
      configurationDetailSchema,
    );
  }

  createConfigurationProfile(
    input: CreateConfigurationInput,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      "/v1/configuration/profiles",
      configurationProfileSchema,
      { method: "POST", body: input, idempotencyKey },
    );
  }

  addConfigurationDraft(
    profileId: string,
    input: DraftConfigurationInput,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions`,
      configurationProfileSchema,
      { method: "POST", body: input, idempotencyKey },
    );
  }

  publishConfiguration(
    profileId: string,
    versionId: string,
    expectedVersion: number,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/publish`,
      configurationProfileSchema,
      {
        method: "POST",
        body: { expected_version: expectedVersion, reason },
        idempotencyKey,
      },
    );
  }

  rollbackConfiguration(
    profileId: string,
    versionId: string,
    expectedVersion: number,
    policyVersion: string,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/rollback`,
      configurationProfileSchema,
      {
        method: "POST",
        body: {
          expected_version: expectedVersion,
          policy_version: policyVersion,
          reason,
        },
        idempotencyKey,
      },
    );
  }

  restoreConfiguration(
    profileId: string,
    versionId: string,
    expectedVersion: number,
    policyVersion: string,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/versions/${encodeURIComponent(versionId)}/restore`,
      configurationProfileSchema,
      {
        method: "POST",
        body: {
          expected_version: expectedVersion,
          policy_version: policyVersion,
          reason,
        },
        idempotencyKey,
      },
    );
  }

  retireConfiguration(
    profileId: string,
    expectedVersion: number,
    reason: string,
    idempotencyKey: string = crypto.randomUUID(),
  ) {
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/retire`,
      configurationProfileSchema,
      {
        method: "POST",
        body: { expected_version: expectedVersion, reason },
        idempotencyKey,
      },
    );
  }

  diffConfiguration(profileId: string, fromVersion: string, toVersion: string) {
    const query = new URLSearchParams({
      from_version: fromVersion,
      to_version: toVersion,
    });
    return this.configurationRequest(
      `/v1/configuration/profiles/${encodeURIComponent(profileId)}/diff?${query.toString()}`,
      configurationVersionDiffSchema,
    );
  }

  private async request<T>(
    path: string,
    options: {
      method?: "GET" | "POST" | "PUT" | "DELETE";
      body?: unknown;
      idempotencyKey?: string;
    } = {},
  ): Promise<T> {
    const identity = this.getIdentity();
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      this.config.requestTimeoutMs,
    );
    const correlationId = crypto.randomUUID();
    const commandId = crypto.randomUUID();
    const method = options.method ?? "GET";
    const headers = new Headers({
      Accept: "application/json",
      Authorization: `Bearer ${identity.accessToken}`,
      "X-BPMP-Tenant-ID": identity.tenantId,
      "X-Correlation-ID": correlationId,
    });
    if (method !== "GET") {
      headers.set("Content-Type", "application/json");
      headers.set("X-Command-ID", commandId);
      headers.set("Idempotency-Key", options.idempotencyKey ?? crypto.randomUUID());
    }
    try {
      const requestInit: RequestInit = {
        method,
        headers,
        signal: controller.signal,
      };
      if (options.body !== undefined) {
        requestInit.body = JSON.stringify(options.body);
      }
      const response = await fetch(new URL(path, this.config.apiBaseUrl), requestInit);
      const responseCorrelation =
        response.headers.get("X-Correlation-ID") ?? correlationId;
      const body = await response.json().catch(() => ({})) as Record<string, unknown>;
      if (!response.ok) {
        const problem = problemDetailsSchema.safeParse(body);
        throw new ApiError(
          problem.success
            ? problem.data.detail || problem.data.title || problem.data.error || problem.data.code || "Request failed"
            : "Request failed",
          response.status,
          responseCorrelation,
        );
      }
      return body as T;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new ApiError("Request timed out", 408, correlationId);
      }
      throw new ApiError("API is unavailable", 0, correlationId);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  private async organizationRequest<T>(
    path: string,
    schema: z.ZodType<T>,
    options: {
      method?: "GET" | "POST";
      body?: unknown;
      idempotencyKey?: string;
    } = {},
  ): Promise<T> {
    const identity = this.getIdentity();
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      this.config.requestTimeoutMs,
    );
    const requestId = crypto.randomUUID();
    const method = options.method ?? "GET";
    const headers = new Headers({
      Accept: "application/json",
      Authorization: `Bearer ${identity.accessToken}`,
      "X-Request-ID": requestId,
      "X-Tenant-ID": identity.tenantId,
    });
    if (options.body !== undefined) headers.set("Content-Type", "application/json");
    try {
      const response = await fetch(
        new URL(path, this.config.organizationApiBaseUrl),
        {
          method,
          headers,
          signal: controller.signal,
          ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
        },
      );
      const contentType = response.headers.get("Content-Type") ?? "";
      if (contentType && !contentType.toLowerCase().includes("application/json")) {
        throw new ApiError(
          "Organization API route is unavailable",
          response.ok ? 502 : response.status,
          response.headers.get("X-Request-ID") ?? requestId,
        );
      }
      const raw: unknown = await response.json().catch(() => null);
      const envelope = z.object({
        data: z.unknown().nullable(),
        error_code: z.string(),
        message: z.string(),
        request_id: z.string(),
        timestamp: z.number(),
      }).safeParse(raw);
      const correlationId = response.headers.get("X-Request-ID") ?? requestId;
      if (!envelope.success) {
        throw new ApiError("Invalid organization API response", 502, correlationId);
      }
      if (
        !response.ok ||
        envelope.data.error_code !== "OK" ||
        envelope.data.data === null
      ) {
        throw new ApiError(
          envelope.data.message || "Organization request failed",
          response.status,
          envelope.data.request_id || correlationId,
        );
      }
      const parsed = schema.safeParse(envelope.data.data);
      if (!parsed.success) {
        throw new ApiError("Invalid organization response data", 502, correlationId);
      }
      return parsed.data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new ApiError("Request timed out", 408, requestId);
      }
      throw new ApiError("Organization API is unavailable", 0, requestId);
    } finally {
      window.clearTimeout(timeout);
    }
  }

  private async configurationRequest<T>(
    path: string,
    schema: z.ZodType<T>,
    options: {
      method?: "GET" | "POST";
      body?: unknown;
      idempotencyKey?: string;
    } = {},
  ): Promise<T> {
    const identity = this.getIdentity();
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      this.config.requestTimeoutMs,
    );
    const correlationId = crypto.randomUUID();
    const headers = new Headers({
      Accept: "application/json",
      Authorization: `Bearer ${identity.accessToken}`,
      "X-BPMP-Tenant-ID": identity.tenantId,
      "X-Correlation-ID": correlationId,
    });
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
      headers.set("X-Command-ID", crypto.randomUUID());
      headers.set("Idempotency-Key", options.idempotencyKey ?? crypto.randomUUID());
    }
    try {
      const response = await fetch(
        new URL(path, this.config.apiBaseUrl),
        {
          method: options.method ?? "GET",
          headers,
          signal: controller.signal,
          ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
        },
      );
      const contentType = response.headers.get("Content-Type") ?? "";
      if (contentType && !contentType.toLowerCase().includes("application/json")) {
        throw new ApiError(
          "Configuration API route is unavailable",
          response.ok ? 502 : response.status,
          response.headers.get("X-Correlation-ID") ?? correlationId,
        );
      }
      const raw: unknown = await response.json().catch(() => null);
      const responseCorrelation =
        response.headers.get("X-Correlation-ID") ?? correlationId;
      if (!response.ok) {
        const error = z.object({ error: z.string() }).safeParse(raw);
        throw new ApiError(
          error.success ? error.data.error : "Configuration request failed",
          response.status,
          responseCorrelation,
        );
      }
      const parsed = schema.safeParse(raw);
      if (!parsed.success) {
        throw new ApiError("Invalid configuration response data", 502, responseCorrelation);
      }
      return parsed.data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new ApiError("Request timed out", 408, correlationId);
      }
      throw new ApiError("Configuration API is unavailable", 0, correlationId);
    } finally {
      window.clearTimeout(timeout);
    }
  }
}
