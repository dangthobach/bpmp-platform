import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  FilePlus2,
  Plus,
  RefreshCw,
  RotateCcw,
  SlidersHorizontal,
  Upload,
} from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useApi } from "../../api/ApiContext";
import { executeBatch } from "../../api/batch";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { Dialog } from "../../components/Dialog";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { IconButton } from "../../components/IconButton";
import { StatusBadge } from "../../components/StatusBadge";
import { useRuntimeConfig } from "../../config/ConfigContext";
import {
  buildPolicy,
  emptyPolicyForm,
  policyFieldsFor,
  policyToForm,
  type PolicyForm,
} from "./policyForm";
import type {
  ConfigurationProfile,
  ConfigurationOwner,
  ConfigurationScopeType,
  ConfigurationVersion,
} from "./types";

type DialogType = "create" | "draft" | "publish" | "batchPublish" | "rollback" | null;
const scopeTypes: readonly ConfigurationScopeType[] = [
  "PLATFORM",
  "ENVIRONMENT",
  "TENANT",
  "WORKFLOW_TYPE",
  "WORKFLOW_VERSION",
  "APPROVED_INSTANCE_OVERRIDE",
];
const configurationOwners: readonly ConfigurationOwner[] = [
  "ENGINE",
  "API_GATEWAY",
  "HUMAN_RUNTIME",
  "PROJECTION",
  "GOVERNANCE",
];

