-- Keys the server makes for itself once and keeps across restarts. The
-- first is "device": it signs the known-device cookie (pentest re-test N2).
CREATE TABLE server_keys (
    name TEXT PRIMARY KEY,
    key  BLOB NOT NULL
) STRICT;
