-- Migration 010: Add last_active_at and partial index for background user deactivation

-- Adding last_active_at to track when the user last performed an action.
-- Setting default to now() so that existing users have a baseline activity.
ALTER TABLE user_account 
ADD COLUMN last_active_at TIMESTAMPTZ NOT NULL DEFAULT now();

COMMENT ON COLUMN user_account.last_active_at IS 'Tracks the last time the user was active, used by background job to deactive idle users.';

-- Partial index for fast scanning. We only care about users who are currently active (is_active = true)
-- and whose last_active_at is older than the threshold. The btree index on last_active_at where is_active = true
-- makes range scans for "older than 60 days" extremely fast.
CREATE INDEX idx_user_account_active_scan 
ON user_account(last_active_at) 
WHERE is_active = true;
