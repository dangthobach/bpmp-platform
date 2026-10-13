ALTER TABLE user_account ADD COLUMN query_revoke_epoch BIGINT NOT NULL DEFAULT 0 CHECK (query_revoke_epoch >= 0);
ALTER TABLE user_account ADD COLUMN query_revoked BOOLEAN NOT NULL DEFAULT false;

-- Administrative writes serialize through this revision; query reads never lock it.
CREATE TABLE query_security_revision (singleton BOOLEAN PRIMARY KEY CHECK (singleton), revision BIGINT NOT NULL CHECK (revision >= 0));
INSERT INTO query_security_revision VALUES (true, 0);

CREATE FUNCTION advance_query_security_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE query_security_revision SET revision = revision + 1 WHERE singleton;
    RETURN NULL;
END $$;

DO $$ DECLARE tab TEXT; BEGIN
    FOREACH tab IN ARRAY ARRAY['tenant','user_account','role','permission','role_permission','user_role','policy','policy_rule','policy_version','row_filter','field_filter','temporal_policy','resource_type','resource_acl','relation_type','relation_tuple','relation_reachability'] LOOP
        EXECUTE format('CREATE TRIGGER query_security_change AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION advance_query_security_revision()', tab);
    END LOOP;
END $$;

CREATE TABLE query_revoke_feed (
    sequence BIGINT PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenant(id),
    actor_id UUID NOT NULL REFERENCES user_account(id),
    revoke_epoch BIGINT NOT NULL CHECK (revoke_epoch > 0),
    revoked BOOLEAN NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 2048),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    performed_by TEXT NOT NULL,
    UNIQUE (tenant_id, actor_id, revoke_epoch)
);
CREATE INDEX query_revoke_feed_tenant_sequence ON query_revoke_feed(tenant_id, sequence);
CREATE FUNCTION reject_query_revoke_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'query revoke feed is append only'; END $$;
CREATE TRIGGER query_revoke_append_only BEFORE UPDATE OR DELETE ON query_revoke_feed FOR EACH ROW EXECUTE FUNCTION reject_query_revoke_mutation();
