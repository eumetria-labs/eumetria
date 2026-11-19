-- Body measurements from a Withings scale, one row per weigh-in (Withings'
-- measure group), mirrored from the history like the other synced data
-- (internal/store): the JSON in data, the columns for queries. date is the
-- local day of the weigh-in.
CREATE TABLE body_measures (
    grpid       TEXT PRIMARY KEY,
    date        TEXT NOT NULL,
    measured_at TEXT NOT NULL,
    weight      REAL,
    fat_ratio   REAL,
    data        TEXT NOT NULL
) STRICT;

CREATE INDEX body_measures_by_date ON body_measures (date);
