import useSWR from "swr";
import type { RemoteRouteModule, RemoteRouteProps } from "@ods/mfe-contract";
import { ErrorPanel, LoadingPanel, Page, SimpleList, type SimpleListItem } from "@ods/shared-ui";

interface WorkItem {
  work_item_id?: string;
  id?: string;
  task_name?: string;
  node_id?: string;
  due_at_epoch_ms?: number;
  status?: string;
}

interface WorkItemPage {
  work_items?: WorkItem[];
  next_page_token?: string;
}

function PersonalWorklist({ platform }: RemoteRouteProps) {
  const query = useSWR("task-mfe:personal-worklist:first-page", async () => {
    const result = await platform.callApi<WorkItemPage>({
      method: "GET",
      path: "/v1/work-items?page_size=25",
    });
    if (!result.ok) throw new TaskApiError(result.problem.title, result.problem.code, result.problem.retryable);
    return result.data;
  }, { shouldRetryOnError: false, revalidateOnFocus: true });

  if (query.isLoading) return <Page title="Personal Worklist"><LoadingPanel label="Loading assigned tasks" /></Page>;
  if (query.error) {
    const error = query.error instanceof TaskApiError ? query.error : new TaskApiError("Task query failed", "unknown", false);
    const retry = error.retryable ? { onRetry: () => { void query.mutate(); } } : {};
    return <Page title="Personal Worklist"><ErrorPanel
      title={error.message}
      detail={`Code: ${error.code}`}
      {...retry}
    /></Page>;
  }
  const items: SimpleListItem[] = (query.data?.work_items ?? []).map((item, index) => {
    const secondary = item.status ?? formatDueDate(item.due_at_epoch_ms);
    return {
      id: item.work_item_id ?? item.id ?? `unknown-${index}`,
      primary: item.task_name ?? item.node_id ?? "Unnamed task",
      ...(secondary === undefined ? {} : { secondary }),
    };
  });
  return <Page title="Personal Worklist"><SimpleList items={items} emptyLabel="No assigned tasks" /></Page>;
}

class TaskApiError extends Error {
  constructor(message: string, readonly code: string, readonly retryable: boolean) {
    super(message);
    this.name = "TaskApiError";
  }
}

function formatDueDate(value: number | undefined): string | undefined {
  return value === undefined ? undefined : `Due ${new Date(value).toLocaleString()}`;
}

export const contractVersion = "1.0";
export const route = {
  path: "/tasks",
  title: "Tasks",
  Component: PersonalWorklist,
};

const moduleContract: RemoteRouteModule = { contractVersion, route };
export default moduleContract;