export function ConfigurationPage() {
  const api = useApi();
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const queryClient = useQueryClient();
  const [pageTokens, setPageTokens] = useState<string[]>([""]);
  const pageToken = pageTokens.at(-1) ?? "";
  const [selectedProfileId, setSelectedProfileId] = useState("");
  const [dialog, setDialog] = useState<DialogType>(null);
  const [name, setName] = useState("");
  const [owner, setOwner] = useState<ConfigurationOwner>("ENGINE");
  const [scopeType, setScopeType] = useState<ConfigurationScopeType>("TENANT");
  const [scopeReference, setScopeReference] = useState(identity?.tenantId ?? "");
  const [schemaVersion, setSchemaVersion] = useState("1");
  const [policyVersion, setPolicyVersion] = useState("");
  const [reason, setReason] = useState("");
  const [policyForm, setPolicyForm] = useState<PolicyForm>(emptyPolicyForm);
  const [rollbackVersionId, setRollbackVersionId] = useState("");
  const [formError, setFormError] = useState("");
  const [notice, setNotice] = useState("");
  const [idempotencyKey, setIdempotencyKey] = useState("");
  const [batchSelectedIds, setBatchSelectedIds] = useState<Set<string>>(new Set());

  const profiles = useQuery({
    queryKey: ["configuration-profiles", identity?.tenantId, pageToken, config.defaultPageSize],
    queryFn: () => api.listConfigurationProfiles(pageToken, config.defaultPageSize),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
    placeholderData: keepPreviousData,
  });

  useEffect(() => {
    const rows = profiles.data?.profiles ?? [];
    if (!rows.some(({ id }) => id === selectedProfileId)) {
      setSelectedProfileId(rows[0]?.id ?? "");
    }
  }, [profiles.data, selectedProfileId]);

  useEffect(() => {
    setBatchSelectedIds(new Set());
  }, [pageToken]);

  const detail = useQuery({
    queryKey: ["configuration-profile", identity?.tenantId, selectedProfileId],
    queryFn: () => api.getConfigurationProfile(selectedProfileId),
    enabled: Boolean(identity && selectedProfileId),
    staleTime: config.staleTimeMs,
  });

  const latestDraft = detail.data?.versions.find(({ status }) => status === "DRAFT");
  const rollbackCandidates = detail.data?.versions.filter(
    ({ status, id }) => status !== "DRAFT" && id !== detail.data.profile.current_published_version_id,
  ) ?? [];

  const createMutation = useMutation({
    mutationFn: (key: string) => api.createConfigurationProfile({
      name: name.trim(),
      owner,
      scope: { type: scopeType, reference: scopeReference.trim() },
      schema_version: positive(schemaVersion, "Schema version"),
      policy_version: policyVersion.trim(),
      reason: reason.trim(),
      values: buildPolicy(policyForm, owner),
    }, key),
    async onSuccess(profile) {
      setSelectedProfileId(profile.id);
      setNotice("Configuration draft created");
      closeDialog();
      await refresh();
    },
  });

  const draftMutation = useMutation({
    mutationFn: (key: string) => {
      if (!detail.data) throw new Error("Configuration is not loaded");
      return api.addConfigurationDraft(detail.data.profile.id, {
        expected_version: detail.data.profile.aggregate_version,
        schema_version: positive(schemaVersion, "Schema version"),
        policy_version: policyVersion.trim(),
        reason: reason.trim(),
        values: buildPolicy(policyForm, detail.data.profile.owner),
      }, key);
    },
    async onSuccess() {
      setNotice("New draft created");
      closeDialog();
      await refresh();
    },
  });

  const publishMutation = useMutation({
    mutationFn: (key: string) => {
      if (!detail.data || !latestDraft) throw new Error("No draft is available");
      return api.publishConfiguration(
        detail.data.profile.id,
        latestDraft.id,
        detail.data.profile.aggregate_version,
        reason.trim(),
        key,
      );
    },
    async onSuccess() {
      setNotice("Configuration published");
      closeDialog();
      await refresh();
    },
  });

  const rollbackMutation = useMutation({
    mutationFn: (key: string) => {
      if (!detail.data || !rollbackVersionId) throw new Error("Rollback version is required");
      return api.rollbackConfiguration(
        detail.data.profile.id,
        rollbackVersionId,
        detail.data.profile.aggregate_version,
        policyVersion.trim(),
        reason.trim(),
        key,
      );
    },
    async onSuccess() {
      setNotice("Rollback published as a new version");
      closeDialog();
      await refresh();
    },
  });

  const batchPublishMutation = useMutation({
    mutationFn: async (batchKey: string) => {
      const selected = (profiles.data?.profiles ?? [])
        .filter(({ id, latest }) => batchSelectedIds.has(id) && latest?.status === "DRAFT")
        .map((profile) => ({
          profile,
          versionId: profile.latest!.id,
          idempotencyKey: `${batchKey}:${profile.id}`,
        }));
      if (selected.length === 0) throw new Error("Select at least one draft");
      return executeBatch(
        selected,
        { chunkSize: config.batchChunkSize, concurrency: config.batchConcurrency },
        async ({ profile, versionId, idempotencyKey: key }) => {
          await api.publishConfiguration(
            profile.id,
            versionId,
            profile.aggregate_version,
            reason.trim(),
            key,
          );
        },
      );
    },
    async onSuccess(result) {
      setBatchSelectedIds(new Set(result.failed.map(({ item }) => item.profile.id)));
      setNotice(
        result.failed.length === 0
          ? `${result.succeeded.length} configurations published`
          : `${result.succeeded.length} published, ${result.failed.length} require attention`,
      );
      closeDialog();
      await refresh();
    },
  });

  const activeMutation = createMutation.isPending || draftMutation.isPending ||
    publishMutation.isPending || rollbackMutation.isPending || batchPublishMutation.isPending;
  const mutationError = createMutation.error ?? draftMutation.error ??
    publishMutation.error ?? rollbackMutation.error ?? batchPublishMutation.error;

  function openCreate() {
    resetForm();
    setScopeType("TENANT");
    setScopeReference(identity?.tenantId ?? "");
    setDialog("create");
  }

  function openDraft() {
    const source = detail.data?.versions[0];
    const profileOwner = detail.data?.profile.owner ?? "ENGINE";
    resetForm();
    setOwner(profileOwner);
    if (source) {
      setSchemaVersion(String(source.schema_version));
      setPolicyVersion(source.policy_version);
      setPolicyForm(policyToForm(source.values, profileOwner));
    }
    setDialog("draft");
  }

  function closeDialog() {
    createMutation.reset();
    draftMutation.reset();
    publishMutation.reset();
    rollbackMutation.reset();
    batchPublishMutation.reset();
    setDialog(null);
    setFormError("");
  }

  function resetForm() {
    setName("");
    setOwner("ENGINE");
    setSchemaVersion("1");
    setPolicyVersion("");
    setReason("");
    setPolicyForm(emptyPolicyForm("ENGINE"));
    setRollbackVersionId("");
    setFormError("");
    setIdempotencyKey("");
  }

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["configuration-profiles"] }),
      queryClient.invalidateQueries({ queryKey: ["configuration-profile"] }),
    ]);
  }

  function submitPolicy(event: FormEvent) {
    event.preventDefault();
    try {
      if (!policyVersion.trim() || !reason.trim() || (dialog === "create" && !name.trim())) {
        throw new Error("Profile metadata is required");
      }
      buildPolicy(policyForm, dialog === "draft" ? detail.data?.profile.owner ?? owner : owner);
      setFormError("");
      const key = idempotencyKey || crypto.randomUUID();
      setIdempotencyKey(key);
      if (dialog === "create") createMutation.mutate(key);
      if (dialog === "draft") draftMutation.mutate(key);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Invalid configuration");
    }
  }

  function submitTransition(event: FormEvent) {
    event.preventDefault();
    if (!reason.trim()) {
      setFormError("Reason is required");
      return;
    }
    const key = idempotencyKey || crypto.randomUUID();
    setIdempotencyKey(key);
    if (dialog === "publish") publishMutation.mutate(key);
    if (dialog === "batchPublish") batchPublishMutation.mutate(key);
    if (dialog === "rollback") {
      if (!rollbackVersionId || !policyVersion.trim()) {
        setFormError("Version and policy version are required");
        return;
      }
      rollbackMutation.mutate(key);
    }
  }

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Configuration</h1>
          <p>{profiles.data?.profiles.length ?? 0} profiles on this page</p>
        </div>
        <div className="header-actions">
          <IconButton
            icon={RefreshCw}
            label="Refresh configuration"
            onClick={() => void refresh()}
            disabled={profiles.isFetching || detail.isFetching}
          />
          <Button
            icon={Upload}
            disabled={batchSelectedIds.size === 0}
            onClick={() => {
              setReason("");
              setDialog("batchPublish");
            }}
          >
            Publish selected ({batchSelectedIds.size})
          </Button>
          <Button icon={Plus} variant="primary" onClick={openCreate}>Create profile</Button>
        </div>
      </header>

      {notice ? (
        <div className="inline-notice" role="status">
          <SlidersHorizontal size={16} />
          <span>{notice}</span>
          <button onClick={() => setNotice("")}>Dismiss</button>
        </div>
      ) : null}

      <div className="configuration-layout">
        <ProfileList
          profiles={profiles.data?.profiles ?? []}
          selectedId={selectedProfileId}
          onSelect={setSelectedProfileId}
          batchSelectedIds={batchSelectedIds}
          onBatchSelectionChange={setBatchSelectedIds}
          loading={profiles.isPending}
          error={profiles.isError}
          retry={() => void profiles.refetch()}
          page={pageTokens.length}
          hasNext={Boolean(profiles.data?.next_page_token)}
          previous={() => setPageTokens((tokens) => tokens.slice(0, -1))}
          next={() => {
            const nextToken = profiles.data?.next_page_token;
            if (nextToken) setPageTokens((tokens) => [...tokens, nextToken]);
          }}
        />

        <section className="table-surface configuration-detail">
          <div className="toolbar" aria-label="Configuration tools">
            <strong>{detail.data?.profile.name ?? "Version history"}</strong>
            <div className="toolbar__spacer" />
            <Button icon={FilePlus2} disabled={!detail.data || Boolean(latestDraft)} onClick={openDraft}>
              New draft
            </Button>
            <Button icon={Upload} disabled={!latestDraft} onClick={() => {
              setReason("");
              setDialog("publish");
            }}>
              Publish
            </Button>
            <Button icon={RotateCcw} disabled={rollbackCandidates.length === 0} onClick={() => {
              setReason("");
              setPolicyVersion("");
              setRollbackVersionId("");
              setDialog("rollback");
            }}>
              Rollback
            </Button>
          </div>
          {detail.isPending && selectedProfileId ? <LoadingRows /> : null}
          {detail.isError ? (
            <ErrorState message="Configuration history could not be loaded" retry={() => void detail.refetch()} />
          ) : null}
          {detail.data ? <VersionTable versions={detail.data.versions} /> : null}
        </section>
      </div>

      <Dialog
        open={dialog === "create" || dialog === "draft"}
        title={dialog === "create" ? "Create configuration profile" : "Create configuration draft"}
        onClose={closeDialog}
        size="wide"
        footer={
          <>
            <Button onClick={closeDialog}>Cancel</Button>
            <Button variant="primary" type="submit" form="configuration-policy-form" disabled={activeMutation}>
              {activeMutation ? "Saving" : "Save draft"}
            </Button>
          </>
        }
      >
        <form id="configuration-policy-form" onSubmit={submitPolicy} className="configuration-form">
          <div className="field-grid">
            {dialog === "create" ? (
              <>
                <TextField label="Profile name" value={name} onChange={setName} />
                <SelectField
                  label="Owner"
                  value={owner}
                  options={configurationOwners}
                  onChange={(value) => {
                    const nextOwner = value as ConfigurationOwner;
                    setOwner(nextOwner);
                    setPolicyForm(emptyPolicyForm(nextOwner));
                  }}
                />
                <SelectField
                  label="Scope type"
                  value={scopeType}
                  options={scopeTypes}
                  onChange={(value) => setScopeType(value as ConfigurationScopeType)}
                />
                <TextField label="Scope reference" value={scopeReference} onChange={setScopeReference} />
              </>
            ) : null}
            <TextField label="Schema version" value={schemaVersion} onChange={setSchemaVersion} type="number" />
            <TextField label="Policy version" value={policyVersion} onChange={setPolicyVersion} />
            <TextField label="Reason" value={reason} onChange={setReason} />
          </div>
          {[...new Set(policyFieldsFor(owner).map(({ group }) => group))].map((group) => (
            <fieldset key={group} className="configuration-fields">
              <legend>{group}</legend>
              <div className="field-grid">
                {policyFieldsFor(owner).filter((field) => field.group === group).map((field) => (
                  <TextField
                    key={field.key}
                    label={field.label}
                    value={policyForm[field.key] ?? ""}
                    onChange={(value) => setPolicyForm((current) => ({ ...current, [field.key]: value }))}
                    type={field.key.includes("key_scope") || field.key.includes("worker_id") ? "text" : "number"}
                  />
                ))}
              </div>
            </fieldset>
          ))}
          <MutationError local={formError} remote={mutationError} />
        </form>
      </Dialog>

      <Dialog
        open={dialog === "publish" || dialog === "batchPublish" || dialog === "rollback"}
        title={
          dialog === "batchPublish"
            ? `Publish ${batchSelectedIds.size} configurations`
            : dialog === "publish"
              ? "Publish configuration"
              : "Rollback configuration"
        }
        onClose={closeDialog}
        footer={
          <>
            <Button onClick={closeDialog}>Cancel</Button>
            <Button
              variant="primary"
              type="submit"
              form="configuration-transition-form"
              disabled={activeMutation}
            >
              {activeMutation
                ? "Processing"
                : dialog === "rollback"
                  ? "Rollback"
                  : "Publish"}
            </Button>
          </>
        }
      >
        <form id="configuration-transition-form" onSubmit={submitTransition}>
          {dialog === "rollback" ? (
            <div className="field-grid">
              <SelectField
                label="Known version"
                value={rollbackVersionId}
                options={rollbackCandidates.map(({ id, ordinal }) => ({ value: id, label: `Version ${ordinal}` }))}
                onChange={setRollbackVersionId}
              />
              <TextField label="New policy version" value={policyVersion} onChange={setPolicyVersion} />
            </div>
          ) : null}
          <div className="field transition-reason">
            <label htmlFor="configuration-reason">Reason</label>
            <textarea id="configuration-reason" rows={4} value={reason} onChange={(event) => setReason(event.target.value)} required />
          </div>
          <MutationError local={formError} remote={mutationError} />
        </form>
      </Dialog>
    </>
  );
}

