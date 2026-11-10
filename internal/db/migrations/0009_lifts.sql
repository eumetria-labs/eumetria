-- Personal bests in lifts (Power Clean, Strict Press, …), written in the
-- Training card. Each row is one result on one day; a lift's best is its
-- heaviest row, the others are its history. lift is the name as typed or
-- picked from the page's list; reps is 1 for a one-rep max.
CREATE TABLE lifts (
    id         INTEGER PRIMARY KEY,
    lift       TEXT NOT NULL,
    kg         REAL NOT NULL,
    reps       INTEGER NOT NULL DEFAULT 1,
    date       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users (id),
    updated_by INTEGER NOT NULL REFERENCES users (id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX lifts_by_lift ON lifts (lift, date);
