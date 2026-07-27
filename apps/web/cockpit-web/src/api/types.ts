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
