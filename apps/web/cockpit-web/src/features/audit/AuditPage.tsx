import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, Search } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useApi } from "../../api/ApiContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { useRuntimeConfig } from "../../config/ConfigContext";

export function AuditPage() {
  const api = useApi();
  const config = useRuntimeConfig();
  const [draftWorkItem, setDraftWorkItem] = useState("");
  const [draftCase, setDraftCase] = useState("");
  const [filters, setFilters] = useState({ workItemId: "", caseId: "" });
  const [tokens, setTokens] = useState([""]);
  const token = tokens.at(-1) ?? "";
  const query = useQuery({
    queryKey: ["audit", filters, token],
    queryFn: () => api.listAuditRecords(filters, token, config.defaultPageSize),
    placeholderData: keepPreviousData,
    staleTime: config.staleTimeMs,
  });

  function apply(event: FormEvent) {
    event.preventDefault();
    setFilters({ workItemId: draftWorkItem.trim(), caseId: draftCase.trim() });
    setTokens([""]);
  }

  return (
    <>
      <header className="page-header">
        <div><h1>Audit</h1><p>Append-only human workflow history</p></div>
      </header>
      <form className="toolbar" onSubmit={apply}>
        <div className="search-input">
          <Search size={16} />
          <input value={draftWorkItem} onChange={(event) => setDraftWorkItem(event.target.value)} placeholder="Work item ID" aria-label="Work item ID" />
        </div>
        <div className="search-input">
          <Search size={16} />
          <input value={draftCase} onChange={(event) => setDraftCase(event.target.value)} placeholder="Case ID" aria-label="Case ID" />
        </div>
        <Button type="submit" variant="primary">Apply</Button>
      </form>
      <section className="table-surface">
        {query.isPending ? <LoadingRows /> : null}
        {query.isError ? <ErrorState message="Audit records could not be loaded" retry={() => void query.refetch()} /> : null}
        {query.isSuccess && query.data.records.length === 0 ? <EmptyState title="No audit records" detail="No records match the current filters." /> : null}
        {query.data?.records.length ? (
          <div className="table-scroll">
            <table>
              <thead><tr><th>Time</th><th>Action</th><th>Actor</th><th>Resource</th><th>Version</th><th>Correlation</th></tr></thead>
              <tbody>
                {query.data.records.map((record) => (
                  <tr key={record.audit_id}>
                    <td>{new Date(record.occurred_at_epoch_ms).toLocaleString()}</td>
                    <td><strong>{record.action}</strong></td>
                    <td>{record.actor_id}</td>
                    <td>{record.work_item_id || record.case_id}</td>
                    <td>{record.from_version} → {record.to_version}</td>
                    <td className="mono">{record.correlation_id}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        <footer className="pagination">
          <span>Page {tokens.length}</span>
          <div>
            <IconButton icon={ArrowLeft} label="Previous page" disabled={tokens.length === 1} onClick={() => setTokens((value) => value.slice(0, -1))} />
            <IconButton icon={ArrowRight} label="Next page" disabled={!query.data?.next_page_token} onClick={() => {
              const next = query.data?.next_page_token;
              if (next) setTokens((value) => [...value, next]);
            }} />
          </div>
        </footer>
      </section>
    </>
  );
}
