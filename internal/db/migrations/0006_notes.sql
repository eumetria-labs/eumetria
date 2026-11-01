-- Workout notes (PLAN M6). workout_id is the ID of a Fitbit workout in the
-- synced data, which lives outside this database: a workout that disappears
-- from Google keeps its note here, and admins see such notes in Users & roles.
CREATE TABLE notes (
    workout_id TEXT PRIMARY KEY,
    markdown   TEXT NOT NULL,
    created_by INTEGER NOT NULL REFERENCES users (id),
    updated_by INTEGER NOT NULL REFERENCES users (id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
