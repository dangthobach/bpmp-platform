CREATE TABLE IF NOT EXISTS query_actor_revoke_epochs (
    tenant_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    revoke_epoch BIGINT NOT NULL CHECK (revoke_epoch > 0),
    event_sequence BIGINT NOT NULL CHECK (event_sequence > 0),
    revoked BOOLEAN NOT NULL,
    event_id TEXT NOT NULL,
    PRIMARY KEY(tenant_id,actor_id)
);
CREATE TABLE IF NOT EXISTS query_revoke_checkpoints (
    topic TEXT NOT NULL,
    partition INTEGER NOT NULL CHECK (partition >= 0),
    last_offset BIGINT NOT NULL CHECK(last_offset >= 0),
    PRIMARY KEY(topic,partition)
);
CREATE TABLE IF NOT EXISTS query_revoke_incidents (
    incident_id TEXT PRIMARY KEY,
    source_topic TEXT NOT NULL,
    source_partition INTEGER NOT NULL,
    source_offset BIGINT NOT NULL,
    source_sha256 BYTEA NOT NULL CHECK(octet_length(source_sha256)=32),
    source_size BIGINT NOT NULL,
    reason_code TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(source_topic,source_partition,source_offset)
);
CREATE TABLE IF NOT EXISTS query_revoke_dlq_state (
    incident_id TEXT PRIMARY KEY REFERENCES query_revoke_incidents(incident_id),
    acknowledged_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS query_revoke_incident_resolutions (
    incident_id TEXT PRIMARY KEY REFERENCES query_revoke_incidents(incident_id),
    operator_ref TEXT NOT NULL CHECK(length(trim(operator_ref))>0),
    reason TEXT NOT NULL CHECK(length(trim(reason))>0),
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE OR REPLACE FUNCTION reject_query_revoke_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'query revoke audit is append only'; END $$;
DROP TRIGGER IF EXISTS query_revoke_incidents_immutable ON query_revoke_incidents;
CREATE TRIGGER query_revoke_incidents_immutable BEFORE UPDATE OR DELETE ON query_revoke_incidents FOR EACH ROW EXECUTE FUNCTION reject_query_revoke_audit_mutation();
DROP TRIGGER IF EXISTS query_revoke_resolutions_immutable ON query_revoke_incident_resolutions;
CREATE TRIGGER query_revoke_resolutions_immutable BEFORE UPDATE OR DELETE ON query_revoke_incident_resolutions FOR EACH ROW EXECUTE FUNCTION reject_query_revoke_audit_mutation();

CREATE TABLE IF NOT EXISTS query_revoke_security_state(singleton BOOLEAN PRIMARY KEY CHECK(singleton),unresolved_incidents BIGINT NOT NULL CHECK(unresolved_incidents>=0));
INSERT INTO query_revoke_security_state SELECT true,count(*) FROM query_revoke_incidents i WHERE NOT EXISTS(SELECT 1 FROM query_revoke_incident_resolutions r WHERE r.incident_id=i.incident_id) ON CONFLICT DO NOTHING;
CREATE OR REPLACE FUNCTION query_revoke_fence_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME='query_revoke_incidents' THEN
        UPDATE query_revoke_security_state SET unresolved_incidents=unresolved_incidents+1 WHERE singleton;
    ELSE
        IF NOT EXISTS(SELECT 1 FROM query_revoke_dlq_state WHERE incident_id=NEW.incident_id AND acknowledged_at IS NOT NULL) THEN RAISE EXCEPTION 'DLQ must be acknowledged before incident resolution'; END IF;
        UPDATE query_revoke_security_state SET unresolved_incidents=unresolved_incidents-1 WHERE singleton;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS query_revoke_fence_created ON query_revoke_incidents;
CREATE TRIGGER query_revoke_fence_created AFTER INSERT ON query_revoke_incidents FOR EACH ROW EXECUTE FUNCTION query_revoke_fence_change();
DROP TRIGGER IF EXISTS query_revoke_fence_resolved ON query_revoke_incident_resolutions;
CREATE TRIGGER query_revoke_fence_resolved AFTER INSERT ON query_revoke_incident_resolutions FOR EACH ROW EXECUTE FUNCTION query_revoke_fence_change();

CREATE OR REPLACE FUNCTION resolve_query_revoke_incident(id TEXT, expected_sha256 BYTEA, operator_ref TEXT, reason TEXT) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS(SELECT 1 FROM query_revoke_incidents i JOIN query_revoke_dlq_state d USING(incident_id) WHERE i.incident_id=id AND i.source_sha256=expected_sha256 AND d.acknowledged_at IS NOT NULL) THEN RAISE EXCEPTION 'incident hash/ACK mismatch'; END IF;
    INSERT INTO query_revoke_incident_resolutions(incident_id,operator_ref,reason) VALUES(id,operator_ref,reason) ON CONFLICT DO NOTHING;
END $$;
