import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  RefreshCw,
  Search,
  Send,
  Users,
} from "lucide-react";
import { useMemo, useState, type FormEvent } from "react";
import { executeBatch } from "../../api/batch";
import { useApi } from "../../api/ApiContext";
import type { WorkItem } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { Dialog } from "../../components/Dialog";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { StatusBadge } from "../../components/StatusBadge";
import { useRuntimeConfig } from "../../config/ConfigContext";

type BatchAction = "complete" | "delegate" | null;

export function WorkItemsPage() {
  const api = useApi();
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const queryClient = useQueryClient();
  const [pageTokens, setPageTokens] = useState<string[]>([""]);
  const pageToken = pageTokens.at(-1) ?? "";
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [search, setSearch] = useState("");
  const [batchAction, setBatchAction] = useState<BatchAction>(null);
  const [decision, setDecision] = useState("");
  const [assignee, setAssignee] = useState("");
  const [candidateGroup, setCandidateGroup] = useState("");
  const [progress, setProgress] = useState<{ processed: number; total: number } | null>(null);
  const [batchSummary, setBatchSummary] = useState<string | null>(null);

  const query = useQuery({
    queryKey: ["work-items", identity?.tenantId, pageToken, config.defaultPageSize],
    queryFn: () => api.listWorkItems(pageToken, config.defaultPageSize),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
    placeholderData: keepPreviousData,
  });

  const visible = useMemo(() => {
    const normalized = search.trim().toLowerCase();
    if (!normalized) return query.data?.work_items ?? [];
    return (query.data?.work_items ?? []).filter((item) =>
      [
        item.work_item_id,
        item.instance_id,
        item.workflow_type,
        item.task_type,
        item.assignee_id,
        item.candidate_group,
      ].some((value) => value.toLowerCase().includes(normalized)),
    );
  }, [query.data, search]);

  const selectedItems = useMemo(
    () => (query.data?.work_items ?? []).filter((item) => selected.has(item.work_item_id)),
    [query.data, selected],
  );

  const batchMutation = useMutation({
    mutationFn: async ({ action, items }: { action: Exclude<BatchAction, null>; items: WorkItem[] }) => {
      setProgress({ processed: 0, total: items.length });
      return executeBatch(
        items,
        {
          chunkSize: config.batchChunkSize,
          concurrency: config.batchConcurrency,
        },
        async (item) => {
          const idempotencyKey = `${action}:${item.work_item_id}:${item.version}`;
          if (action === "complete") {
            await api.completeWorkItem({
              workItemId: item.work_item_id,
              decision,
              expectedVersion: item.version,
              idempotencyKey,
            });
          } else {
            await api.delegateWorkItem({
              workItemId: item.work_item_id,
              expectedVersion: item.version,
              ...(assignee ? { assigneeId: assignee } : { candidateGroup }),
              idempotencyKey,
            });
          }
        },
        (processed, total) => setProgress({ processed, total }),
      );
    },
    onSuccess(result) {
      setBatchSummary(`${result.succeeded.length} succeeded, ${result.failed.length} failed`);
      setSelected(new Set());
      setBatchAction(null);
      void queryClient.invalidateQueries({ queryKey: ["work-items"] });
    },
    onSettled() {
      setProgress(null);
    },
  });

  function toggle(id: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleVisible() {
    setSelected((current) => {
      const next = new Set(current);
      const allSelected = visible.length > 0 && visible.every((item) => next.has(item.work_item_id));
      for (const item of visible) {
        if (allSelected) next.delete(item.work_item_id);
        else next.add(item.work_item_id);
      }
      return next;
    });
  }

  function submitBatch(event: FormEvent) {
    event.preventDefault();
    if (!batchAction || selectedItems.length === 0) return;
    batchMutation.mutate({ action: batchAction, items: selectedItems });
  }

  const allVisibleSelected =
    visible.length > 0 && visible.every((item) => selected.has(item.work_item_id));

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Work queue</h1>
          <p>{query.data?.work_items.length ?? 0} items on this page</p>
        </div>
        <IconButton
          icon={RefreshCw}
          label="Refresh work items"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        />
      </header>

      <section className="toolbar" aria-label="Work item tools">
        <div className="search-input">
          <Search size={16} aria-hidden="true" />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter current page"
            aria-label="Filter current page"
          />
        </div>
        <div className="toolbar__spacer" />
        <Button
          icon={CheckCircle2}
          onClick={() => setBatchAction("complete")}
          disabled={selectedItems.length === 0}
        >
          Complete
        </Button>
        <Button
          icon={Users}
          onClick={() => setBatchAction("delegate")}
          disabled={selectedItems.length === 0}
        >
          Delegate
        </Button>
      </section>

      {batchSummary ? (
        <div className="inline-notice" role="status">
          <CheckCircle2 size={16} />
          <span>{batchSummary}</span>
          <button onClick={() => setBatchSummary(null)}>Dismiss</button>
        </div>
      ) : null}

      <section className="table-surface">
        {query.isPending ? <LoadingRows /> : null}
        {query.isError ? (
          <ErrorState message="Work items could not be loaded" retry={() => void query.refetch()} />
        ) : null}
        {query.isSuccess && visible.length === 0 ? (
          <EmptyState title="No work items" detail="No items match the current page filter." />
        ) : null}
        {visible.length > 0 ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th className="selection-cell">
                    <input
                      type="checkbox"
                      checked={allVisibleSelected}
                      onChange={toggleVisible}
                      aria-label="Select all visible work items"
                    />
                  </th>
                  <th>Work item</th>
                  <th>Process</th>
                  <th>Task</th>
                  <th>Assignment</th>
                  <th>SLA</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((item) => (
                  <tr key={item.work_item_id} className={selected.has(item.work_item_id) ? "is-selected" : ""}>
                    <td className="selection-cell">
                      <input
                        type="checkbox"
                        checked={selected.has(item.work_item_id)}
                        onChange={() => toggle(item.work_item_id)}
                        aria-label={`Select ${item.work_item_id}`}
                      />
                    </td>
                    <td>
                      <strong>{item.work_item_id}</strong>
                      <span className="cell-secondary">{item.instance_id}</span>
                    </td>
                    <td>
                      <span>{item.workflow_type}</span>
                      <span className="cell-secondary">v{item.workflow_version}</span>
                    </td>
                    <td>
                      <span>{item.task_type}</span>
                      <span className="cell-secondary">{item.node_id}</span>
                    </td>
                    <td>{item.assignee_id || item.candidate_group || "Unassigned"}</td>
                    <td>{formatDeadline(item.sla_deadline_epoch_ms)}</td>
                    <td><StatusBadge status={item.status} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        <footer className="pagination">
          <span>{selectedItems.length} selected</span>
          <div>
            <IconButton
              icon={ArrowLeft}
              label="Previous page"
              disabled={pageTokens.length === 1}
              onClick={() => {
                setPageTokens((tokens) => tokens.slice(0, -1));
                setSelected(new Set());
              }}
            />
            <span>Page {pageTokens.length}</span>
            <IconButton
              icon={ArrowRight}
              label="Next page"
              disabled={!query.data?.next_page_token}
              onClick={() => {
                const next = query.data?.next_page_token;
                if (next) setPageTokens((tokens) => [...tokens, next]);
                setSelected(new Set());
              }}
            />
          </div>
        </footer>
      </section>

      <Dialog
        open={batchAction !== null}
        title={batchAction === "complete" ? "Complete work items" : "Delegate work items"}
        description={`${selectedItems.length} selected items`}
        onClose={() => setBatchAction(null)}
        footer={
          <>
            <Button onClick={() => setBatchAction(null)}>Cancel</Button>
            <Button
              variant="primary"
              icon={batchAction === "complete" ? CheckCircle2 : Send}
              type="submit"
              form="batch-form"
              disabled={
                batchMutation.isPending ||
                (batchAction === "complete" ? !decision.trim() : (!assignee.trim() && !candidateGroup.trim()))
              }
            >
              {batchMutation.isPending ? "Processing" : "Apply"}
            </Button>
          </>
        }
      >
        <form id="batch-form" onSubmit={submitBatch}>
          {batchAction === "complete" ? (
            <div className="field">
              <label htmlFor="decision">Decision</label>
              <input id="decision" value={decision} onChange={(event) => setDecision(event.target.value)} required />
            </div>
          ) : (
            <div className="field-grid">
              <div className="field">
                <label htmlFor="assignee">Assignee ID</label>
                <input id="assignee" value={assignee} onChange={(event) => {
                  setAssignee(event.target.value);
                  if (event.target.value) setCandidateGroup("");
                }} />
              </div>
              <div className="field">
                <label htmlFor="group">Candidate group</label>
                <input id="group" value={candidateGroup} onChange={(event) => {
                  setCandidateGroup(event.target.value);
                  if (event.target.value) setAssignee("");
                }} />
              </div>
            </div>
          )}
          {progress ? (
            <div className="batch-progress" role="status">
              <div style={{ width: `${(progress.processed / progress.total) * 100}%` }} />
              <span>{progress.processed} / {progress.total}</span>
            </div>
          ) : null}
        </form>
      </Dialog>
    </>
  );
}

function formatDeadline(epochMs: number): string {
  if (!epochMs) return "None";
  const difference = epochMs - Date.now();
  const minutes = Math.round(Math.abs(difference) / 60_000);
  return difference < 0 ? `${minutes}m overdue` : `in ${minutes}m`;
}
