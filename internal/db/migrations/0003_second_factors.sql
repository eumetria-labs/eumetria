-- Second factors (PLAN M4b): passkeys and security keys (WebAuthn), an
-- authenticator app (TOTP) and recovery codes.

-- The user handle a passkey stores for its user: random, made on first use.
ALTER TABLE users ADD COLUMN webauthn_handle BLOB;
CREATE UNIQUE INDEX users_webauthn_handle ON users (webauthn_handle);

-- When the session's user last proved who they are (sign-in); changing the
-- second factors needs that to be recent.
ALTER TABLE sessions ADD COLUMN authenticated_at INTEGER NOT NULL DEFAULT 0;
UPDATE sessions SET authenticated_at = created_at;

CREATE TABLE webauthn_credentials (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id BLOB NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    -- The credential as go-webauthn keeps it (JSON): public key, signature
    -- counter, flags, transports.
    data          TEXT NOT NULL,
    -- Registered with user verification (fingerprint, face, PIN): it signs
    -- in without the password.
    passwordless  INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER
) STRICT;
CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);

CREATE TABLE totp (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret     TEXT NOT NULL, -- base32
    last_step  INTEGER NOT NULL DEFAULT 0, -- each code works once
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE recovery_codes (
    id        INTEGER PRIMARY KEY,
    user_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at   INTEGER
) STRICT;
CREATE INDEX recovery_codes_user ON recovery_codes (user_id);
