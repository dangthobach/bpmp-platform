import { useState } from "react";
import { KeyRound } from "lucide-react";
import { useAuth } from "../auth/AuthContext";
import { Button } from "./Button";

export function ConnectionDialog() {
  const { identity, loading, connect } = useAuth();
  const [starting, setStarting] = useState(false);
  if (identity) return null;

  return (
    <div className="connection-screen">
      <section className="connection-panel" aria-busy={loading || starting}>
        <div className="connection-panel__mark">
          <KeyRound size={20} aria-hidden="true" />
        </div>
        <h1>Sign in to BPMP</h1>
        <p>Continue with your organization account through Keycloak.</p>
        <Button
          type="button"
          variant="primary"
          disabled={loading || starting}
          onClick={() => { setStarting(true); connect(); }}
        >
          {loading ? "Checking session…" : "Continue with SSO"}
        </Button>
      </section>
    </div>
  );
}
