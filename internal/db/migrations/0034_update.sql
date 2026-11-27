-- In-place updates (PLAN 15a). update_state keeps what the update check
-- needs across restarts: "last_check" (the last result as JSON, so a
-- restart doesn't ask GitHub again) and "mailed" (the version last mailed
-- about, so each one is mailed once).
CREATE TABLE update_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