function ProfileList({
  profiles,
  selectedId,
  onSelect,
  batchSelectedIds,
  onBatchSelectionChange,
  loading,
  error,
  retry,
  page,
  hasNext,
  previous,
  next,
}: {
  profiles: ConfigurationProfile[];
  selectedId: string;
  onSelect: (id: string) => void;
  batchSelectedIds: Set<string>;
  onBatchSelectionChange: (ids: Set<string>) => void;
  loading: boolean;
  error: boolean;
  retry: () => void;
  page: number;
  hasNext: boolean;
  previous: () => void;
  next: () => void;
}) {
  const draftProfiles = profiles.filter(({ latest }) => latest?.status === "DRAFT");
  const allDraftsSelected = draftProfiles.length > 0 &&
    draftProfiles.every(({ id }) => batchSelectedIds.has(id));

  function toggleProfile(profile: ConfigurationProfile) {
    const next = new Set(batchSelectedIds);
    if (next.has(profile.id)) next.delete(profile.id);
    else next.add(profile.id);
    onBatchSelectionChange(next);
  }

  return (
    <section className="table-surface configuration-profiles">
      {loading ? <LoadingRows /> : null}
      {error ? <ErrorState message="Configuration profiles could not be loaded" retry={retry} /> : null}
      {!loading && !error && profiles.length === 0 ? (
        <EmptyState title="No configuration profiles" detail="Create the first versioned profile." />
      ) : null}
      {profiles.length > 0 ? (
        <div className="table-scroll">
          <table>
            <thead><tr><th className="selection-cell">
              <input
                type="checkbox"
                checked={allDraftsSelected}
                disabled={draftProfiles.length === 0}
                onChange={() => onBatchSelectionChange(
                  allDraftsSelected
                    ? new Set()
                    : new Set(draftProfiles.map(({ id }) => id)),
                )}
                aria-label="Select all drafts on this page"
              />
            </th><th>Profile</th><th>Status</th></tr></thead>
            <tbody>
              {profiles.map((profile) => (
                <tr key={profile.id} className={selectedId === profile.id ? "is-selected" : ""} onClick={() => onSelect(profile.id)}>
                  <td className="selection-cell">
                    <input
                      type="checkbox"
                      checked={batchSelectedIds.has(profile.id)}
                      disabled={profile.latest?.status !== "DRAFT"}
                      onClick={(event) => event.stopPropagation()}
                      onChange={() => toggleProfile(profile)}
                      aria-label={`Select ${profile.name} for publishing`}
                    />
                  </td>
                  <td><strong>{profile.name}</strong><span className="cell-secondary">{formatScope(profile)}</span></td>
                  <td><StatusBadge status={profile.latest?.status ?? "empty"} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      <footer className="pagination">
        <span>Page {page}</span>
        <div>
          <IconButton icon={ArrowLeft} label="Previous profiles" disabled={page === 1} onClick={previous} />
          <IconButton icon={ArrowRight} label="Next profiles" disabled={!hasNext} onClick={next} />
        </div>
      </footer>
    </section>
  );
}

function VersionTable({ versions }: { versions: ConfigurationVersion[] }) {
  return (
    <div className="table-scroll">
      <table>
        <thead><tr><th>Version</th><th>Policy</th><th>Status</th><th>Created by</th><th>Reason</th></tr></thead>
        <tbody>
          {versions.map((version) => (
            <tr key={version.id}>
              <td><strong>v{version.ordinal}</strong><span className="cell-secondary mono">{version.config_version}</span></td>
              <td>{version.policy_version}</td>
              <td><StatusBadge status={version.status} /></td>
              <td>{version.created_by}</td>
              <td>{version.reason}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  type = "text",
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: "text" | "number";
}) {
  const id = `configuration-${label.toLowerCase().replaceAll(/[^a-z0-9]+/g, "-")}`;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input id={id} type={type} min={type === "number" ? 1 : undefined} value={value} onChange={(event) => onChange(event.target.value)} required />
    </div>
  );
}

function SelectField({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: readonly (string | { value: string; label: string })[];
  onChange: (value: string) => void;
}) {
  const id = `configuration-${label.toLowerCase().replaceAll(/[^a-z0-9]+/g, "-")}`;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <select id={id} value={value} onChange={(event) => onChange(event.target.value)} required>
        <option value="">Select</option>
        {options.map((option) => {
          const item = typeof option === "string" ? { value: option, label: option } : option;
          return <option key={item.value} value={item.value}>{item.label}</option>;
        })}
      </select>
    </div>
  );
}

function MutationError({ local, remote }: { local: string; remote: Error | null }) {
  const message = local || remote?.message;
  return message ? <div className="form-error" role="alert">{message}</div> : null;
}

function positive(raw: string, label: string): number {
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${label} must be positive`);
  return value;
}

function formatScope(profile: ConfigurationProfile): string {
  return `${profile.owner.replaceAll("_", " ").toLowerCase()} · ${profile.scope.type.replaceAll("_", " ").toLowerCase()} · ${profile.scope.reference}`;
}
