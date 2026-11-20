import { useEffect, useState, type ReactNode } from "react";
import useSWR from "swr";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import {
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  ErrorPanel,
  FormControl,
  InputLabel,
  LoadingPanel,
  MenuItem,
  Page,
  Paper,
  Select,
  SimpleList,
  SlaBadge,
  Stack,
  StalenessIndicator,
  StatusPanel,
  TextField,
  Typography,
  type SimpleListItem,
} from "@ods/shared-ui";

export interface WorkItem {
  tenant_id?: string;
  work_item_id?: string;
  id?: string;
  instance_id?: string;
  workflow_type?: string;
  workflow_version?: string;
  node_id?: string;
  task_type?: string;
  task_name?: string;
  assignee_id?: string;
  candidate_group?: string;
  form_key?: string;
  status?: string;
  decision?: string;
  due_at_epoch_ms?: number;
  sla_deadline_epoch_ms?: number;
  version?: number;
  delegation_depth?: number;
}

interface WorkItemResponse { work_item: WorkItem }
interface PublishedFormVersion { id: string; schema_json: string; ui_schema_json?: string }
interface PublishedFormResponse { form: { form_key: string; name: string }; published_version: PublishedFormVersion }
type FormOutputValue = string | number | boolean | FormOutputValue[];
interface TaskFormField {
  type: "string" | "integer" | "boolean" | "array";
  title?: string;
  description?: string;
  enum?: Array<string | number | boolean>;
  items?: { type?: "string" | "integer" | "boolean" };
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  minItems?: number;
  maxItems?: number;
}
interface TaskFormSchema { type: "object"; properties: Record<string, TaskFormField>; required?: string[]; additionalProperties?: boolean }
interface TaskFormUiSchema { order?: string[]; fields?: Record<string, { widget?: "default" | "textarea"; width?: "full" | "half"; placeholder?: string; help_text?: string }> }

interface WorkItemPage {
  work_items?: WorkItem[];
  next_page_token?: string;
}

interface CommandResponse {
  command_id?: string;
  work_item_version?: number;
}

type CommandKind = "claim" | "unclaim" | "complete" | "delegate";

interface CommandIntent {
  kind: CommandKind;
  taskId: string;
  path: string;
  body: Record<string, unknown>;
  commandId: string;
  idempotencyKey: string;
}

interface ActionFailure {
  title: string;
  detail: string;
  retryable: boolean;
}

interface ReconciliationNotice {
  title: string;
  detail: string;
  pending: boolean;
}

interface AuditRecord {
  audit_id: string;
  actor_id: string;
  action: string;
  occurred_at_epoch_ms: number;
  from_version?: number;
  to_version?: number;
}

interface AuditRecordPage {
  records?: AuditRecord[];
}

