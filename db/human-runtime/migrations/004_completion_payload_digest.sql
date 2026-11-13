ALTER TABLE work_items
    ADD COLUMN IF NOT EXISTS completion_payload_digest text;
