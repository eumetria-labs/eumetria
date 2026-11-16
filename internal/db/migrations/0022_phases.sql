-- Phases and events, noted on the page (account menu → Phases & events).
-- A phase is a stretch of days with a purpose (kind cut, bulk, maintain or
-- other, with an optional label); an event is one day (a competition, a
-- race), start_date = end_date. A phase still running has end_date ''.
-- The charts shade phases and mark events.
CREATE TABLE phases (
    id         INTEGER PRIMARY KEY,
    kind       TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    start_date TEXT NOT NULL,
    end_date   TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX phases_by_start ON phases (start_date);