export function TaskWorklist({ platform }: RemoteRouteProps) {
  const [activeTab, setActiveTab] = useState<"personal" | "group">("personal");
  const [searchQuery, setSearchQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<string>("ALL");
  const [pageToken, setPageToken] = useState("");
  const [previousPageTokens, setPreviousPageTokens] = useState<string[]>([]);
  const [selectedTask, setSelectedTask] = useState<WorkItem | null>(null);
  const [formOutputs, setFormOutputs] = useState<Record<string, FormOutputValue>>({});

  // Dialog states for Actions
  const [actionType, setActionType] = useState<"complete" | "delegate" | null>(null);
  const [decision, setDecision] = useState("APPROVED");
  const [delegateTarget, setDelegateTarget] = useState("");
  const [delegateType, setDelegateType] = useState<"user" | "group">("user");
  const [actionError, setActionError] = useState<ActionFailure | null>(null);
  const [commandIntent, setCommandIntent] = useState<CommandIntent | null>(null);
  const [reconciliation, setReconciliation] = useState<ReconciliationNotice | null>(null);
  const [lastReconciledAt, setLastReconciledAt] = useState<Date>();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const selectedTaskId = selectedTask?.work_item_id || selectedTask?.id;
  const detailTaskId = actionType === null ? selectedTaskId : undefined;
  const workItemScope = activeTab === "personal" ? "mine" : "group";

  const query = useSWR<WorkItemPage>(`task-mfe:worklist:${workItemScope}:${pageToken}`, async () => {
    const pageTokenQuery = pageToken ? `&page_token=${encodeURIComponent(pageToken)}` : "";
    const result = await platform.callApi<WorkItemPage>({
      method: "GET",
      path: `/v1/work-items?page_size=50&scope=${workItemScope}${pageTokenQuery}`,
    });
    if (!result.ok) throw new TaskApiError(result.problem.title, result.problem.code, result.problem.retryable);
    setLastReconciledAt(new Date());
    return result.data;
  }, { shouldRetryOnError: false, revalidateOnFocus: true });

  const detailQuery = useSWR<WorkItemResponse>(selectedTaskId ? `task-mfe:detail:${selectedTaskId}` : null, async () => {
    if (!selectedTaskId) throw new Error("No work item selected");
    const result = await platform.callApi<WorkItemResponse>({ method: "GET", path: `/v1/work-items/${encodeURIComponent(selectedTaskId)}` });
    if (!result.ok) throw new TaskApiError(result.problem.title, result.problem.code, result.problem.retryable);
    return result.data;
  }, { shouldRetryOnError: false });
  const activeTask = detailQuery.data?.work_item ?? selectedTask;
  const formKey = actionType === "complete" ? activeTask?.form_key : undefined;
  const formQuery = useSWR<PublishedFormResponse>(formKey ? `task-mfe:published-form:${formKey}` : null, async () => {
    if (!formKey) throw new Error("No published form is linked to this task");
    const result = await platform.callApi<PublishedFormResponse>({ method: "GET", path: `/v1/authoring/published-forms/${encodeURIComponent(formKey)}` });
    if (!result.ok) throw new TaskApiError(result.problem.title, result.problem.code, result.problem.retryable);
    return result.data;
  }, { shouldRetryOnError: false });

  useEffect(() => { setFormOutputs({}); }, [selectedTaskId, formQuery.data?.published_version.id]);

  // History query for selected task
  const historyQuery = useSWR<AuditRecordPage>(
    detailTaskId ? `task-mfe:audit:${detailTaskId}` : null,
    async () => {
      if (!detailTaskId) return { records: [] };
      const result = await platform.callApi<AuditRecordPage>({
        method: "GET",
        path: `/v1/audit-records?work_item_id=${encodeURIComponent(detailTaskId)}`,
      });
      if (!result.ok) throw new TaskApiError(result.problem.title, result.problem.code, result.problem.retryable);
      return result.data;
    },
    { shouldRetryOnError: false }
  );

  const executeCommand = async (intent: CommandIntent) => {
    setIsSubmitting(true);
    setActionError(null);
    setReconciliation(null);
    try {
      const res = await platform.callApi<CommandResponse>({
        method: "POST",
        path: intent.path,
        commandId: intent.commandId,
        idempotencyKey: intent.idempotencyKey,
        body: intent.body,
      });
      if (!res.ok) {
        const isConflict = res.problem.status === 409;
        setActionError({
          title: res.problem.title || `${intent.kind} failed`,
          detail: isConflict
            ? "The task changed before this command was committed. The worklist has been reconciled; review the latest version before trying again."
            : `Code: ${res.problem.code}; request: ${res.requestId}`,
          retryable: !isConflict && res.problem.retryable,
        });
        if (isConflict) {
          setCommandIntent(null);
          await query.mutate();
        }
        return;
      }
      const refreshed = await query.mutate();
      const refreshedItem = refreshed?.work_items?.find((item) => (item.work_item_id || item.id) === intent.taskId);
      const committedVersion = res.data.work_item_version;
      const converged = refreshedItem === undefined
        || committedVersion === undefined
        || (refreshedItem.version ?? 0) >= committedVersion;
      setReconciliation({
        title: converged ? "Command accepted and reconciled" : "Command accepted; projection is synchronizing",
        detail: `Command ${res.data.command_id || intent.idempotencyKey} was accepted.${converged ? " The worklist reflects the committed version." : " Reconcile again if the update remains pending."}`,
        pending: !converged,
      });
      setCommandIntent(null);
      setActionType(null);
      setSelectedTask(null);
    } catch (error) {
      platform.reportError(error, { remote: "task-mfe", phase: `${intent.kind}-command` });
      setActionError({
        title: "Command transport failed",
        detail: "No receipt was returned. Retrying is safe because the command identity and payload are preserved.",
        retryable: true,
      });
    } finally {
      setIsSubmitting(false);
    }
  };

  const beginCommand = (kind: CommandKind, task: WorkItem, body: Record<string, unknown>) => {
    if (isSubmitting) return;
    const taskId = task.work_item_id || task.id;
    if (!taskId) return;
    const intent: CommandIntent = {
      kind,
      taskId,
      path: `/v1/work-items/${taskId}/${kind}`,
      body,
      commandId: `task-command-${crypto.randomUUID()}`,
      idempotencyKey: `task-intent-${crypto.randomUUID()}`,
    };
    setCommandIntent(intent);
    void executeCommand(intent);
  };

  const handleClaim = (task: WorkItem) => {
    beginCommand("claim", task, { expected_version: task.version ?? 1 });
  };

  const openAction = (task: WorkItem, action: "complete" | "delegate") => {
    setSelectedTask(task);
    setActionType(action);
    setCommandIntent(null);
    setActionError(null);
  };

  const handleUnclaim = (task: WorkItem) => {
    if (!task.candidate_group) {
      setActionError({
        title: "Cannot unclaim task",
        detail: "The authoritative work-item projection does not include a candidate group.",
        retryable: false,
      });
      return;
    }
    beginCommand("unclaim", task, {
      expected_version: task.version ?? 1,
      candidate_group: task.candidate_group,
    });
  };

  const handleCompleteSubmit = () => {
    if (!activeTask) return;
    if (detailQuery.isLoading || detailQuery.error) return;
    if (commandIntent?.kind === "complete") {
      void executeCommand(commandIntent);
      return;
    }
    const publishedForm = formQuery.data?.published_version;
    if (activeTask.form_key && (!publishedForm || !isValidTaskForm(publishedForm, formOutputs))) return;
    beginCommand("complete", activeTask, {
      decision,
      expected_version: activeTask.version ?? 1,
      ...(publishedForm ? { form_version_id: publishedForm.id, outputs: formOutputs } : {}),
    });
  };

  const handleDelegateSubmit = () => {
    if (!activeTask || !delegateTarget.trim() || detailQuery.isLoading || detailQuery.error) return;
    if (commandIntent?.kind === "delegate") {
      void executeCommand(commandIntent);
      return;
    }
    const body = delegateType === "user"
      ? { expected_version: activeTask.version ?? 1, assignee_id: delegateTarget.trim() }
      : { expected_version: activeTask.version ?? 1, candidate_group: delegateTarget.trim() };
    beginCommand("delegate", activeTask, body);
  };

  if (query.isLoading) {
    return <Page title="Task Worklist"><LoadingPanel label="Loading work items..." /></Page>;
  }

  if (query.error) {
    const error = query.error instanceof TaskApiError ? query.error : new TaskApiError("Task query failed", "unknown", false);
    const retry = error.retryable ? { onRetry: () => { void query.mutate(); } } : {};
    return (
      <Page title="Task Worklist">
        <ErrorPanel title={error.message} detail={`Code: ${error.code}`} {...retry} />
      </Page>
    );
  }

  const allItems = query.data?.work_items ?? [];

  // Categorize between personal and group tasks
  const filteredItems = allItems.filter((item) => {
    // Tab filter
    if (activeTab === "personal") {
      if (!item.assignee_id) return false;
    } else {
      if (item.assignee_id || !item.candidate_group) return false;
    }

    // Status filter
    if (statusFilter !== "ALL" && item.status !== statusFilter) {
      return false;
    }

    // Text query
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase();
      const matchName = (item.task_name || item.node_id || "").toLowerCase().includes(q);
      const matchId = (item.work_item_id || item.id || "").toLowerCase().includes(q);
      const matchWf = (item.workflow_type || "").toLowerCase().includes(q);
      if (!matchName && !matchId && !matchWf) return false;
    }
    return true;
  });

  const listItems: SimpleListItem[] = filteredItems.map((item, index) => {
    const id = item.work_item_id || item.id || `unknown-${index}`;
    const deadline = item.sla_deadline_epoch_ms || item.due_at_epoch_ms;
    const primary = item.task_name || item.node_id || "Unnamed task";
    const secondary = `${item.workflow_type || "Workflow"} • Status: ${item.status || "UNKNOWN"} • Assignee: ${item.assignee_id || "Unassigned (Group: " + (item.candidate_group || "None") + ")"}`;

    let actionButton: ReactNode = null;
    if (activeTab === "group" && !item.assignee_id) {
      actionButton = (
        <Button size="small" variant="contained" onClick={() => void handleClaim(item)} disabled={isSubmitting}>
          Claim
        </Button>
      );
    } else if (activeTab === "personal" && item.assignee_id) {
      actionButton = (
        <Stack direction="row" spacing={1}>
          {item.candidate_group && (
            <Button size="small" variant="outlined" onClick={() => void handleUnclaim(item)} disabled={isSubmitting}>
              Unclaim
            </Button>
          )}
          <Button size="small" variant="contained" onClick={() => openAction(item, "complete")}>
            Complete
          </Button>
        </Stack>
      );
    }

    return {
      id,
      primary,
      secondary,
      onClick: () => setSelectedTask(item),
      action: (
        <Stack direction="row" spacing={1} alignItems="center">
          <SlaBadge deadlineEpochMs={deadline} />
          {actionButton}
        </Stack>
      ),
    };
  });

  return (
    <Page title="Task Worklist">
      <Stack spacing={2}>
        <StalenessIndicator
          {...(lastReconciledAt ? { lastSyncedAt: lastReconciledAt } : {})}
          onRefresh={() => { void query.mutate(); }}
          isRefreshing={query.isValidating}
        />

        {actionError && actionType === null && (
          <ErrorPanel
            title={actionError.title}
            detail={actionError.detail}
            {...(actionError.retryable && commandIntent
              ? { onRetry: () => { void executeCommand(commandIntent); } }
              : {})}
          />
        )}

        {reconciliation && (
          <StatusPanel
            title={reconciliation.title}
            detail={reconciliation.detail}
            pending={reconciliation.pending}
          />
        )}

        <Paper variant="outlined" sx={{ p: { xs: 1.5, md: 2 }, borderColor: "#e2e7e3", borderRadius: 2 }}>
          <Stack direction={{ xs: "column", sm: "row" }} spacing={2} alignItems="center" justifyContent="space-between">
            <Stack direction="row" spacing={1} role="tablist">
              <Button
                role="tab"
                aria-selected={activeTab === "personal"}
                variant={activeTab === "personal" ? "contained" : "outlined"}
                onClick={() => { setActiveTab("personal"); setPageToken(""); setPreviousPageTokens([]); }}
              >
                Personal Tasks
              </Button>
              <Button
                role="tab"
                aria-selected={activeTab === "group"}
                variant={activeTab === "group" ? "contained" : "outlined"}
                onClick={() => { setActiveTab("group"); setPageToken(""); setPreviousPageTokens([]); }}
              >
                Group Tasks
              </Button>
            </Stack>

            <Stack direction="row" spacing={1.5} sx={{ minWidth: 280 }}>
              <TextField
                size="small"
                placeholder="Search current page..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
              <FormControl size="small" sx={{ minWidth: 110 }}>
                <InputLabel>Status</InputLabel>
                <Select
                  value={statusFilter}
                  label="Status"
                  onChange={(e) => setStatusFilter(e.target.value)}
                >
                  <MenuItem value="ALL">All</MenuItem>
                  <MenuItem value="ACTIVE">Active</MenuItem>
                  <MenuItem value="COMPLETION_REQUESTED">Completion Requested</MenuItem>
                </Select>
              </FormControl>
            </Stack>
          </Stack>
        </Paper>

        <Paper variant="outlined" sx={{ p: { xs: 1.25, md: 1.75 }, borderColor: "#e2e7e3", borderRadius: 2 }}>
          <Stack direction={{ xs: "column", sm: "row" }} justifyContent="space-between" alignItems={{ xs: "flex-start", sm: "center" }} sx={{ px: 0.5, pb: 1.5, gap: 0.5 }}>
            <Box>
              <Typography variant="subtitle2" sx={{ fontWeight: 750 }}>{activeTab === "personal" ? "Assigned to me" : "Available to my groups"}</Typography>
              <Typography variant="caption" color="text.secondary">Showing {filteredItems.length} of {allItems.length} work items on this page</Typography>
            </Box>
            <Chip size="small" variant="outlined" label={query.isValidating ? "Synchronizing" : "Projection-backed"} sx={{ color: query.isValidating ? "#8a5b00" : "primary.dark", borderColor: query.isValidating ? "#f1d28a" : "#b9dfca", bgcolor: query.isValidating ? "#fff8e6" : "#eff9f3" }} />
          </Stack>
          <SimpleList
            items={listItems}
            emptyLabel={activeTab === "personal" ? "No assigned personal tasks" : "No pending group tasks"}
          />
          <Stack direction="row" spacing={1} justifyContent="flex-end" sx={{ mt: 2 }}>
            <Button
              size="small"
              variant="outlined"
              disabled={previousPageTokens.length === 0 || query.isValidating}
              onClick={() => {
                const tokens = [...previousPageTokens];
                setPageToken(tokens.pop() ?? "");
                setPreviousPageTokens(tokens);
              }}
            >
              Previous page
            </Button>
            <Button
              size="small"
              variant="outlined"
              disabled={!query.data?.next_page_token || query.isValidating}
              onClick={() => {
                const next = query.data?.next_page_token;
                if (!next) return;
                setPreviousPageTokens((tokens) => [...tokens, pageToken]);
                setPageToken(next);
              }}
            >
              Next page
            </Button>
          </Stack>
        </Paper>
      </Stack>

      {/* Task Details & Audit History Modal */}
      {selectedTask && actionType === null && (
        <Dialog open onClose={() => setSelectedTask(null)} maxWidth="md" fullWidth>
          <DialogTitle>
            <Stack direction="row" justifyContent="space-between" alignItems="center">
              <Typography variant="h6">{activeTask?.task_name || activeTask?.node_id || selectedTask.task_name || selectedTask.node_id}</Typography>
              <SlaBadge deadlineEpochMs={activeTask?.sla_deadline_epoch_ms || activeTask?.due_at_epoch_ms} />
            </Stack>
          </DialogTitle>
          <DialogContent dividers>
            <Stack spacing={2}>
              {detailQuery.isLoading ? <LoadingPanel label="Loading current task details..." /> : null}
              {detailQuery.error ? <ErrorPanel title="Current task details unavailable" detail={detailQuery.error instanceof TaskApiError ? `Code: ${detailQuery.error.code}` : "The detail query failed."} onRetry={() => { void detailQuery.mutate(); }} /> : null}
              <Box>
                <Typography variant="subtitle2" color="text.secondary">Work Item ID</Typography>
                <Typography variant="body2">{activeTask?.work_item_id || activeTask?.id || selectedTask.work_item_id || selectedTask.id}</Typography>
              </Box>
              <Stack direction="row" spacing={4}>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Workflow</Typography>
                  <Typography variant="body2">{activeTask?.workflow_type || selectedTask.workflow_type} (v{activeTask?.workflow_version || selectedTask.workflow_version})</Typography>
                </Box>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Status</Typography>
                  <Chip label={activeTask?.status || selectedTask.status || "UNKNOWN"} size="small" />
                </Box>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Version</Typography>
                  <Typography variant="body2">{activeTask?.version ?? selectedTask.version ?? 1}</Typography>
                </Box>
              </Stack>
              <Stack direction="row" spacing={4}>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Assignee</Typography>
                  <Typography variant="body2">{activeTask?.assignee_id || "None"}</Typography>
                </Box>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Candidate Group</Typography>
                  <Typography variant="body2">{activeTask?.candidate_group || "None"}</Typography>
                </Box>
              </Stack>

              <Divider sx={{ my: 1 }} />
              <Typography variant="subtitle1" fontWeight={700}>Audit History</Typography>
              {historyQuery.isLoading ? (
                <LoadingPanel label="Loading audit history..." />
              ) : historyQuery.error ? (
                <ErrorPanel
                  title="Audit history unavailable"
                  detail={historyQuery.error instanceof TaskApiError ? `Code: ${historyQuery.error.code}` : "The audit query failed."}
                  onRetry={() => { void historyQuery.mutate(); }}
                />
              ) : (historyQuery.data?.records ?? []).length === 0 ? (
                <Typography variant="body2" color="text.secondary">No audit entries found</Typography>
              ) : (
                <Stack spacing={1}>
                  {historyQuery.data?.records?.map((record) => (
                    <Box key={record.audit_id} sx={{ p: 1, bgcolor: "background.default", borderRadius: 1 }}>
                      <Typography variant="body2" fontWeight={600}>
                        {record.action} by {record.actor_id}
                      </Typography>
                      <Typography variant="caption" color="text.secondary">
                        {new Date(record.occurred_at_epoch_ms).toLocaleString()} • Version: {record.from_version ?? 0} → {record.to_version ?? 0}
                      </Typography>
                    </Box>
                  ))}
                </Stack>
              )}
            </Stack>
          </DialogContent>
          <DialogActions>
            {(activeTask ?? selectedTask).assignee_id ? (
              <>
                <Button onClick={() => openAction(activeTask ?? selectedTask, "delegate")} disabled={detailQuery.isLoading || Boolean(detailQuery.error)}>Delegate</Button>
                <Button variant="contained" onClick={() => openAction(activeTask ?? selectedTask, "complete")} disabled={detailQuery.isLoading || Boolean(detailQuery.error)}>Complete</Button>
              </>
            ) : (
              <Button variant="contained" onClick={() => void handleClaim(activeTask ?? selectedTask)}>Claim</Button>
            )}
            <Button onClick={() => setSelectedTask(null)}>Close</Button>
          </DialogActions>
        </Dialog>
      )}

      {/* Complete Dialog */}
      {selectedTask && actionType === "complete" && (
        <Dialog open onClose={() => { if (!isSubmitting) setActionType(null); }} maxWidth="xs" fullWidth>
          <DialogTitle>Complete Task</DialogTitle>
          <DialogContent>
            <Stack spacing={2} sx={{ mt: 1 }}>
              {actionError && (
                <ErrorPanel
                  title={actionError.title}
                  detail={actionError.detail}
                  {...(actionError.retryable && commandIntent
                    ? { onRetry: () => { void executeCommand(commandIntent); } }
                    : {})}
                />
              )}
              <Typography variant="body2">
                Decide on: <strong>{selectedTask.task_name || selectedTask.node_id}</strong>
              </Typography>
              {formKey ? (
                <Box>
                  <Typography variant="subtitle2" sx={{ mb: 1 }}>Task form · {formQuery.data?.form.name || formKey}</Typography>
                  {formQuery.isLoading ? <LoadingPanel label="Loading published form..." /> : null}
                  {formQuery.error ? <ErrorPanel title="Published task form unavailable" detail={formQuery.error instanceof TaskApiError ? `Code: ${formQuery.error.code}` : "The form query failed."} onRetry={() => { void formQuery.mutate(); }} /> : null}
                  {formQuery.data ? <TaskFormFields
                    version={formQuery.data.published_version}
                    values={formOutputs}
                    onChange={setFormOutputs}
                  /> : null}
                </Box>
              ) : null}
              <FormControl fullWidth size="small">
                <InputLabel>Decision</InputLabel>
                <Select
                  value={decision}
                  label="Decision"
                  onChange={(e) => {
                    setDecision(e.target.value);
                    setCommandIntent(null);
                    setActionError(null);
                  }}
                >
                  <MenuItem value="APPROVED">APPROVED</MenuItem>
                  <MenuItem value="REJECTED">REJECTED</MenuItem>
                  <MenuItem value="CHANGES_REQUESTED">CHANGES_REQUESTED</MenuItem>
                </Select>
              </FormControl>
            </Stack>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => { setActionType(null); setCommandIntent(null); setActionError(null); }} disabled={isSubmitting}>Cancel</Button>
            <Button variant="contained" onClick={() => void handleCompleteSubmit()} disabled={isSubmitting || detailQuery.isLoading || Boolean(detailQuery.error) || Boolean(formKey && (formQuery.isLoading || !formQuery.data || !isValidTaskForm(formQuery.data.published_version, formOutputs)))}>
              Submit Decision
            </Button>
          </DialogActions>
        </Dialog>
      )}

      {/* Delegate Dialog */}
      {selectedTask && actionType === "delegate" && (
        <Dialog open onClose={() => { if (!isSubmitting) setActionType(null); }} maxWidth="xs" fullWidth>
          <DialogTitle>Delegate Task</DialogTitle>
          <DialogContent>
            <Stack spacing={2} sx={{ mt: 1 }}>
              {actionError && (
                <ErrorPanel
                  title={actionError.title}
                  detail={actionError.detail}
                  {...(actionError.retryable && commandIntent
                    ? { onRetry: () => { void executeCommand(commandIntent); } }
                    : {})}
                />
              )}
              <FormControl fullWidth size="small">
                <InputLabel>Delegate To</InputLabel>
                <Select
                  value={delegateType}
                  label="Delegate To"
                  onChange={(e) => {
                    setDelegateType(e.target.value as "user" | "group");
                    setCommandIntent(null);
                    setActionError(null);
                  }}
                >
                  <MenuItem value="user">User (Assignee)</MenuItem>
                  <MenuItem value="group">Candidate Group</MenuItem>
                </Select>
              </FormControl>
              <TextField
                size="small"
                label={delegateType === "user" ? "Target Assignee ID" : "Target Group Name"}
                placeholder={delegateType === "user" ? "e.g. officer-2" : "e.g. credit-team"}
                value={delegateTarget}
                onChange={(e) => {
                  setDelegateTarget(e.target.value);
                  setCommandIntent(null);
                  setActionError(null);
                }}
                fullWidth
              />
            </Stack>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => { setActionType(null); setCommandIntent(null); setActionError(null); }} disabled={isSubmitting}>Cancel</Button>
            <Button
              variant="contained"
              onClick={() => void handleDelegateSubmit()}
              disabled={isSubmitting || detailQuery.isLoading || Boolean(detailQuery.error) || !delegateTarget.trim()}
            >
              Delegate
            </Button>
          </DialogActions>
        </Dialog>
      )}
    </Page>
  );
}

