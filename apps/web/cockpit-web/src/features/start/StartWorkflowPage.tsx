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
  const [variablesJson, setVariablesJson] = useState("{}");
  const [variablesError, setVariablesError] = useState("");
  const mutation = useMutation({
    mutationFn: (variables: Record<string, unknown>) => api.startWorkflow({ workflowType, workflowVersion, instanceId, startNodeId, variables }),
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    let variables: unknown;
    try {
      variables = JSON.parse(variablesJson);
    } catch {
      setVariablesError("Initial variables must be valid JSON.");
      return;
    }
    if (!variables || typeof variables !== "object" || Array.isArray(variables)) {
      setVariablesError("Initial variables must be a JSON object keyed by variable name.");
      return;
    }
    setVariablesError("");
    mutation.mutate(variables as Record<string, unknown>);
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
          <div className="field">
            <label htmlFor="initial-variables">Initial variables (JSON object)</label>
            <textarea
              id="initial-variables"
              value={variablesJson}
              onChange={(event) => { setVariablesJson(event.target.value); setVariablesError(""); }}
              rows={8}
              spellCheck={false}
              aria-invalid={Boolean(variablesError)}
              aria-describedby="initial-variables-help"
            />
            <span id="initial-variables-help" className="field__hint">Values support strings, integers, booleans and nested lists of these types. Gateway validates variable count, size, names and value types.</span>
            {variablesError ? <span className="field__hint" role="alert">{variablesError}</span> : null}
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
