-- Invitations: an account an admin made for someone with their e-mail
-- address, whose password they choose themselves through a link sent there
-- (one use, a few days). Until then the account has a random password
-- nobody knows.
CREATE TABLE invites (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;
