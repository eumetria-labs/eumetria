-- Deleting a disabled account (Users & roles) keeps what that person wrote:
-- notes, lifts, benchmark workouts and their results stay, their author
-- becomes NULL ("deleted user" in the page). SQLite can't change a foreign
-- key in place, so the four tables are built anew. Renaming first makes
-- wod_results follow wods to its old name, so dropping the old tables
-- deletes nothing that stays (a DROP TABLE deletes its rows first, with
-- their ON DELETE CASCADE).

ALTER TABLE wod_results RENAME TO wod_results_old;
ALTER TABLE wods RENAME TO wods_old;
ALTER TABLE lifts RENAME TO lifts_old;
ALTER TABLE notes RENAME TO notes_old;
DROP INDEX wod_results_by_wod;
DROP INDEX wods_by_name;
DROP INDEX lifts_by_lift;

CREATE TABLE notes (
    workout_id TEXT PRIMARY KEY,
    markdown   TEXT NOT NULL,
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
INSERT INTO notes SELECT workout_id, markdown, created_by, updated_by, created_at, updated_at FROM notes_old;

CREATE TABLE lifts (
    id         INTEGER PRIMARY KEY,
    lift       TEXT NOT NULL,
    kg         REAL NOT NULL,
    reps       INTEGER NOT NULL DEFAULT 1,
    date       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
INSERT INTO lifts SELECT id, lift, kg, reps, date, note, created_by, updated_by, created_at, updated_at FROM lifts_old;
CREATE INDEX lifts_by_lift ON lifts (lift, date);

CREATE TABLE wods (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    scoring     TEXT NOT NULL CHECK (scoring IN ('time', 'rounds', 'reps', 'load')),
    description TEXT NOT NULL DEFAULT '',
    created_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    time_cap    INTEGER NOT NULL DEFAULT 0
) STRICT;
INSERT INTO wods SELECT id, name, scoring, description, created_by, updated_by, created_at, updated_at, time_cap FROM wods_old;
CREATE UNIQUE INDEX wods_by_name ON wods (name COLLATE NOCASE);

CREATE TABLE wod_results (
    id             INTEGER PRIMARY KEY,
    wod_id         INTEGER NOT NULL REFERENCES wods (id) ON DELETE CASCADE,
    value          REAL NOT NULL,
    reps           INTEGER NOT NULL DEFAULT 0,
    scaled         INTEGER NOT NULL DEFAULT 0,
    date           TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    created_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    capped         INTEGER NOT NULL DEFAULT 0,
    fitbit_workout TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO wod_results SELECT id, wod_id, value, reps, scaled, date, note, created_by, updated_by, created_at, updated_at, capped, fitbit_workout FROM wod_results_old;
CREATE INDEX wod_results_by_wod ON wod_results (wod_id, date);

DROP TABLE wod_results_old;
DROP TABLE wods_old;
DROP TABLE lifts_old;
DROP TABLE notes_old;
