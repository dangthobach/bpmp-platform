import { useState, type ReactNode } from "react";
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
  body: Record<string, string | number>;
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

  const query = useSWR<WorkItemPage>(`task-mfe:worklist:${pageToken}`, async () => {
    const pageTokenQuery = pageToken ? `&page_token=${encodeURIComponent(pageToken)}` : "";
    const result = await platform.callApi<WorkItemPage>({
      method: "GET",
      path: `/v1/work-items?page_size=50${pageTokenQuery}`,
    });
    if (!result.ok) throw new TaskApiError(result.problem.title, result.problem.code, result.problem.retryable);
    setLastReconciledAt(new Date());
    return result.data;
  }, { shouldRetryOnError: false, revalidateOnFocus: true });

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

  const beginCommand = (kind: CommandKind, task: WorkItem, body: Record<string, string | number>) => {
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
    if (!selectedTask) return;
    if (commandIntent?.kind === "complete") {
      void executeCommand(commandIntent);
      return;
    }
    beginCommand("complete", selectedTask, {
      decision,
      expected_version: selectedTask.version ?? 1,
    });
  };

  const handleDelegateSubmit = () => {
    if (!selectedTask || !delegateTarget.trim()) return;
    if (commandIntent?.kind === "delegate") {
      void executeCommand(commandIntent);
      return;
    }
    const body = delegateType === "user"
      ? { expected_version: selectedTask.version ?? 1, assignee_id: delegateTarget.trim() }
      : { expected_version: selectedTask.version ?? 1, candidate_group: delegateTarget.trim() };
    beginCommand("delegate", selectedTask, body);
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

        <Paper sx={{ p: 2 }}>
          <Stack direction={{ xs: "column", sm: "row" }} spacing={2} alignItems="center" justifyContent="space-between">
            <Stack direction="row" spacing={1} role="tablist">
              <Button
                role="tab"
                aria-selected={activeTab === "personal"}
                variant={activeTab === "personal" ? "contained" : "outlined"}
                onClick={() => setActiveTab("personal")}
              >
                {`Personal Tasks (${allItems.filter((i) => Boolean(i.assignee_id)).length})`}
              </Button>
              <Button
                role="tab"
                aria-selected={activeTab === "group"}
                variant={activeTab === "group" ? "contained" : "outlined"}
                onClick={() => setActiveTab("group")}
              >
                {`Group Tasks (${allItems.filter((i) => !i.assignee_id && i.candidate_group).length})`}
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
                  <MenuItem value="COMPLETED">Completed</MenuItem>
                </Select>
              </FormControl>
            </Stack>
          </Stack>
        </Paper>

        <Paper sx={{ p: 2 }}>
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
              <Typography variant="h6">{selectedTask.task_name || selectedTask.node_id}</Typography>
              <SlaBadge deadlineEpochMs={selectedTask.sla_deadline_epoch_ms || selectedTask.due_at_epoch_ms} />
            </Stack>
          </DialogTitle>
          <DialogContent dividers>
            <Stack spacing={2}>
              <Box>
                <Typography variant="subtitle2" color="text.secondary">Work Item ID</Typography>
                <Typography variant="body2">{selectedTask.work_item_id || selectedTask.id}</Typography>
              </Box>
              <Stack direction="row" spacing={4}>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Workflow</Typography>
                  <Typography variant="body2">{selectedTask.workflow_type} (v{selectedTask.workflow_version})</Typography>
                </Box>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Status</Typography>
                  <Chip label={selectedTask.status || "UNKNOWN"} size="small" />
                </Box>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Version</Typography>
                  <Typography variant="body2">{selectedTask.version ?? 1}</Typography>
                </Box>
              </Stack>
              <Stack direction="row" spacing={4}>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Assignee</Typography>
                  <Typography variant="body2">{selectedTask.assignee_id || "None"}</Typography>
                </Box>
                <Box>
                  <Typography variant="subtitle2" color="text.secondary">Candidate Group</Typography>
                  <Typography variant="body2">{selectedTask.candidate_group || "None"}</Typography>
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
            {selectedTask.assignee_id ? (
              <>
                <Button onClick={() => openAction(selectedTask, "delegate")}>Delegate</Button>
                <Button variant="contained" onClick={() => openAction(selectedTask, "complete")}>Complete</Button>
              </>
            ) : (
              <Button variant="contained" onClick={() => void handleClaim(selectedTask)}>Claim</Button>
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
            <Button variant="contained" onClick={() => void handleCompleteSubmit()} disabled={isSubmitting}>
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
              disabled={isSubmitting || !delegateTarget.trim()}
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

export const contractVersion = "1.0";
export const route = {
  path: "/tasks",
  title: "Tasks",
  Component: TaskWorklist,
};

const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
