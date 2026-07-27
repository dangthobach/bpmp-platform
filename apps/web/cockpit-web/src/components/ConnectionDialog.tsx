import { useState, type FormEvent } from "react";
import { KeyRound } from "lucide-react";
import { useAuth } from "../auth/AuthContext";
import { Button } from "./Button";

export function ConnectionDialog() {
  const { identity, connect } = useAuth();
  const [tenantId, setTenantId] = useState("");
  const [accessToken, setAccessToken] = useState("");
  if (identity) return null;

  function submit(event: FormEvent) {
    event.preventDefault();
    const tenant = tenantId.trim();
    const token = accessToken.trim();
    if (tenant && token) connect({ tenantId: tenant, accessToken: token });
  }

  return (
    <div className="connection-screen">
      <form className="connection-panel" onSubmit={submit}>
        <div className="connection-panel__mark">
          <KeyRound size={20} aria-hidden="true" />
        </div>
        <h1>Connect to BPMP</h1>
        <div className="field">
          <label htmlFor="tenant">Tenant ID</label>
          <input
            id="tenant"
            value={tenantId}
            onChange={(event) => setTenantId(event.target.value)}
            autoComplete="organization"
            required
          />
        </div>
        <div className="field">
          <label htmlFor="token">Access token</label>
          <textarea
            id="token"
            value={accessToken}
            onChange={(event) => setAccessToken(event.target.value)}
            rows={5}
            autoComplete="off"
            required
          />
        </div>
        <Button type="submit" variant="primary">Connect</Button>
      </form>
    </div>
  );
}
