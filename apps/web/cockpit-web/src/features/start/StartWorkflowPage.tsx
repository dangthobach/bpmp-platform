import { useMutation } from "@tanstack/react-query";
import { CheckCircle2, Play } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useApi } from "../../api/ApiContext";
import { Button } from "../../components/Button";

export function StartWorkflowPage() {
  const api = useApi();
  const [workflowType, setWorkflowType] = useState("");
  const [workflowVersion, setWorkflowVersion] = useState("");
  const [instanceId, setInstanceId] = useState("");
  const [startNodeId, setStartNodeId] = useState("");
  const mutation = useMutation({
    mutationFn: () => api.startWorkflow({ workflowType, workflowVersion, instanceId, startNodeId }),
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    mutation.mutate();
  }

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Start workflow</h1>
          <p>Create an authoritative workflow instance</p>
        </div>
      </header>
      <section className="form-surface">
        <form onSubmit={submit}>
          <div className="field-grid">
            <div className="field">
              <label htmlFor="workflow-type">Workflow type</label>
              <input id="workflow-type" value={workflowType} onChange={(event) => setWorkflowType(event.target.value)} required />
            </div>
            <div className="field">
              <label htmlFor="workflow-version">Workflow version</label>
              <input id="workflow-version" value={workflowVersion} onChange={(event) => setWorkflowVersion(event.target.value)} required />
            </div>
            <div className="field">
              <label htmlFor="instance-id">Instance ID</label>
              <input id="instance-id" value={instanceId} onChange={(event) => setInstanceId(event.target.value)} required />
            </div>
            <div className="field">
              <label htmlFor="start-node">Start node ID</label>
              <input id="start-node" value={startNodeId} onChange={(event) => setStartNodeId(event.target.value)} required />
            </div>
          </div>
          <div className="form-actions">
            <Button type="submit" variant="primary" icon={Play} disabled={mutation.isPending}>
              {mutation.isPending ? "Submitting" : "Start instance"}
            </Button>
          </div>
        </form>
        {mutation.isSuccess ? (
          <div className="result-banner" role="status">
            <CheckCircle2 size={18} />
            <div>
              <strong>Command accepted</strong>
              <span>{mutation.data.command_id}</span>
            </div>
          </div>
        ) : null}
        {mutation.isError ? <div className="form-error" role="alert">{mutation.error.message}</div> : null}
      </section>
    </>
  );
}
