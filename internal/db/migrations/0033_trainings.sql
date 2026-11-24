-- One Training per workout (PLAN phase 14a). A training session becomes
-- the hub of what was done: it wraps zero or more device workouts (a Fitbit
-- workout now, other sources later), and the note, the lifts, the benchmark
-- results and the sets of that training belong to it, not to a Fitbit id.
--
-- The old links (lifts.fitbit_workout, wod_results.fitbit_workout,
-- training_sessions.fitbit_workout and .note, the notes table) stay as they
-- are for one release, so the previous image still works on this database.
-- A later migration drops them.

-- A device workout belongs to at most one training.
CREATE TABLE training_workouts (
    session_id INTEGER NOT NULL REFERENCES training_sessions (id) ON DELETE CASCADE,
    source     TEXT NOT NULL,
    workout_id TEXT NOT NULL,
    PRIMARY KEY (source, workout_id)
) STRICT;
CREATE INDEX training_workouts_by_session ON training_workouts (session_id);

-- The training's note: Markdown, one per training, with its images.
CREATE TABLE training_notes (
    session_id INTEGER PRIMARY KEY REFERENCES training_sessions (id) ON DELETE CASCADE,
    markdown   TEXT NOT NULL,
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- lifts.session_id exists (0028) and now means the training.
ALTER TABLE wod_results ADD COLUMN session_id INTEGER REFERENCES training_sessions (id) ON DELETE SET NULL;

-- 1. Sessions linked to a Fitbit workout. Two sessions on one workout: the
-- older one gets it, the other stays without.
INSERT OR IGNORE INTO training_workouts (session_id, source, workout_id)
    SELECT id, 'fitbit', fitbit_workout FROM training_sessions WHERE fitbit_workout <> '' ORDER BY id;

-- 2. A training for every other workout something was noted in: its day
-- from the synced workout, else from the entries, else from when the note
-- was written.
INSERT INTO training_sessions (date, fitbit_workout, title, note, rpe, created_by, updated_by, created_at, updated_at)
    SELECT coalesce((SELECT w.date FROM workouts w WHERE w.id = r.wid), min(r.day), date(min(r.ts), 'unixepoch')),
           r.wid, '', '', NULL, min(r.uid), min(r.uid), min(r.ts), min(r.ts)
    FROM (
        SELECT workout_id AS wid, NULL AS day, created_by AS uid, created_at AS ts FROM notes
        UNION ALL SELECT fitbit_workout, date, created_by, created_at FROM lifts WHERE fitbit_workout <> ''
        UNION ALL SELECT fitbit_workout, date, created_by, created_at FROM wod_results WHERE fitbit_workout <> ''
    ) r
    WHERE NOT EXISTS (SELECT 1 FROM training_workouts tw WHERE tw.source = 'fitbit' AND tw.workout_id = r.wid)
    GROUP BY r.wid
    ORDER BY min(r.ts), r.wid;
INSERT OR IGNORE INTO training_workouts (session_id, source, workout_id)
    SELECT id, 'fitbit', fitbit_workout FROM training_sessions WHERE fitbit_workout <> '' ORDER BY id;

-- 3. Lifts and benchmark results join their workout's training. A best saved
-- from a session keeps that session.
UPDATE lifts SET session_id = (SELECT tw.session_id FROM training_workouts tw WHERE tw.source = 'fitbit' AND tw.workout_id = lifts.fitbit_workout)
    WHERE session_id IS NULL AND fitbit_workout <> '';
UPDATE wod_results SET session_id = (SELECT tw.session_id FROM training_workouts tw WHERE tw.source = 'fitbit' AND tw.workout_id = wod_results.fitbit_workout)
    WHERE fitbit_workout <> '';

-- 4. Notes move to their training; a session's short plain note joins it
-- (or becomes it), so a training has one note.
INSERT INTO training_notes (session_id, markdown, created_by, updated_by, created_at, updated_at)
    SELECT tw.session_id, n.markdown, n.created_by, n.updated_by, n.created_at, n.updated_at
    FROM notes n JOIN training_workouts tw ON tw.source = 'fitbit' AND tw.workout_id = n.workout_id;
UPDATE training_notes SET markdown = markdown || char(10) || char(10) || (SELECT s.note FROM training_sessions s WHERE s.id = training_notes.session_id)
    WHERE (SELECT s.note FROM training_sessions s WHERE s.id = training_notes.session_id) <> '';
INSERT INTO training_notes (session_id, markdown, created_by, updated_by, created_at, updated_at)
    SELECT id, note, created_by, updated_by, created_at, updated_at FROM training_sessions
    WHERE note <> '' AND id NOT IN (SELECT session_id FROM training_notes);
