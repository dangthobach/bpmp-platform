BEGIN;

CREATE TABLE process_model_versions (
    tenant_id UUID NOT NULL,
    id UUID NOT NULL,
    process_model_id UUID NOT NULL,
    version_num BIGINT NOT NULL CHECK (version_num > 0),
    source_revision BIGINT NOT NULL CHECK (source_revision > 0),
    bpmn_xml TEXT NOT NULL CHECK (octet_length(bpmn_xml) BETWEEN 1 AND 1048576),
    content_hash BYTEA NOT NULL CHECK (octet_length(content_hash) = 32),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    version_tag TEXT NOT NULL CHECK (length(version_tag) BETWEEN 1 AND 128),
    summary TEXT NOT NULL CHECK (length(summary) BETWEEN 1 AND 1000),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, process_model_id, version_num),
    UNIQUE (tenant_id, process_model_id, source_revision),
    UNIQUE (tenant_id, process_model_id, version_tag),
    FOREIGN KEY (tenant_id, process_model_id)
      REFERENCES process_models (tenant_id, id) ON DELETE RESTRICT
);

ALTER TABLE process_models
    ADD CONSTRAINT process_models_latest_version_fk
    FOREIGN KEY (tenant_id, latest_version_id)
    REFERENCES process_model_versions (tenant_id, id) ON DELETE RESTRICT;

CREATE INDEX process_model_versions_history
    ON process_model_versions (tenant_id, process_model_id, version_num DESC);

CREATE FUNCTION reject_process_model_version_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'process model versions are immutable';
END;
$$;

CREATE TRIGGER process_model_versions_immutable
BEFORE UPDATE OR DELETE ON process_model_versions
FOR EACH ROW EXECUTE FUNCTION reject_process_model_version_mutation();

COMMIT;
