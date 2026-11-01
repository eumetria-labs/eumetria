-- One row per backup (PLAN M9), for the status in the settings and to know
-- whether today's has run. Times are Unix seconds. error says what went wrong
-- with the archive, drive how the upload went: empty when there was none, "ok"
-- or what failed.
CREATE TABLE backup_runs (
    id          INTEGER PRIMARY KEY,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0,
    file        TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    encrypted   INTEGER NOT NULL DEFAULT 0,
    drive       TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT ''
) STRICT;
