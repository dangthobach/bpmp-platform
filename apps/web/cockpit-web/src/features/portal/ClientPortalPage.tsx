import { useQuery } from "@tanstack/react-query";
import { ArrowRight, ClipboardList, Play, RefreshCw, Workflow } from "lucide-react";
import { useApi } from "../../api/ApiContext";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { ErrorState, LoadingRows } from "../../components/Feedback";
import { StatusBadge } from "../../components/StatusBadge";
import { useRuntimeConfig } from "../../config/ConfigContext";
import { AppLink } from "../../routing/router";

export function ClientPortalPage() {
  const api = useApi();
  const { identity } = useAuth();
  const config = useRuntimeConfig();
  const workItems = useQuery({
    queryKey: ["work-items", identity?.tenantId, "portal", "ASSIGNED_TO_ME", config.defaultPageSize],
    queryFn: () => api.listWorkItems("", config.defaultPageSize, "ASSIGNED_TO_ME"),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
  });
  const items = workItems.data?.work_items ?? [];

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Client Portal</h1>
          <p>Start a workflow or continue work assigned to your authenticated account.</p>
        </div>
        <div className="header-actions">
          <Button icon={RefreshCw} onClick={() => void workItems.refetch()} disabled={workItems.isFetching}>Refresh</Button>
          <AppLink className="button button--primary" to="/start"><Play size={15} /> Start workflow</AppLink>
        </div>
      </header>

      <section className="portal-summary" aria-label="Workspace summary">
        <article className="portal-summary__card">
          <span className="portal-summary__icon"><ClipboardList size={18} /></span>
          <div><span>Tasks in current page</span><strong>{workItems.isPending ? "—" : items.length}</strong></div>
        </article>
        <article className="portal-summary__card">
          <span className="portal-summary__icon"><Workflow size={18} /></span>
          <div><span>Tenant</span><strong className="portal-summary__tenant">{identity?.tenantId ?? "—"}</strong></div>
        </article>
        <AppLink className="portal-summary__action" to="/tasklist">
          <span><strong>Open tasklist</strong><small>Review, complete, or delegate work</small></span><ArrowRight size={18} />
        </AppLink>
      </section>

      <section className="table-surface portal-work-items">
        <header className="portal-section-heading">
          <div><h2>My work</h2><p>Live work items from Human Runtime</p></div>
          <AppLink className="button button--secondary" to="/tasklist">View tasklist <ArrowRight size={14} /></AppLink>
        </header>
        {workItems.isPending ? <LoadingRows /> : null}
        {workItems.isError ? <ErrorState message={workItems.error.message} retry={() => void workItems.refetch()} /> : null}
        {workItems.isSuccess && items.length === 0 ? <div className="empty-state"><ClipboardList size={26} /><strong>No work items found</strong><span>The service returned no items for this tenant.</span></div> : null}
        {items.length > 0 ? (
          <div className="table-scroll">
            <table>
              <thead><tr><th>Work item</th><th>Workflow</th><th>Task</th><th>Assignee / group</th><th>Status</th></tr></thead>
              <tbody>{items.slice(0, 8).map((item) => (
                <tr key={item.work_item_id}>
                  <td className="mono">{item.work_item_id}<span className="cell-secondary">{item.instance_id}</span></td>
                  <td>{item.workflow_type}</td>
                  <td>{item.task_type}</td>
                  <td>{item.assignee_id || item.candidate_group || "Unassigned"}</td>
                  <td><StatusBadge status={item.status} /></td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        ) : null}
      </section>
    </>
  );
}
