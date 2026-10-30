-- Roles made of permissions (PLAN M5). A user has exactly one role. The
-- built-in roles get their scopes from the code (internal/auth), so a new
-- scope reaches them without a migration; the scopes column holds those of
-- the roles an admin makes.

CREATE TABLE roles (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    builtin    INTEGER NOT NULL DEFAULT 0,
    scopes     TEXT NOT NULL DEFAULT '', -- space-separated, e.g. "view.nutrition view.foods"
    created_at INTEGER NOT NULL
) STRICT;

INSERT INTO roles (id, name, builtin, created_at) VALUES
    (1, 'admin', 1, unixepoch()),
    (2, 'viewer', 1, unixepoch());

-- SQLite can't add a REFERENCES column with a default, so the users get
-- theirs right after; the old column's CHECK goes with it.
ALTER TABLE users ADD COLUMN role_id INTEGER REFERENCES roles (id);
UPDATE users SET role_id = CASE role WHEN 'admin' THEN 1 ELSE 2 END;
ALTER TABLE users DROP COLUMN role;
CREATE INDEX users_role ON users (role_id);