class TaskApiError extends Error {
  constructor(message: string, readonly code: string, readonly retryable: boolean) {
    super(message);
    this.name = "TaskApiError";
  }
}

function TaskFormFields({
  version,
  values,
  onChange,
}: {
  version: PublishedFormVersion;
  values: Record<string, FormOutputValue>;
  onChange: (values: Record<string, FormOutputValue>) => void;
}) {
  const parsed = parseTaskForm(version);
  if (!parsed) return <ErrorPanel title="Published form cannot be rendered" detail="The published schema or UI schema is invalid or contains an unsupported field type." />;
  const names = [...new Set([...(parsed.ui.order ?? []), ...Object.keys(parsed.schema.properties).filter((name) => !(parsed.ui.order ?? []).includes(name))])];
  return (
    <Stack spacing={1.5}>
      {names.map((name) => {
        const field = parsed.schema.properties[name];
        if (!field) return null;
        const uiField = parsed.ui.fields?.[name] ?? {};
        const required = parsed.schema.required?.includes(name) ?? false;
        const label = `${field.title || name}${required ? " *" : ""}`;
        const value = values[name];
        const help = uiField.help_text || field.description;
        if (field.type === "boolean") {
          return <Box component="label" key={name} sx={{ display: "flex", alignItems: "center", gap: 1, py: 0.5 }}>
            <input type="checkbox" checked={Boolean(value)} onChange={(event) => onChange({ ...values, [name]: event.target.checked })} />
            <Typography variant="body2">{label}</Typography>
            {help ? <Typography variant="caption" color="text.secondary">{help}</Typography> : null}
          </Box>;
        }
        if (field.enum?.length) {
          return <FormControl key={name} fullWidth size="small" required={required}>
            <InputLabel>{label}</InputLabel>
            <Select value={value === undefined ? "" : String(value)} label={label} onChange={(event) => {
              const selected = field.enum?.find((option) => String(option) === event.target.value);
              if (selected !== undefined) onChange({ ...values, [name]: selected });
              else { const next = { ...values }; delete next[name]; onChange(next); }
            }}>
              <MenuItem value=""><em>Select…</em></MenuItem>
              {field.enum.map((option) => <MenuItem key={String(option)} value={String(option)}>{String(option)}</MenuItem>)}
            </Select>
            {help ? <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5 }}>{help}</Typography> : null}
          </FormControl>;
        }
        const isArray = field.type === "array";
        const inputValue = isArray
          ? (Array.isArray(value) ? value.join(", ") : "")
          : value === undefined ? "" : String(value);
        return <TextField
          key={name}
          fullWidth
          size="small"
          required={required}
          label={label}
          helperText={help}
          placeholder={uiField.placeholder}
          multiline={field.type === "string" && uiField.widget === "textarea"}
          minRows={field.type === "string" && uiField.widget === "textarea" ? 3 : undefined}
          type={field.type === "integer" ? "number" : "text"}
          value={inputValue}
          inputProps={{
            min: field.minimum,
            max: field.maximum,
            minLength: field.minLength,
            maxLength: field.maxLength,
            step: field.type === "integer" ? 1 : undefined,
          }}
          onChange={(event) => {
            const raw = event.target.value;
            if (raw === "") {
              const next = { ...values };
              delete next[name];
              onChange(next);
            } else if (isArray) {
              const tokens = raw.split(",").map((token) => token.trim()).filter(Boolean);
              const items = tokens.map((token) => field.items?.type === "integer" ? Number(token) : field.items?.type === "boolean" ? token.toLowerCase() === "true" : token);
              onChange({ ...values, [name]: items });
            } else {
              onChange({ ...values, [name]: field.type === "integer" ? Number(raw) : raw });
            }
          }}
        />;
      })}
    </Stack>
  );
}

