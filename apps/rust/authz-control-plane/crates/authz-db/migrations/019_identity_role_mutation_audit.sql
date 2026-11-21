-- Role changes advance the owning profile's optimistic version and share the
-- same local transaction as the idempotency receipt and immutable audit row.
ALTER TABLE identity_profile_mutation_audit
    DROP CONSTRAINT identity_profile_mutation_audit_operation_check;

ALTER TABLE identity_profile_mutation_audit
    ADD CONSTRAINT identity_profile_mutation_audit_operation_check
    CHECK (operation IN ('PROVISION_LINK', 'UPDATE_PROFILE', 'ASSIGN_ROLE', 'REVOKE_ROLE'));

CREATE INDEX idx_identity_profile_audit_operation
    ON identity_profile_mutation_audit (tenant_id, operation, created_at DESC);
