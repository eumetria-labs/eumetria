-- Enrolment links (pentest F19): the first second factor of an account
-- that must have one (admin, sync, writers, or asked for by an admin) can
-- only be set up with a one-time link an admin or the command line made.
-- The password alone, which someone else may know, isn't enough.
CREATE TABLE enrolments (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE, -- sha256 of the token, hex
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;
CREATE INDEX enrolments_user ON enrolments (user_id);

-- Security notices (pentest F18/F19): what changed about an account's sign-in
-- (a key, an app, recovery codes, a token, the password), shown to its user
-- until they dismiss it, so a change someone else made doesn't go unnoticed.
CREATE TABLE security_notices (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT '',
    -- The session the user made the change in themselves (its token_hash,
    -- which unlike its id is never reused): that session doesn't need
    -- telling. '' for changes made elsewhere (an admin, the command line).
    session    TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    seen_at    INTEGER
) STRICT;
CREATE INDEX security_notices_user ON security_notices (user_id, seen_at);
