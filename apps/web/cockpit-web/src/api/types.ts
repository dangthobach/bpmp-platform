export interface WorkItem {
  tenant_id: string;
  work_item_id: string;
  instance_id: string;
  workflow_type: string;
  workflow_version: string;
  node_id: string;
  task_type: string;
  assignee_id: string;
  candidate_group: string;
  form_key: string;
  status: string;
  decision: string;
  sla_deadline_epoch_ms: number;
  version: number;
}

export interface WorkItemPage {
  work_items: WorkItem[];
  next_page_token: string;
}

export interface WorkItemResponse {
  work_item: WorkItem;
}

export interface CommandReceipt {
  command_id: string;
  committed_sequence?: number;
  duplicate?: boolean;
  work_item_version?: number;
}

export interface CasePlanItem {
  plan_item_id: string;
  kind: string;
  status: string;
  version: number;
}

export interface CaseView {
  tenant_id: string;
  case_id: string;
  case_type: string;
  status: string;
  plan_items: CasePlanItem[];
  version: number;
}

export interface CaseResponse {
  case: CaseView;
}

export interface AuditRecord {
  audit_id: string;
  work_item_id: string;
  case_id: string;
  actor_id: string;
  action: string;
  occurred_at_epoch_ms: number;
  command_id: string;
  correlation_id: string;
  from_version: number;
  to_version: number;
  details_json: string;
}

export interface AuditPage {
  records: AuditRecord[];
  next_page_token: string;
}

export interface StartWorkflowInput {
  workflowType: string;
  instanceId: string;
  workflowVersion: string;
  startNodeId: string;
}

export interface CompleteWorkItemInput {
  workItemId: string;
  decision: string;
  expectedVersion: number;
  idempotencyKey?: string;
}

export interface DelegateWorkItemInput {
  workItemId: string;
  expectedVersion: number;
  assigneeId?: string;
  candidateGroup?: string;
  idempotencyKey?: string;
}

export interface WorkflowInstance {
  tenant_id?: string;
  instance_id: string;
  workflow_type?: string;
  workflow_version?: string;
  status?: string;
  active_node_id?: string;
  last_event_sequence?: number;
  started_at_epoch_ms?: number;
  updated_at_epoch_ms?: number;
  completed_at_epoch_ms?: number;
  config_version?: string;
  policy_version?: string;
  initiator_actor_id?: string;
  is_deleted?: boolean;
  version?: number;
}

export interface CompletedProcess {
  instance: WorkflowInstance;
  duration_ms?: number;
  event_count?: number;
  masked_fields?: string[];
  events?: WorkflowEvent[];
}

export interface CompletedProcessPage {
  processes: CompletedProcess[];
  next_page_token: string;
  normalized_sort?: ProcessSortInput[];
  policy_version?: string;
  query_fingerprint?: string;
}

export type ProcessField =
  | "PROCESS_FIELD_INSTANCE_ID" | "PROCESS_FIELD_WORKFLOW_TYPE" | "PROCESS_FIELD_WORKFLOW_VERSION"
  | "PROCESS_FIELD_STATUS" | "PROCESS_FIELD_ACTIVE_NODE_ID" | "PROCESS_FIELD_LAST_EVENT_SEQUENCE"
  | "PROCESS_FIELD_STARTED_AT" | "PROCESS_FIELD_UPDATED_AT" | "PROCESS_FIELD_COMPLETED_AT"
  | "PROCESS_FIELD_DURATION_MS" | "PROCESS_FIELD_CONFIG_VERSION" | "PROCESS_FIELD_POLICY_VERSION"
  | "PROCESS_FIELD_VERSION" | "PROCESS_FIELD_INITIATOR_ACTOR_ID" | "PROCESS_FIELD_EVENT_COUNT"
  | "PROCESS_FIELD_IS_DELETED";
export type EventField = "EVENT_FIELD_EVENT_TYPE" | "EVENT_FIELD_NODE_ID" | "EVENT_FIELD_ACTOR_ID"
  | "EVENT_FIELD_SEQUENCE" | "EVENT_FIELD_OCCURRED_AT" | "EVENT_FIELD_CONFIG_VERSION" | "EVENT_FIELD_POLICY_VERSION";
export type QueryScope = "QUERY_SCOPE_ALL" | "QUERY_SCOPE_ACTIVE" | "QUERY_SCOPE_COMPLETED" | "QUERY_SCOPE_DELETED";
export type ComparisonOperator =
  | "COMPARISON_OPERATOR_EQUAL" | "COMPARISON_OPERATOR_NOT_EQUAL"
  | "COMPARISON_OPERATOR_GREATER_THAN" | "COMPARISON_OPERATOR_GREATER_THAN_OR_EQUAL"
  | "COMPARISON_OPERATOR_LESS_THAN" | "COMPARISON_OPERATOR_LESS_THAN_OR_EQUAL"
  | "COMPARISON_OPERATOR_IN" | "COMPARISON_OPERATOR_NOT_IN" | "COMPARISON_OPERATOR_CONTAINS"
  | "COMPARISON_OPERATOR_STARTS_WITH" | "COMPARISON_OPERATOR_ENDS_WITH"
  | "COMPARISON_OPERATOR_IS_NULL" | "COMPARISON_OPERATOR_IS_NOT_NULL"
  | "COMPARISON_OPERATOR_BETWEEN" | "COMPARISON_OPERATOR_NOT_BETWEEN";
export type TextTransform = "TEXT_TRANSFORM_NONE" | "TEXT_TRANSFORM_TRIM" | "TEXT_TRANSFORM_LOWER"
  | "TEXT_TRANSFORM_UPPER" | "TEXT_TRANSFORM_TRIM_LOWER" | "TEXT_TRANSFORM_TRIM_UPPER";
