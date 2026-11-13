import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, Building2, KeyRound, RefreshCw, Users } from "lucide-react";
import { useState } from "react";
import { useApi } from "../../api/ApiContext";
import { useAuth } from "../../auth/AuthContext";
import { Button } from "../../components/Button";
import { EmptyState, ErrorState, LoadingRows } from "../../components/Feedback";
import { useRuntimeConfig } from "../../config/ConfigContext";
import { AppLink } from "../../routing/router";

export function IdentityDirectoryPage() {
  const api = useApi();
  const { identity } = useAuth();
  const config = useRuntimeConfig();
  const [userPageTokens, setUserPageTokens] = useState<string[]>([""]);
  const afterUserId = userPageTokens.at(-1) ?? "";
  const organizations = useQuery({
    queryKey: ["organizations", identity?.tenantId, "identity-overview"],
    queryFn: () => api.listOrganizations(0, config.defaultPageSize),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
  });
  const roles = useQuery({
    queryKey: ["authorization-roles", identity?.tenantId],
    queryFn: () => api.listAuthorizationRoles(),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
  });
  const users = useQuery({
    queryKey: ["identity-users", identity?.tenantId, afterUserId],
    queryFn: () => api.listIdentityUsers(afterUserId, config.defaultPageSize),
    enabled: Boolean(identity),
    staleTime: config.staleTimeMs,
  });
  const refreshing = organizations.isFetching || roles.isFetching || users.isFetching;

  return (
    <>
      <header className="page-header">
        <div><h1>Identity</h1><p>Live organization directory and authorization roles for this tenant.</p></div>
        <div className="header-actions">
          <Button icon={RefreshCw} onClick={() => { void organizations.refetch(); void roles.refetch(); void users.refetch(); }} disabled={refreshing}>Refresh</Button>
          <AppLink className="button button--secondary" to="/organizations">Organization tree</AppLink>
          <AppLink className="button button--primary" to="/access"><KeyRound size={15} /> Access policies</AppLink>
        </div>
      </header>
      <section className="portal-summary" aria-label="Identity summary">
        <article className="portal-summary__card"><span className="portal-summary__icon"><Building2 size={18} /></span><div><span>Organizations on this page</span><strong>{organizations.data?.length ?? (organizations.isPending ? "—" : 0)}</strong></div></article>
        <article className="portal-summary__card"><span className="portal-summary__icon"><Users size={18} /></span><div><span>Users on this page</span><strong>{users.data?.users.length ?? (users.isPending ? "—" : 0)}</strong></div></article>
        <article className="portal-summary__card"><span className="portal-summary__icon"><KeyRound size={18} /></span><div><span>Tenant</span><strong className="portal-summary__tenant">{identity?.tenantId ?? "—"}</strong></div></article>
      </section>
      <div className="identity-directory-grid">
        <section className="table-surface">
          <header className="portal-section-heading"><div><h2>User directory</h2><p>Authz directory records and active role assignments, provisioned by the configured identity source.</p></div></header>
          {users.isPending ? <LoadingRows /> : null}
          {users.isError ? <ErrorState message={users.error.message} retry={() => void users.refetch()} /> : null}
          {users.isSuccess && users.data.users.length === 0 ? <EmptyState title="No directory users" detail="No user accounts were returned for this tenant and cursor." /> : null}
          {users.data?.users.length ? <div className="table-scroll"><table><thead><tr><th>User</th><th>External subject</th><th>Roles</th><th>Account</th><th>Query access</th><th>Version</th></tr></thead><tbody>{users.data.users.map((user) => <tr key={user.user_id}><td><strong>{user.username}</strong><span className="cell-secondary">{user.user_id}</span></td><td className="mono">{user.external_id || "—"}</td><td>{user.roles.length ? user.roles.map((role) => <code className="identity-role-chip" key={role}>{role}</code>) : "—"}</td><td>{user.is_active ? "Active" : "Inactive"}</td><td>{user.query_revoked ? "Revoked" : "Current"}</td><td>{user.version}</td></tr>)}</tbody></table></div> : null}
          {users.data ? <footer className="pagination"><span>Directory page {userPageTokens.length}</span><div><Button icon={ArrowLeft} disabled={userPageTokens.length === 1 || users.isFetching} onClick={() => setUserPageTokens((tokens) => tokens.slice(0, -1))}>Previous</Button><Button icon={ArrowRight} disabled={!users.data.next_page_token || users.isFetching} onClick={() => { const next = users.data.next_page_token; if (next) setUserPageTokens((tokens) => [...tokens, next]); }}>Next</Button></div></footer> : null}
        </section>
        <section className="table-surface">
          <header className="portal-section-heading"><div><h2>Organizations</h2><p>Owned by the Organization API</p></div></header>
          {organizations.isPending ? <LoadingRows /> : null}
          {organizations.isError ? <ErrorState message={organizations.error.message} retry={() => void organizations.refetch()} /> : null}
          {organizations.isSuccess && organizations.data.length === 0 ? <EmptyState title="No organizations" detail="The organization service returned no rows for this tenant." /> : null}
          {organizations.data?.length ? <div className="table-scroll"><table><thead><tr><th>Organization</th><th>Code</th><th>Nodes</th><th>Version</th></tr></thead><tbody>{organizations.data.map((organization) => <tr key={organization.id}><td>{organization.name}</td><td className="mono">{organization.code}</td><td>{organization.node_count}</td><td>{organization.version}</td></tr>)}</tbody></table></div> : null}
        </section>
        <section className="table-surface">
          <header className="portal-section-heading"><div><h2>Authorization roles</h2><p>Role codes registered for policy grants</p></div></header>
          {roles.isPending ? <LoadingRows /> : null}
          {roles.isError ? <ErrorState message={roles.error.message} retry={() => void roles.refetch()} /> : null}
          {roles.isSuccess && roles.data.length === 0 ? <EmptyState title="No roles registered" detail="Register role codes before referencing them in a grant." /> : null}
          {roles.data?.length ? <div className="table-scroll"><table><thead><tr><th>Role code</th><th>Name</th></tr></thead><tbody>{roles.data.map((role) => <tr key={role.code}><td className="mono">{role.code}</td><td>{role.name}</td></tr>)}</tbody></table></div> : null}
        </section>
      </div>
      <p className="query-policy-note identity-boundary-note">This directory is read-only: account creation, profile changes, credentials, MFA, and identity-provider lifecycle stay with the provisioned identity source. User roles are read from Authz user-role assignments, not inferred from role catalog codes.</p>
    </>
  );
}
