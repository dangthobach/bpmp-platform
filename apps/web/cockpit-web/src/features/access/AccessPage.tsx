import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, CirclePlus, GitBranch, LockKeyhole, RefreshCw, ShieldCheck, Upload } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useApi } from "../../api/ApiContext";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { StatusBadge } from "../../components/StatusBadge";
import { useRuntimeConfig } from "../../config/ConfigContext";
import { authorizationCatalogEntrySchema, policyGrantSchema, type AuthorizationCatalogEntry, type AuthorizationCatalogVersion } from "./types";

type PolicyDraft = {
  id: string;
  role: string;
  actorIds: string;
  catalogEntryKey: string;
  effect: "ALLOW" | "DENY";
  branchIds: string[];
  branchIncludesDescendants: boolean;
  queueIds: string[];
  ownScope: boolean;
  resourceIds: string[];
  tenantScope: boolean;
  readableFields: string[];
  writableFields: string[];
};

function entryKey(entry: AuthorizationCatalogEntry) {
  return `${entry.owner_id}/${entry.resource_type}/${entry.action}/${entry.capability}`;
}

function newGrant(): PolicyDraft {
  return {
    id: crypto.randomUUID(), role: "", actorIds: "", catalogEntryKey: "", effect: "ALLOW",
    branchIds: [], branchIncludesDescendants: true, queueIds: [], ownScope: false,
    resourceIds: [], tenantScope: false, readableFields: [], writableFields: [],
  };
}

