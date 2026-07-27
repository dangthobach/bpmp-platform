import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useApi } from "../../api/ApiContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { StatusBadge } from "../../components/StatusBadge";

export function CasesPage() {
  const api = useApi();
  const [draft, setDraft] = useState("");
  const [caseId, setCaseId] = useState("");
  const query = useQuery({
    queryKey: ["case", caseId],
    queryFn: () => api.getCase(caseId),
    enabled: Boolean(caseId),
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    setCaseId(draft.trim());
  }

  return (
    <>
      <header className="page-header">
        <div><h1>Cases</h1><p>Inspect CMMN plan item state</p></div>
      </header>
      <form className="toolbar" onSubmit={submit}>
        <div className="search-input">
          <Search size={16} />
          <input value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="Case ID" aria-label="Case ID" required />
        </div>
        <Button type="submit" variant="primary">Open case</Button>
      </form>
      {!caseId ? <EmptyState title="Open a case" detail="Enter a case ID to load its current projection." /> : null}
      {query.isPending && caseId ? <LoadingRows /> : null}
      {query.isError ? <ErrorState message="Case could not be loaded" retry={() => void query.refetch()} /> : null}
      {query.data ? (
        <section className="case-surface">
          <header>
            <div>
              <h2>{query.data.case.case_id}</h2>
              <span>{query.data.case.case_type}</span>
            </div>
            <StatusBadge status={query.data.case.status} />
          </header>
          <div className="case-grid">
            {query.data.case.plan_items.map((item) => (
              <article key={item.plan_item_id}>
                <span>{item.kind}</span>
                <strong>{item.plan_item_id}</strong>
                <StatusBadge status={item.status} />
              </article>
            ))}
          </div>
        </section>
      ) : null}
    </>
  );
}
