-- Fix 1 (forgot password): 6-digit codes delivered via FCM, valid 15 minutes, single-use.
CREATE TABLE IF NOT EXISTS password_reset_codes (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code       VARCHAR(6)  NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used       BOOLEAN     DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_password_reset_codes_user    ON password_reset_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_password_reset_codes_expires ON password_reset_codes(expires_at);

-- Fix 3 (first-login password change): admin creates staff -> profile is flagged ->
-- on login backend returns status="password_change_required" with a temporary JWT.
ALTER TABLE profiles ADD COLUMN IF NOT EXISTS password_change_required BOOLEAN DEFAULT FALSE;
