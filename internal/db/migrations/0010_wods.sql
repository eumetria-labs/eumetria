-- Benchmark workouts (Fran, Cindy, a WOD the box repeats, …) and their
-- results, written in the Training card next to the lifts. People create a
-- workout once and note a result each time they do it.
--
-- scoring says what a result is and which way is better:
--   time    value = seconds, the fewer the better
--   rounds  value = full rounds, reps = the reps of the round after them
--   reps    value = reps
--   load    value = kg
-- scaled marks a result done with lighter weights or easier movements; it
-- has its own best.
CREATE TABLE wods (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    scoring     TEXT NOT NULL CHECK (scoring IN ('time', 'rounds', 'reps', 'load')),
    description TEXT NOT NULL DEFAULT '',
    created_by  INTEGER NOT NULL REFERENCES users (id),
    updated_by  INTEGER NOT NULL REFERENCES users (id),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX wods_by_name ON wods (name COLLATE NOCASE);

CREATE TABLE wod_results (
    id         INTEGER PRIMARY KEY,
    wod_id     INTEGER NOT NULL REFERENCES wods (id) ON DELETE CASCADE,
    value      REAL NOT NULL,
    reps       INTEGER NOT NULL DEFAULT 0,
    scaled     INTEGER NOT NULL DEFAULT 0,
    date       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users (id),
    updated_by INTEGER NOT NULL REFERENCES users (id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX wod_results_by_wod ON wod_results (wod_id, date);