function parseTaskForm(version: PublishedFormVersion): { schema: TaskFormSchema; ui: TaskFormUiSchema } | null {
  try {
    const schema = JSON.parse(version.schema_json) as TaskFormSchema;
    const ui = JSON.parse(version.ui_schema_json || "{}") as TaskFormUiSchema;
    if (!schema || schema.type !== "object" || !schema.properties || typeof schema.properties !== "object" || Array.isArray(schema.properties) || !ui || typeof ui !== "object" || Array.isArray(ui)) return null;
    if (Object.keys(ui).some((key) => key !== "order" && key !== "fields")) return null;
    if (schema.required && (!Array.isArray(schema.required) || schema.required.some((name) => typeof name !== "string" || !schema.properties[name]))) return null;
    if (ui.order && (!Array.isArray(ui.order) || ui.order.some((name) => typeof name !== "string" || !schema.properties[name]) || new Set(ui.order).size !== ui.order.length)) return null;
    if (ui.fields && (typeof ui.fields !== "object" || Array.isArray(ui.fields))) return null;
    for (const [name, field] of Object.entries(schema.properties)) {
      if (!field || !["string", "integer", "boolean", "array"].includes(field.type)) return null;
      if (field.type === "array" && !["string", "integer", "boolean"].includes(field.items?.type ?? "")) return null;
      if (field.enum && (!Array.isArray(field.enum) || field.enum.some((option) => !["string", "number", "boolean"].includes(typeof option)))) return null;
      const uiField = ui.fields?.[name];
      if (uiField && (Object.keys(uiField).some((key) => !["widget", "width", "placeholder", "help_text"].includes(key))
        || (uiField.widget !== undefined && !["default", "textarea"].includes(uiField.widget))
        || (uiField.widget === "textarea" && (field.type !== "string" || field.enum?.length))
        || (uiField.width !== undefined && !["full", "half"].includes(uiField.width)))) return null;
    }
    if (Object.keys(ui.fields ?? {}).some((name) => !schema.properties[name])) return null;
    return { schema, ui };
  } catch {
    return null;
  }
}

