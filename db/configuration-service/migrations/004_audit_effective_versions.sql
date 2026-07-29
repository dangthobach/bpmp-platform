ALTER TABLE configuration_audit
    ADD COLUMN IF NOT EXISTS config_version text NOT NULL DEFAULT 'legacy-unknown',
    ADD COLUMN IF NOT EXISTS policy_version text NOT NULL DEFAULT 'legacy-unknown';

ALTER TABLE configuration_audit
    ALTER COLUMN config_version DROP DEFAULT,
    ALTER COLUMN policy_version DROP DEFAULT;

ALTER TABLE configuration_audit
    DROP CONSTRAINT IF EXISTS configuration_audit_effective_versions_check;
ALTER TABLE configuration_audit
    ADD CONSTRAINT configuration_audit_effective_versions_check
    CHECK (
        length(trim(config_version)) > 0
        AND length(trim(policy_version)) > 0
    );
