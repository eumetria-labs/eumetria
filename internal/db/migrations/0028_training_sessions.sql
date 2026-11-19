-- Training sessions (not sign-in sessions, which are "sessions"): what was done in a session, set by set. Optional, and
-- not a training app: lifts (personal bests) and benchmark results stay as
-- they are. A session may belong to the Fitbit workout it was done in, like
-- a lift or a benchmark result.
CREATE TABLE training_sessions (
    id             INTEGER PRIMARY KEY,
    date           TEXT NOT NULL,
    fitbit_workout TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    rpe            INTEGER CHECK (rpe BETWEEN 1 AND 10),
    created_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX training_sessions_by_date ON training_sessions (date);

-- A set: the lift, warm-up or work, reps × kg (0 kg: bodyweight), how hard
-- it felt (RPE, optional), in the order done.
CREATE TABLE training_sets (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES training_sessions (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    lift       TEXT NOT NULL,
    set_type   TEXT NOT NULL CHECK (set_type IN ('warmup', 'work')),
    reps       INTEGER NOT NULL,
    kg         REAL NOT NULL,
    rpe        REAL,
    note       TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX training_sets_by_lift ON training_sets (lift, session_id);

-- A personal best saved from a session's set keeps a link to the session;
-- deleting the session leaves the best, without the link.
ALTER TABLE lifts ADD COLUMN session_id INTEGER REFERENCES training_sessions (id) ON DELETE SET NULL;