function isValidTaskForm(version: PublishedFormVersion, values: Record<string, FormOutputValue>): boolean {
  const parsed = parseTaskForm(version);
  if (!parsed || parsed.schema.additionalProperties !== false) return false;
  const propertyNames = Object.keys(parsed.schema.properties);
  if (Object.keys(values).some((name) => !propertyNames.includes(name))) return false;
  if ((parsed.schema.required ?? []).some((name) => values[name] === undefined || values[name] === "")) return false;
  return Object.entries(values).every(([name, value]) => {
    const field = parsed.schema.properties[name];
    if (!field) return false;
    if (field.type === "string") return typeof value === "string" && value.length >= (field.minLength ?? 0) && value.length <= (field.maxLength ?? Number.MAX_SAFE_INTEGER) && (!field.enum || field.enum.includes(value));
    if (field.type === "integer") return typeof value === "number" && Number.isSafeInteger(value) && value >= (field.minimum ?? Number.MIN_SAFE_INTEGER) && value <= (field.maximum ?? Number.MAX_SAFE_INTEGER) && (!field.enum || field.enum.includes(value));
    if (field.type === "boolean") return typeof value === "boolean" && (!field.enum || field.enum.includes(value));
    return Array.isArray(value) && value.length >= (field.minItems ?? 0) && value.length <= (field.maxItems ?? 256) && value.every((item) => field.items?.type === "string" ? typeof item === "string" : field.items?.type === "integer" ? typeof item === "number" && Number.isSafeInteger(item) : typeof item === "boolean");
  });
}

export const contractVersion = "1.0";
export const route = {
  path: "/tasks",
  title: "Tasks",
  Component: TaskWorklist,
};

const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