export type NumericTransform = "NUMERIC_TRANSFORM_NONE" | "NUMERIC_TRANSFORM_ABS";
export type DateTransform = "DATE_TRANSFORM_NONE" | "DATE_TRANSFORM_UTC_DAY" | "DATE_TRANSFORM_UTC_WEEK"
  | "DATE_TRANSFORM_UTC_MONTH" | "DATE_TRANSFORM_UTC_YEAR";
export type QueryValue = { string_value: string } | { integer_value: number } | { boolean_value: boolean } | { timestamp_epoch_ms: number } | { decimal_value: string };
export interface ProcessPredicateInput { field: ProcessField; operator: ComparisonOperator; values?: QueryValue[]; text_transform?: TextTransform; numeric_transform?: NumericTransform; date_transform?: DateTransform; coalesce_value?: QueryValue }
export interface EventPredicateInput { field: EventField; operator: ComparisonOperator; values?: QueryValue[]; text_transform?: TextTransform; numeric_transform?: NumericTransform; date_transform?: DateTransform; coalesce_value?: QueryValue }
export type EventExpressionInput = { predicate: EventPredicateInput } | { group: { operator: "LOGICAL_OPERATOR_AND" | "LOGICAL_OPERATOR_OR"; expressions: EventExpressionInput[] } } | { negated: EventExpressionInput };
export type ProcessExpressionInput = { predicate: ProcessPredicateInput } | { group: { operator: "LOGICAL_OPERATOR_AND" | "LOGICAL_OPERATOR_OR"; expressions: ProcessExpressionInput[] } } | { negated: ProcessExpressionInput } | { event_relation: { quantifier: "RELATION_QUANTIFIER_EXISTS" | "RELATION_QUANTIFIER_NOT_EXISTS"; expression: EventExpressionInput } };
export interface ProcessSortInput { field: ProcessField; direction: "SORT_DIRECTION_ASCENDING" | "SORT_DIRECTION_DESCENDING"; text_transform?: TextTransform; numeric_transform?: NumericTransform; date_transform?: DateTransform; coalesce_value?: QueryValue }

export interface CompletedProcessQueryInput {
  filter?: ProcessExpressionInput;
  sort?: ProcessSortInput[];
  select?: ProcessField[];
  include_deleted?: boolean;
  page_size?: number;
  page_token?: string;
  collapse?: ProcessField;
  expand_events?: EventExpansionInput;
}

export interface ProcessQueryInput extends CompletedProcessQueryInput { scope: QueryScope }
export interface EventExpansionInput { filter?: EventExpressionInput; limit: number }
export interface WorkflowEvent { instance_id?: string; sequence?: number; event_type?: string; node_id?: string; actor_id?: string; occurred_at_epoch_ms?: number; config_version?: string; policy_version?: string; is_deleted?: boolean; masked_fields?: string[] }
export interface EventQueryInput { filter?: EventExpressionInput; process_filter?: ProcessExpressionInput; sort?: { field: EventField; direction: "SORT_DIRECTION_ASCENDING" | "SORT_DIRECTION_DESCENDING" }[]; page_size?: number; page_token?: string }
export interface EventPage { events: WorkflowEvent[]; next_page_token: string; policy_version?: string }
export type AggregateOperation = "AGGREGATE_OPERATION_COUNT" | "AGGREGATE_OPERATION_SUM" | "AGGREGATE_OPERATION_AVG" | "AGGREGATE_OPERATION_MIN" | "AGGREGATE_OPERATION_MAX";
export interface AggregateQueryInput { scope: QueryScope; filter?: ProcessExpressionInput; group_by: ProcessField[]; metrics: { alias: string; operation: AggregateOperation; field?: ProcessField }[]; limit?: number }
export interface AggregateCell { name: string; value?: QueryValue }
export interface AggregatePage { rows: { dimensions: AggregateCell[]; metrics: AggregateCell[] }[]; policy_version?: string }
export interface SavedQuery { id: string; name: string; version: number; query: ProcessQueryInput; created_at_epoch_ms: number; updated_at_epoch_ms: number }
export interface SavedQueryPage { saved_queries: SavedQuery[]; next_page_token: string }
export interface QueryExport { id: string; status: "QUERY_EXPORT_STATUS_PENDING" | "QUERY_EXPORT_STATUS_COMPLETED" | "QUERY_EXPORT_STATUS_FAILED" | "QUERY_EXPORT_STATUS_CANCELLED"; row_count?: number; csv?: string; error_code?: string; created_at_epoch_ms: number; expires_at_epoch_ms: number }
export interface QueryPlan { redacted_plan_json: string; query_fingerprint: string }
export interface FieldCapability { field: ProcessField; value_kind: string; filterable: boolean; sortable: boolean; selectable: boolean; operators: ComparisonOperator[]; text_transforms?: TextTransform[]; numeric_transforms?: NumericTransform[]; date_transforms?: DateTransform[] }
export interface EventFieldCapability { field: EventField; value_kind: string; operators: ComparisonOperator[]; text_transforms?: TextTransform[]; numeric_transforms?: NumericTransform[]; date_transforms?: DateTransform[] }
export interface QueryCapabilities {
  schema_version: string; config_version: string; policy_version: string;
  fields: FieldCapability[]; event_fields: EventFieldCapability[];
  default_page_size: number; max_page_size: number; max_filter_depth: number; max_predicates: number;
  max_sort_fields: number; max_relation_joins: number; max_in_values: number; max_string_bytes: number;
  scopes: QueryScope[]; default_sort: ProcessSortInput[];
}
