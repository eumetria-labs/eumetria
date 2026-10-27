-- Accounts (PLAN M4). Times are Unix seconds, UTC. Tokens are kept only as
-- their SHA-256 (internal/auth), passwords only as argon2id hashes.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    -- M5 replaces the two fixed roles with roles made of permissions.
    role          TEXT NOT NULL CHECK (role IN ('admin', 'viewer')),
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    password_at   INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    remember     INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL, -- space-separated, e.g. "read sync"
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER, -- NULL: never
    last_used_at INTEGER
) STRICT;
CREATE INDEX api_tokens_user ON api_tokens (user_id);

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY,
    at      INTEGER NOT NULL,
    user_id INTEGER REFERENCES users (id) ON DELETE SET NULL,
    actor   TEXT NOT NULL, -- the user name at the time, or "cli", or the name tried
    action  TEXT NOT NULL, -- e.g. login, login_failed, logout, user_created
    detail  TEXT NOT NULL DEFAULT '',
    ip      TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX audit_log_at ON audit_log (at);