export function AccessPage() {
  const api = useApi();
  const config = useRuntimeConfig();
  const { identity } = useAuth();
  const queryClient = useQueryClient();
  const [enabledEntries, setEnabledEntries] = useState<Set<string>>(new Set());
  const [grants, setGrants] = useState<PolicyDraft[]>([]);
  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [selectedOrganizationId, setSelectedOrganizationId] = useState("");
  const [editingDraft, setEditingDraft] = useState<AuthorizationCatalogVersion | null>(null);
  const [publishReason, setPublishReason] = useState("");
  const [draftReason, setDraftReason] = useState("");
  const [notice, setNotice] = useState("");
  const [formError, setFormError] = useState("");

  useEffect(() => {
    setSelectedOrganizationId("");
    setSelectedVersionId("");
    setEditingDraft(null);
    setEnabledEntries(new Set());
    setGrants([]);
    setDraftReason("");
    setPublishReason("");
  }, [identity?.tenantId]);

  const ownerEntries = useQuery({
    queryKey: ["authz-owner-catalog", identity?.tenantId],
    queryFn: () => api.listAuthorizationCatalogEntries(),
    enabled: Boolean(identity), staleTime: config.staleTimeMs,
  });
  const versions = useQuery({
    queryKey: ["authz-catalog-versions", identity?.tenantId],
    queryFn: () => api.listAuthorizationCatalogVersions(),
    enabled: Boolean(identity), staleTime: 0,
  });
  const catalogAudit = useQuery({
    queryKey: ["authz-catalog-audit", identity?.tenantId],
    queryFn: () => api.listAuthorizationCatalogAudit(100),
    enabled: Boolean(identity), staleTime: 0,
  });
  const assignmentGroups = useQuery({
    queryKey: ["human-assignment-groups", identity?.tenantId],
    queryFn: () => api.listAccessQueues(),
    enabled: Boolean(identity), staleTime: config.staleTimeMs,
  });
  const organizations = useQuery({
    queryKey: ["organizations", identity?.tenantId, "authz-scope"],
    queryFn: () => api.listOrganizations(0, config.maxPageSize),
    enabled: Boolean(identity), staleTime: config.staleTimeMs,
  });
  const effectiveOrganizationId = selectedOrganizationId || organizations.data?.[0]?.id || "";
  const organization = useQuery({
    queryKey: ["organization", identity?.tenantId, effectiveOrganizationId],
    queryFn: () => api.getOrganization(effectiveOrganizationId),
    enabled: Boolean(identity && effectiveOrganizationId), staleTime: config.staleTimeMs,
  });

  const entries = ownerEntries.data ?? [];
  const enabledOwnerEntries = useMemo(
    () => entries.filter((entry) => enabledEntries.has(entryKey(entry))),
    [entries, enabledEntries],
  );
  const branches = useMemo(
    () => (organization.data?.nodes ?? []).filter((node) => node.kind === "BRANCH" && node.is_active),
    [organization.data],
  );
  const branchScopePreview = useMemo(() => {
    const nodes = organization.data?.nodes ?? [];
    const activeBranches = nodes.filter((node) => node.kind === "BRANCH" && node.is_active);
    const children = new Map<string, string[]>();
    for (const node of nodes.filter((item) => item.is_active)) {
      if (node.parent_id) children.set(node.parent_id, [...(children.get(node.parent_id) ?? []), node.id]);
    }
    return (selectedIds: string[], includeDescendants: boolean) => {
      const selected = new Set(selectedIds);
      if (!includeDescendants) return activeBranches.filter((branch) => selected.has(branch.id));
      const reachable = new Set(selectedIds);
      const queue = [...selectedIds];
      while (queue.length) {
        for (const childId of children.get(queue.shift()!) ?? []) {
          if (!reachable.has(childId)) { reachable.add(childId); queue.push(childId); }
        }
      }
      return activeBranches.filter((branch) => reachable.has(branch.id));
    };
  }, [organization.data]);
  const currentVersion = versions.data?.find((version) => version.id === selectedVersionId)
    ?? versions.data?.find((version) => version.state === "PUBLISHED")
    ?? versions.data?.[0];
  const publishedVersion = versions.data?.find((version) => version.state === "PUBLISHED");
  const acknowledgements = useQuery({
    queryKey: ["authz-catalog-acknowledgements", identity?.tenantId, currentVersion?.id],
    queryFn: () => api.listAuthorizationCatalogAcknowledgements(currentVersion!.id),
    enabled: Boolean(identity && currentVersion?.state === "PUBLISHED"), staleTime: 0,
  });

  const refreshVersions = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ["authz-catalog-versions", identity?.tenantId] }),
    queryClient.invalidateQueries({ queryKey: ["authz-catalog-audit", identity?.tenantId] }),
  ]);
  const draftMutation = useMutation({
    mutationFn: () => {
      const selected = entries.filter((entry) => enabledEntries.has(entryKey(entry)));
      if (selected.length === 0) throw new Error("Chọn ít nhất một action đã được owner đăng ký.");
      const policyGrants = grants.map((grant) => {
        const target = entries.find((entry) => entryKey(entry) === grant.catalogEntryKey && enabledEntries.has(entryKey(entry)));
        if (!target) throw new Error("Mỗi policy phải trỏ tới action đang bật trong catalog.");
        const actorIds = grant.actorIds.split(/[\n,;]/).map((actorId) => actorId.trim()).filter(Boolean);
        const roles = grant.role.split(/[\n,;]/).map((role) => role.trim()).filter(Boolean);
        if (actorIds.length === 0 && roles.length === 0) throw new Error("Mỗi policy cần ít nhất một role hoặc actor ID.");
        const hasNarrowScope = grant.branchIds.length > 0 || grant.queueIds.length > 0 || grant.ownScope || grant.resourceIds.length > 0;
        if (grant.tenantScope === hasNarrowScope) throw new Error("Mỗi policy phải chọn rõ scope toàn tenant hoặc ít nhất một scope giới hạn.");
        return {
          grant_id: grant.id,
          owner_id: target.owner_id,
          actor_ids: [...new Set(actorIds)].sort(),
          roles: [...new Set(roles)].sort(),
          required_capabilities: [target.capability],
          resource_type: target.resource_type,
          action: target.action,
          effect: grant.effect,
          branch_scope_ids: grant.branchIds,
          branch_includes_descendants: grant.branchIncludesDescendants,
          queue_scope_ids: grant.queueIds,
          own_scope: grant.ownScope,
          resource_scope_ids: grant.resourceIds,
          tenant_scope: grant.tenantScope,
          readable_fields: grant.readableFields,
          writable_fields: grant.writableFields,
        };
      });
      const snapshot = {
        schema_version: 1,
        entries: entries.map((entry) => ({ ...entry, enabled: enabledEntries.has(entryKey(entry)) })),
        policy_grants: policyGrants,
      };
      return editingDraft
        ? api.updateAuthorizationCatalogDraft(editingDraft.id, editingDraft.draft_revision, snapshot, draftReason)
        : api.createAuthorizationCatalogDraft(snapshot, draftReason);
    },
    onSuccess: async (created) => {
      setNotice(`${editingDraft ? "Đã cập nhật" : "Đã tạo"} catalog draft v${created.version_num}.`);
      setFormError(""); setSelectedVersionId(created.id);
      setEditingDraft(null);
      setDraftReason("");
      await refreshVersions();
    },
    onError: (error) => setFormError(error instanceof Error ? error.message : "Không tạo được draft."),
  });
  const validateMutation = useMutation({
    mutationFn: (version: AuthorizationCatalogVersion) => api.validateAuthorizationCatalog(version.id),
    onSuccess: async (result) => { setNotice(`Catalog v${result.state === "VALIDATED" ? currentVersion?.version_num : ""} đã validate.`); setFormError(""); await refreshVersions(); },
    onError: (error) => setFormError(error instanceof Error ? error.message : "Validate thất bại."),
  });
  const publishMutation = useMutation({
    mutationFn: (version: AuthorizationCatalogVersion) => api.publishAuthorizationCatalog(version.id, version.version_num, publishReason),
    onSuccess: async (result) => {
      setNotice(`Đã ký và phát hành ${result.policy_version} (bundle ${result.bundle_sequence}).`);
      setPublishReason(""); setFormError(""); await refreshVersions();
    },
    onError: (error) => setFormError(error instanceof Error ? error.message : "Publish thất bại."),
  });

  function toggleEntry(entry: AuthorizationCatalogEntry) {
    const key = entryKey(entry);
    setEnabledEntries((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  }

  function patchGrant(id: string, patch: Partial<PolicyDraft>) {
    setGrants((current) => current.map((grant) => {
      if (grant.id !== id) return grant;
      const updated = { ...grant, ...patch };
      if (patch.tenantScope === true) {
        updated.branchIds = []; updated.queueIds = []; updated.ownScope = false; updated.resourceIds = [];
      } else if (patch.branchIds || patch.queueIds || patch.ownScope || patch.resourceIds) {
        const hasNarrowScope = updated.branchIds.length > 0 || updated.queueIds.length > 0 || updated.ownScope || updated.resourceIds.length > 0;
        if (hasNarrowScope) updated.tenantScope = false;
      }
      return updated;
    }));
  }

  function selectGrantEntry(id: string, selectedEntryKey: string) {
    const entry = entries.find((candidate) => entryKey(candidate) === selectedEntryKey);
    const supported = new Set(entry?.supported_scope_types ?? []);
    patchGrant(id, {
      catalogEntryKey: selectedEntryKey,
      readableFields: [],
      writableFields: [],
      branchIds: supported.has("branch") ? grants.find((grant) => grant.id === id)?.branchIds ?? [] : [],
      queueIds: supported.has("queue") ? grants.find((grant) => grant.id === id)?.queueIds ?? [] : [],
      resourceIds: supported.has("resource") ? grants.find((grant) => grant.id === id)?.resourceIds ?? [] : [],
      tenantScope: grants.find((grant) => grant.id === id)?.tenantScope ?? false,
      ownScope: supported.has("own") ? grants.find((grant) => grant.id === id)?.ownScope ?? false : false,
    });
  }

  function toggleArray(values: string[], value: string) {
    return values.includes(value) ? values.filter((current) => current !== value) : [...values, value];
  }

  function loadDraft(version: AuthorizationCatalogVersion, asNewDraft = false) {
    const snapshotEntries = version.snapshot.entries.flatMap((value) => {
      const parsed = authorizationCatalogEntrySchema.safeParse(value);
      return parsed.success ? [{ ...parsed.data, enabled: value.enabled === true }] : [];
    });
    const loadedGrants = version.snapshot.policy_grants.flatMap((value) => {
      const parsed = policyGrantSchema.safeParse(value);
      if (!parsed.success) return [];
      const catalogEntry = entries.find((entry) =>
        entry.owner_id === parsed.data.owner_id
        && entry.resource_type === parsed.data.resource_type
        && entry.action === parsed.data.action
        && parsed.data.required_capabilities.includes(entry.capability));
      return [{
        id: parsed.data.grant_id,
        role: parsed.data.roles.join("\n"),
        actorIds: parsed.data.actor_ids.join("\n"),
        catalogEntryKey: catalogEntry ? entryKey(catalogEntry) : "",
        effect: parsed.data.effect,
        branchIds: parsed.data.branch_scope_ids,
        branchIncludesDescendants: parsed.data.branch_includes_descendants,
        queueIds: parsed.data.queue_scope_ids,
        ownScope: parsed.data.own_scope,
        resourceIds: parsed.data.resource_scope_ids,
        tenantScope: parsed.data.tenant_scope,
        readableFields: parsed.data.readable_fields,
        writableFields: parsed.data.writable_fields,
      } satisfies PolicyDraft];
    });
    setEnabledEntries(new Set(snapshotEntries.filter((entry) => entry.enabled).map(entryKey)));
    setGrants(loadedGrants);
    setEditingDraft(asNewDraft ? null : version);
    setSelectedVersionId(asNewDraft ? "" : version.id);
    setNotice(asNewDraft
      ? `Đã sao chép cấu hình v${version.version_num} vào form. Lưu sẽ tạo draft version mới; version đã publish không thay đổi.`
      : `Đã nạp draft v${version.version_num} vào biểu mẫu.`);
    setFormError("");
  }

  if (ownerEntries.isPending || versions.isPending) return <LoadingRows />;
  if (ownerEntries.isError || versions.isError) {
    const error = ownerEntries.error ?? versions.error;
    return <ErrorState message={error instanceof Error ? error.message : "Không tải được Access catalog."} retry={() => { void ownerEntries.refetch(); void versions.refetch(); }} />;
  }

  return (
    <div className="access-page">
      <header className="page-header">
        <div>
          <h1>Access control</h1>
          <p>Quản lý action/resource catalog và policy theo version. Quyền chỉ có hiệu lực sau khi bundle được ký và publish.</p>
        </div>
        <div className="access-version-chip">
          <ShieldCheck size={17} aria-hidden="true" />
          <span>{versions.data?.find((version) => version.state === "PUBLISHED")?.version_num
            ? `Catalog v${versions.data.find((version) => version.state === "PUBLISHED")?.version_num} đang phát hành`
            : "Chưa có catalog được phát hành"}</span>
        </div>
      </header>

      {notice && <div className="access-notice" role="status">{notice}</div>}
      {formError && <div className="form-error" role="alert">{formError}</div>}

      <section className="access-section">
        <div className="access-section__heading">
          <div><span className="access-step">1</span><div><h2>Permission catalog</h2><p>Action do service owner đăng ký; tenant chỉ bật/tắt và cấu hình trong phạm vi manifest.</p></div></div>
          <span className="access-count">{enabledEntries.size} đang chọn</span>
        </div>
        {entries.length === 0 ? (
          <EmptyState title="Chưa có owner manifest" detail="Service owner cần đăng ký resource/action manifest trước khi tạo catalog tenant." />
        ) : (
          <div className="access-table-wrap">
            <table className="access-table">
              <thead><tr><th>Action / resource</th><th>Owner</th><th>Capability</th><th>Scope hỗ trợ</th><th>Bật</th></tr></thead>
              <tbody>{entries.map((entry) => <tr key={entryKey(entry)}>
                <td><strong>{entry.resource_type}.{entry.action}</strong><small>Manifest {entry.manifest_version}</small></td>
                <td>{entry.owner_id}</td>
                <td><code>{entry.capability}</code></td>
                <td>{entry.supported_scope_types.length ? entry.supported_scope_types.join(", ") : "tenant"}</td>
                <td><input aria-label={`Bật ${entry.resource_type}.${entry.action}`} type="checkbox" checked={enabledEntries.has(entryKey(entry))} onChange={() => toggleEntry(entry)} /></td>
              </tr>)}</tbody>
            </table>
          </div>
        )}
      </section>

      <section className="access-section">
        <div className="access-section__heading">
          <div><span className="access-step">2</span><div><h2>Role policies</h2><p>Rule scope branch theo cây parent/child; queue được giao với branch khi cùng khai báo.</p></div></div>
          <div className="access-heading-actions">
            {publishedVersion && <Button onClick={() => loadDraft(publishedVersion, true)}>Tạo draft từ v{publishedVersion.version_num}</Button>}
            <Button icon={CirclePlus} onClick={() => setGrants((current) => [...current, newGrant()])}>Thêm policy</Button>
          </div>
        </div>
        {(organizations.data?.length ?? 0) > 1 && <label className="access-organization-select">Organization<select value={effectiveOrganizationId} onChange={(event) => setSelectedOrganizationId(event.target.value)}>{organizations.data?.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>}
        {grants.length === 0 ? <EmptyState title="Chưa có policy grant" detail="Thêm role và action để tạo allow/deny rule cho catalog đang bật." /> : (
          <div className="access-policy-list">
            {grants.map((grant, index) => {
              const selectedEntry = entries.find((entry) => entryKey(entry) === grant.catalogEntryKey);
              const supportedScopes = new Set(selectedEntry?.supported_scope_types ?? []);
              const matchedBranches = branchScopePreview(grant.branchIds, grant.branchIncludesDescendants);
              const inactiveScopeIds = grant.branchIds.filter((id) => !branches.some((branch) => branch.id === id));
              return <article className="access-policy-card" key={grant.id}>
                <div className="access-policy-card__heading"><strong>Policy {index + 1}</strong><button type="button" className="link-button" onClick={() => setGrants((current) => current.filter((item) => item.id !== grant.id))}>Xóa</button></div>
                <div className="access-form-grid">
                  <label>Role codes<input value={grant.role} onChange={(event) => patchGrant(grant.id, { role: event.target.value })} placeholder="branch_reviewer" /><small>Nhập nhiều role, mỗi dòng hoặc dấu phẩy một role.</small></label>
                  <label>Actor IDs<textarea value={grant.actorIds} onChange={(event) => patchGrant(grant.id, { actorIds: event.target.value })} rows={2} placeholder="user UUID hoặc subject ID" /><small>Grant khớp actor trực tiếp hoặc một trong các role ở trên.</small></label>
                  <label>Resource / action<select value={grant.catalogEntryKey} onChange={(event) => selectGrantEntry(grant.id, event.target.value)}>
                    <option value="">Chọn action đã bật</option>{enabledOwnerEntries.map((entry) => <option value={entryKey(entry)} key={entryKey(entry)}>{entry.owner_id} · {entry.resource_type}.{entry.action} · {entry.capability}</option>)}
                  </select></label>
                  <label>Decision<select value={grant.effect} onChange={(event) => patchGrant(grant.id, { effect: event.target.value as PolicyDraft["effect"] })}><option value="ALLOW">Allow</option><option value="DENY">Deny</option></select></label>
                  {selectedEntry && <label className="access-tenant-scope"><span><input type="checkbox" checked={grant.tenantScope} onChange={(event) => patchGrant(grant.id, { tenantScope: event.target.checked })} /> Toàn tenant</span><small>Cho phép trên mọi branch, queue và resource của tenant. Chọn rõ ràng; bật mục này sẽ xóa các scope hẹp hơn.</small></label>}
                  <label>Branch scope{supportedScopes.has("branch") ? <><select multiple value={grant.branchIds} onChange={(event) => patchGrant(grant.id, { branchIds: Array.from(event.target.selectedOptions, (option) => option.value) })}>
                    {inactiveScopeIds.map((id) => <option key={id} value={id}>{id} · không còn active trong organization đang chọn</option>)}
                    {branches.map((branch) => <option key={branch.id} value={branch.id}>{branch.path} · {branch.name}</option>)}
                  </select><small>Không chọn branch nghĩa là tenant scope. Chọn branch cha sẽ áp dụng descendants theo policy.</small></> : <small>{selectedEntry ? "Owner chưa khai báo branch scope cho action này." : "Chọn action để xem scope được hỗ trợ."}</small>}</label>
                  <label>Queue / assignment group scope{supportedScopes.has("queue") ? assignmentGroups.isError ? <small>Không tải được assignment group từ Human Runtime.</small> : <><select multiple value={grant.queueIds} onChange={(event) => patchGrant(grant.id, { queueIds: Array.from(event.target.selectedOptions, (option) => option.value) })}>
                    {(assignmentGroups.data ?? []).map((group) => <option key={group.group_id} value={group.group_id}>{group.group_id} · {group.policy_count} policies</option>)}
                  </select><small>Danh sách là candidate groups trong assignment policies của Human Runtime. Khi có branch và group, cả hai phải cùng khớp.</small></> : <small>{selectedEntry ? "Owner chưa khai báo queue scope cho action này." : "Chọn action để xem scope được hỗ trợ."}</small>}</label>
                  {supportedScopes.has("resource") && <label>Resource IDs<input value={grant.resourceIds.join(", ")} onChange={(event) => patchGrant(grant.id, { resourceIds: event.target.value.split(",").map((item) => item.trim()).filter(Boolean) })} placeholder="(tùy chọn)" /></label>}
                </div>
                <div className="access-policy-options">
                  {grant.branchIds.length > 0 && <label><input type="checkbox" checked={grant.branchIncludesDescendants} onChange={(event) => patchGrant(grant.id, { branchIncludesDescendants: event.target.checked })} /> Bao gồm branch con</label>}
                  {supportedScopes.has("own") && <label><input type="checkbox" checked={grant.ownScope} onChange={(event) => patchGrant(grant.id, { ownScope: event.target.checked })} /> Chỉ resource do actor tạo</label>}
                </div>
                {grant.branchIds.length > 0 && <aside className="access-scope-preview" aria-label={`Scope preview policy ${index + 1}`}>
                  <strong>Branch scope preview</strong>
                  {organization.isPending ? <p>Đang tải cây tổ chức hiện hành…</p> : organization.isError ? <p>Không tải được cây tổ chức; chưa thể xem trước phạm vi branch.</p> : <>
                    <p>{matchedBranches.length} branch đang hoạt động khớp theo cây tổ chức {organization.data?.version !== undefined ? `(phiên bản ${organization.data.version})` : ""}.</p>
                    {inactiveScopeIds.length > 0 && <p role="alert">{inactiveScopeIds.length} branch đã chọn không còn hoạt động hoặc không thuộc organization này; cần bỏ khỏi policy trước khi lưu.</p>}
                    <ul>{matchedBranches.map((branch) => <li key={branch.id}>{branch.path} · {branch.name}</li>)}</ul>
                    <small>Đây là xem trước từ cấu hình nháp và cây tổ chức hiện tại, không phải quyết định cấp quyền. Authz validate và evaluator tại service mới quyết định. Nhiều branch trong cùng policy là OR; branch kết hợp queue/resource/own là AND.</small>
                  </>}
                </aside>}
                {selectedEntry && grant.effect === "ALLOW" && <div className="access-field-grants">
                  <fieldset><legend>Field được đọc</legend>{selectedEntry.readable_fields.length ? selectedEntry.readable_fields.map((field) => <label key={field}><input type="checkbox" checked={grant.readableFields.includes(field)} onChange={() => patchGrant(grant.id, { readableFields: toggleArray(grant.readableFields, field) })} />{field}</label>) : <small>Owner không khai báo field đọc.</small>}</fieldset>
                  <fieldset><legend>Field được sửa</legend>{selectedEntry.writable_fields.length ? selectedEntry.writable_fields.map((field) => <label key={field}><input type="checkbox" checked={grant.writableFields.includes(field)} onChange={() => patchGrant(grant.id, { writableFields: toggleArray(grant.writableFields, field) })} />{field}</label>) : <small>Owner không khai báo field sửa.</small>}</fieldset>
                </div>}
              </article>;
            })}
          </div>
        )}
        <div className="access-draft-footer">
          <span><LockKeyhole size={16} aria-hidden="true" /> Các thay đổi hiện nằm trong form; chưa có hiệu lực runtime.</span>
          <label>Lý do thay đổi<input value={draftReason} onChange={(event) => setDraftReason(event.target.value)} maxLength={1000} placeholder="Vì sao cần thay đổi catalog hoặc policy?" /></label>
          <Button icon={CirclePlus} variant="primary" onClick={() => draftMutation.mutate()} disabled={draftMutation.isPending || entries.length === 0 || draftReason.trim().length === 0}>
            {draftMutation.isPending ? "Đang lưu…" : editingDraft ? `Cập nhật draft v${editingDraft.version_num}` : "Tạo catalog draft"}
          </Button>
        </div>
      </section>

      <section className="access-section">
        <div className="access-section__heading">
          <div><span className="access-step">3</span><div><h2>Validate & publish</h2><p>Publish ký bundle theo version; consumers cần tải/acknowledge trước khi UI báo đồng bộ hoàn tất.</p></div></div>
          <Button icon={RefreshCw} onClick={() => { void refreshVersions(); void ownerEntries.refetch(); }}>Refresh</Button>
        </div>
        {(versions.data ?? []).length === 0 ? <EmptyState title="Chưa có catalog version" detail="Tạo draft ở bước trên để bắt đầu vòng đời version." /> : (
          <div className="access-lifecycle">
            <div className="access-version-list" role="list" aria-label="Catalog versions">
              {versions.data?.map((version) => <button type="button" key={version.id} className={`access-version-row ${currentVersion?.id === version.id ? "is-selected" : ""}`} onClick={() => setSelectedVersionId(version.id)}>
                <span><strong>v{version.version_num}</strong><small>{new Date(version.created_at).toLocaleString()}</small></span><StatusBadge status={version.state} />
              </button>)}
            </div>
            {currentVersion && <div className="access-version-detail">
              <div className="access-version-detail__top"><div><strong>Catalog v{currentVersion.version_num}</strong><StatusBadge status={currentVersion.state} /></div><span>Policy version: {currentVersion.state === "PUBLISHED" ? `access-v${currentVersion.version_num}` : "chưa phát hành"}</span></div>
              <p>{currentVersion.snapshot.entries.length} catalog entries · {currentVersion.snapshot.policy_grants.length} policy grants</p>
              {currentVersion.state === "DRAFT" && <Button icon={Check} onClick={() => validateMutation.mutate(currentVersion)} disabled={validateMutation.isPending}>{validateMutation.isPending ? "Đang validate…" : "Validate draft"}</Button>}
              {currentVersion.state === "DRAFT" && <Button onClick={() => loadDraft(currentVersion)} disabled={editingDraft?.id === currentVersion.id}>Nạp và chỉnh sửa draft</Button>}
              {currentVersion.state === "VALIDATED" && <div className="access-publish-row"><label>Publish reason<input value={publishReason} onChange={(event) => setPublishReason(event.target.value)} maxLength={1000} placeholder="Lý do thay đổi quyền" /></label><Button icon={Upload} variant="primary" onClick={() => publishMutation.mutate(currentVersion)} disabled={publishMutation.isPending || publishReason.trim().length === 0}>{publishMutation.isPending ? "Đang ký/publish…" : "Publish signed bundle"}</Button></div>}
              {currentVersion.state === "PUBLISHED" && <div className="access-published-note"><GitBranch size={16} aria-hidden="true" /> Bundle đã ký và publish. Acknowledgement phản ánh consumer đã verify và cài bundle; danh sách này không đại diện cho inventory consumer bắt buộc.</div>}
              {currentVersion.state === "PUBLISHED" && (acknowledgements.isPending ? <LoadingRows /> : acknowledgements.isError ? <ErrorState message="Không tải được trạng thái consumer." retry={() => { void acknowledgements.refetch(); }} /> : (acknowledgements.data ?? []).length === 0 ? <p>Chưa nhận acknowledgement cho version này.</p> : <div className="access-table-wrap"><table className="access-table"><thead><tr><th>Consumer</th><th>Bundle sequence</th><th>Hash</th><th>Đã áp dụng lúc</th></tr></thead><tbody>{acknowledgements.data.map((ack) => <tr key={`${ack.consumer_id}/${ack.bundle_sequence}`}><td>{ack.consumer_type} · {ack.consumer_id}</td><td>{ack.bundle_sequence}</td><td><code>{ack.bundle_content_hash.slice(0, 16)}…</code></td><td>{new Date(ack.acknowledged_at_epoch_ms).toLocaleString()}</td></tr>)}</tbody></table></div>)}
            </div>}
          </div>
        )}
      </section>

      <section className="access-section">
        <div className="access-section__heading">
          <div><span className="access-step">4</span><div><h2>Lịch sử catalog và policy</h2><p>Audit do Authz ghi cho thay đổi lifecycle; không thay thế audit thao tác nghiệp vụ ở từng owner service.</p></div></div>
          <Button icon={RefreshCw} onClick={() => { void catalogAudit.refetch(); }}>Refresh</Button>
        </div>
        {catalogAudit.isPending ? <LoadingRows /> : catalogAudit.isError ? (
          <ErrorState message={catalogAudit.error instanceof Error ? catalogAudit.error.message : "Không tải được lịch sử Authz."} retry={() => { void catalogAudit.refetch(); }} />
        ) : (catalogAudit.data ?? []).length === 0 ? (
          <EmptyState title="Chưa có thay đổi được ghi nhận" detail="Các lần tạo, sửa, validate và publish catalog sẽ xuất hiện tại đây." />
        ) : (
          <div className="access-table-wrap"><table className="access-table">
            <thead><tr><th>Thời điểm</th><th>Thao tác</th><th>Version</th><th>Actor</th><th>Lý do</th><th>Request</th></tr></thead>
            <tbody>{catalogAudit.data.map((item) => <tr key={`${item.request_id}/${item.action}/${item.occurred_at}`}>
              <td>{new Date(item.occurred_at).toLocaleString()}</td><td><StatusBadge status={item.action} /></td><td>v{item.version_num}</td><td>{item.actor_ref}</td><td>{item.reason || "—"}</td><td><code>{item.request_id}</code></td>
            </tr>)}</tbody>
          </table></div>
        )}
      </section>
    </div>
  );
}
