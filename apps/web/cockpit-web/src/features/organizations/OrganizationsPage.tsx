import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  Building2,
  FolderInput,
  GitBranchPlus,
  Plus,
  RefreshCw,
} from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { executeBatch } from "../../api/batch";
import { useApi } from "../../api/ApiContext";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { Dialog } from "../../components/Dialog";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { useRuntimeConfig } from "../../config/ConfigContext";
import {
  childKind,
  eligibleMoveParents,
  parseNamedCodes,
} from "./organizationModel";
import type { OrganizationNode } from "./types";

export function OrganizationsPage() {
  const api = useApi();
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const queryClient = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [selectedOrganizationId, setSelectedOrganizationId] = useState("");
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [dialog, setDialog] = useState<"create" | "add" | "move" | null>(null);
  const [batchInput, setBatchInput] = useState("");
  const [moveParentId, setMoveParentId] = useState("");
  const [inputError, setInputError] = useState("");
  const [notice, setNotice] = useState("");
  const pageSize = config.defaultPageSize;

  const organizations = useQuery({
    queryKey: ["organizations", identity?.tenantId, offset, pageSize],
    queryFn: () => api.listOrganizations(offset, pageSize),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
    placeholderData: keepPreviousData,
  });

  useEffect(() => {
    const rows = organizations.data ?? [];
    if (!rows.some(({ id }) => id === selectedOrganizationId)) {
      setSelectedOrganizationId(rows[0]?.id ?? "");
    }
  }, [organizations.data, selectedOrganizationId]);

  const detail = useQuery({
    queryKey: ["organization", identity?.tenantId, selectedOrganizationId],
    queryFn: () => api.getOrganization(selectedOrganizationId),
    enabled: Boolean(identity && selectedOrganizationId),
    staleTime: config.staleTimeMs,
  });

  useEffect(() => {
    const nodes = detail.data?.nodes ?? [];
    if (!nodes.some(({ id }) => id === selectedNodeId)) {
      setSelectedNodeId(detail.data?.root_node_id ?? "");
    }
  }, [detail.data, selectedNodeId]);

  const selectedNode = detail.data?.nodes.find(({ id }) => id === selectedNodeId);
  const addKind = selectedNode ? childKind(selectedNode.kind) : null;
  const moveParents = useMemo(
    () => selectedNode && detail.data
      ? eligibleMoveParents(selectedNode, detail.data.nodes)
      : [],
    [detail.data, selectedNode],
  );

  const createMutation = useMutation({
    mutationFn: (entries: ReturnType<typeof parseNamedCodes>) =>
      executeBatch(
        entries,
        {
          chunkSize: config.batchChunkSize,
          concurrency: config.batchConcurrency,
        },
        (entry) => api.createOrganization(entry).then(() => undefined),
      ),
    async onSuccess(result) {
      setNotice(`${result.succeeded.length} created, ${result.failed.length} failed`);
      closeDialog();
      await queryClient.invalidateQueries({ queryKey: ["organizations"] });
    },
  });

  const addMutation = useMutation({
    mutationFn: async ({
      parent,
      entries,
    }: {
      parent: OrganizationNode;
      entries: ReturnType<typeof parseNamedCodes>;
    }) => {
      const kind = childKind(parent.kind);
      if (!kind || !detail.data) throw new Error("This node cannot contain children");
      let expectedVersion = detail.data.version;
      for (const entry of entries) {
        const response = await api.addOrganizationNode({
          organizationId: detail.data.id,
          parentId: parent.id,
          kind,
          ...entry,
          expectedVersion,
        });
        expectedVersion = response.version;
      }
      return entries.length;
    },
    async onSuccess(count) {
      setNotice(`${count} nodes added`);
      closeDialog();
      await refreshSelectedOrganization();
    },
    async onError() {
      await refreshSelectedOrganization();
    },
  });

  const moveMutation = useMutation({
    mutationFn: async ({
      node,
      newParentId,
    }: {
      node: OrganizationNode;
      newParentId: string;
    }) => {
      if (!detail.data) throw new Error("Organization is not loaded");
      await api.moveOrganizationNode({
        organizationId: detail.data.id,
        nodeId: node.id,
        newParentId,
        expectedVersion: detail.data.version,
      });
    },
    async onSuccess() {
      setNotice("Node moved");
      closeDialog();
      await refreshSelectedOrganization();
    },
    async onError() {
      await refreshSelectedOrganization();
    },
  });

  function closeDialog() {
    createMutation.reset();
    addMutation.reset();
    moveMutation.reset();
    setDialog(null);
    setBatchInput("");
    setMoveParentId("");
    setInputError("");
  }

  async function refreshSelectedOrganization() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["organizations"] }),
      queryClient.invalidateQueries({
        queryKey: ["organization", identity?.tenantId, selectedOrganizationId],
      }),
    ]);
  }

  function submitCreate(event: FormEvent) {
    event.preventDefault();
    try {
      const entries = parseNamedCodes(batchInput);
      if (entries.length > config.maxPageSize) {
        throw new Error(`A batch cannot exceed ${config.maxPageSize} rows`);
      }
      setInputError("");
      createMutation.mutate(entries);
    } catch (error) {
      setInputError(error instanceof Error ? error.message : "Invalid batch");
    }
  }

  function submitAdd(event: FormEvent) {
    event.preventDefault();
    if (!selectedNode) return;
    try {
      const entries = parseNamedCodes(batchInput);
      if (entries.length > config.maxPageSize) {
        throw new Error(`A batch cannot exceed ${config.maxPageSize} rows`);
      }
      setInputError("");
      addMutation.mutate({ parent: selectedNode, entries });
    } catch (error) {
      setInputError(error instanceof Error ? error.message : "Invalid batch");
    }
  }

  function submitMove(event: FormEvent) {
    event.preventDefault();
    if (selectedNode && moveParentId) {
      moveMutation.mutate({ node: selectedNode, newParentId: moveParentId });
    }
  }

  const mutationError =
    createMutation.error ?? addMutation.error ?? moveMutation.error;

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Organizations</h1>
          <p>{organizations.data?.length ?? 0} organizations on this page</p>
        </div>
        <div className="header-actions">
          <IconButton
            icon={RefreshCw}
            label="Refresh organizations"
            onClick={() => void refreshSelectedOrganization()}
            disabled={organizations.isFetching || detail.isFetching}
          />
          <Button icon={Plus} variant="primary" onClick={() => setDialog("create")}>
            Create
          </Button>
        </div>
      </header>

      {notice ? (
        <div className="inline-notice" role="status">
          <Building2 size={16} />
          <span>{notice}</span>
          <button onClick={() => setNotice("")}>Dismiss</button>
        </div>
      ) : null}

      <div className="organization-layout">
        <section className="table-surface organization-list">
          {organizations.isPending ? <LoadingRows /> : null}
          {organizations.isError ? (
            <ErrorState
              message="Organizations could not be loaded"
              retry={() => void organizations.refetch()}
            />
          ) : null}
          {organizations.isSuccess && organizations.data.length === 0 ? (
            <EmptyState title="No organizations" detail="Create the first organization." />
          ) : null}
          {organizations.data && organizations.data.length > 0 ? (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th className="selection-cell"><span className="sr-only">Select</span></th>
                    <th>Organization</th>
                    <th>Nodes</th>
                    <th>Version</th>
                  </tr>
                </thead>
                <tbody>
                  {organizations.data.map((organization) => (
                    <tr
                      key={organization.id}
                      className={selectedOrganizationId === organization.id ? "is-selected" : ""}
                      onClick={() => setSelectedOrganizationId(organization.id)}
                    >
                      <td className="selection-cell">
                        <input
                          type="radio"
                          name="organization"
                          checked={selectedOrganizationId === organization.id}
                          onChange={() => setSelectedOrganizationId(organization.id)}
                          aria-label={`Select ${organization.name}`}
                        />
                      </td>
                      <td>
                        <strong>{organization.name}</strong>
                        <span className="cell-secondary">{organization.code}</span>
                      </td>
                      <td>{organization.node_count}</td>
                      <td>{organization.version}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
          <footer className="pagination">
            <span>{offset + 1} - {offset + (organizations.data?.length ?? 0)}</span>
            <div>
              <IconButton
                icon={ArrowLeft}
                label="Previous organizations"
                disabled={offset === 0}
                onClick={() => setOffset((value) => Math.max(0, value - pageSize))}
              />
              <IconButton
                icon={ArrowRight}
                label="Next organizations"
                disabled={(organizations.data?.length ?? 0) < pageSize}
                onClick={() => setOffset((value) => value + pageSize)}
              />
            </div>
          </footer>
        </section>

        <section className="table-surface organization-tree">
          <div className="toolbar" aria-label="Organization node tools">
            <strong>{organizations.data?.find(({ id }) => id === selectedOrganizationId)?.name ?? "Structure"}</strong>
            <div className="toolbar__spacer" />
            <Button
              icon={GitBranchPlus}
              disabled={!selectedNode || !addKind}
              onClick={() => setDialog("add")}
            >
              Add child
            </Button>
            <Button
              icon={FolderInput}
              disabled={!selectedNode?.parent_id || moveParents.length === 0}
              onClick={() => setDialog("move")}
            >
              Move
            </Button>
          </div>
          {detail.isPending && selectedOrganizationId ? <LoadingRows /> : null}
          {detail.isError ? (
            <ErrorState
              message="Organization structure could not be loaded"
              retry={() => void detail.refetch()}
            />
          ) : null}
          {detail.data ? (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th className="selection-cell"><span className="sr-only">Select</span></th>
                    <th>Node</th>
                    <th>Kind</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.data.nodes.map((node) => (
                    <tr
                      key={node.id}
                      className={selectedNodeId === node.id ? "is-selected" : ""}
                      onClick={() => setSelectedNodeId(node.id)}
                    >
                      <td className="selection-cell">
                        <input
                          type="radio"
                          name="organization-node"
                          checked={selectedNodeId === node.id}
                          onChange={() => setSelectedNodeId(node.id)}
                          aria-label={`Select ${node.name}`}
                        />
                      </td>
                      <td>
                        <div
                          className="tree-node"
                          style={{ paddingLeft: `${Math.max(0, node.path.split(".").length - 1) * 16}px` }}
                        >
                          <strong>{node.name}</strong>
                          <span className="cell-secondary">{node.code}</span>
                        </div>
                      </td>
                      <td>{formatKind(node.kind)}</td>
                      <td>{node.is_active ? "Active" : "Inactive"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </section>
      </div>

      <Dialog
        open={dialog === "create"}
        title="Create organizations"
        description="Batch"
        onClose={closeDialog}
        footer={
          <>
            <Button onClick={closeDialog}>Cancel</Button>
            <Button
              variant="primary"
              type="submit"
              form="create-organizations-form"
              disabled={createMutation.isPending}
            >
              {createMutation.isPending ? "Creating" : "Create"}
            </Button>
          </>
        }
      >
        <form id="create-organizations-form" onSubmit={submitCreate}>
          <BatchField value={batchInput} onChange={setBatchInput} />
          <MutationFeedback inputError={inputError} mutationError={mutationError} />
        </form>
      </Dialog>

      <Dialog
        open={dialog === "add"}
        title={`Add ${addKind ? formatKind(addKind) : "child"} nodes`}
        {...(selectedNode ? { description: selectedNode.name } : {})}
        onClose={closeDialog}
        footer={
          <>
            <Button onClick={closeDialog}>Cancel</Button>
            <Button
              variant="primary"
              type="submit"
              form="add-organization-nodes-form"
              disabled={addMutation.isPending}
            >
              {addMutation.isPending ? "Adding" : "Add"}
            </Button>
          </>
        }
      >
        <form id="add-organization-nodes-form" onSubmit={submitAdd}>
          <BatchField value={batchInput} onChange={setBatchInput} />
          <MutationFeedback inputError={inputError} mutationError={mutationError} />
        </form>
      </Dialog>

      <Dialog
        open={dialog === "move"}
        title="Move organization node"
        {...(selectedNode ? { description: selectedNode.name } : {})}
        onClose={closeDialog}
        footer={
          <>
            <Button onClick={closeDialog}>Cancel</Button>
            <Button
              variant="primary"
              type="submit"
              form="move-organization-node-form"
              disabled={moveMutation.isPending || !moveParentId}
            >
              {moveMutation.isPending ? "Moving" : "Move"}
            </Button>
          </>
        }
      >
        <form id="move-organization-node-form" onSubmit={submitMove}>
          <div className="field">
            <label htmlFor="move-parent">New parent</label>
            <select
              id="move-parent"
              value={moveParentId}
              onChange={(event) => setMoveParentId(event.target.value)}
              required
            >
              <option value="">Select parent</option>
              {moveParents.map((parent) => (
                <option key={parent.id} value={parent.id}>
                  {parent.name} ({parent.code})
                </option>
              ))}
            </select>
          </div>
          <MutationFeedback inputError={inputError} mutationError={mutationError} />
        </form>
      </Dialog>
    </>
  );
}

function BatchField({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="field">
      <label htmlFor="organization-batch">Code and name</label>
      <textarea
        id="organization-batch"
        rows={8}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={"APAC,Asia Pacific\nEMEA,Europe and Middle East"}
        required
      />
    </div>
  );
}

function MutationFeedback({
  inputError,
  mutationError,
}: {
  inputError: string;
  mutationError: Error | null;
}) {
  const message = inputError || mutationError?.message;
  return message ? <div className="form-error" role="alert">{message}</div> : null;
}

function formatKind(kind: string): string {
  return kind.charAt(0) + kind.slice(1).toLowerCase();
}
