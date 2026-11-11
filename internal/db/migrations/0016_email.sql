-- Verified e-mail addresses (PLAN phase 6): security alerts and enrolment
-- links go only to an address its user confirmed with a link sent there.
-- A new address waits in email_changes until then; the old one stays.
ALTER TABLE profiles ADD COLUMN email_verified_at INTEGER NOT NULL DEFAULT 0;

CREATE TABLE email_changes (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;
