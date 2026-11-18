-- The synced data (Yazio, Google Health, Open Food Facts, the weekly
-- reviews), until now only in history.json (internal/store). Each record
-- keeps its exact JSON in `data`, which is what is read back; the other
-- columns are taken from it on every write, for queries. Nested detail
-- (meals, sleep stages, heart rate, routes, ingredients) stays in `data`.
--
-- food_entries and workouts are indexes over the days: rebuilt whenever
-- their day changes, never read back into the history.

CREATE TABLE yazio_days (
    date            TEXT PRIMARY KEY,
    version         INTEGER NOT NULL DEFAULT 0,
    fetched_at      TEXT NOT NULL DEFAULT '',
    energy          REAL NOT NULL DEFAULT 0,
    protein         REAL NOT NULL DEFAULT 0,
    carb            REAL NOT NULL DEFAULT 0,
    fat             REAL NOT NULL DEFAULT 0,
    energy_goal     REAL,
    steps           REAL NOT NULL DEFAULT 0,
    activity_energy REAL NOT NULL DEFAULT 0,
    water           REAL NOT NULL DEFAULT 0,
    weight          REAL,
    data            TEXT NOT NULL
) STRICT;

CREATE TABLE food_entries (
    date      TEXT NOT NULL REFERENCES yazio_days (date) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    entry_id  TEXT NOT NULL DEFAULT '',
    daytime   TEXT NOT NULL DEFAULT '',
    name      TEXT NOT NULL DEFAULT '',
    amount    REAL NOT NULL DEFAULT 0,
    unit      TEXT NOT NULL DEFAULT '',
    energy    REAL NOT NULL DEFAULT 0,
    protein   REAL NOT NULL DEFAULT 0,
    carb      REAL NOT NULL DEFAULT 0,
    fat       REAL NOT NULL DEFAULT 0,
    fiber     REAL,
    sugar     REAL,
    saturated REAL,
    ai        INTEGER NOT NULL DEFAULT 0,
    recipe_id TEXT NOT NULL DEFAULT '',
    ean       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (date, position)
) STRICT;

CREATE INDEX food_entries_by_name ON food_entries (name);

CREATE TABLE foods (
    key     TEXT PRIMARY KEY,
    name    TEXT NOT NULL DEFAULT '',
    unit    TEXT NOT NULL DEFAULT '',
    missing INTEGER NOT NULL DEFAULT 0,
    data    TEXT NOT NULL
) STRICT;

CREATE TABLE health_days (
    date         TEXT PRIMARY KEY,
    fetched_at   TEXT NOT NULL DEFAULT '',
    resting_hr   REAL NOT NULL DEFAULT 0,
    hrv          REAL NOT NULL DEFAULT 0,
    sleep_start  TEXT NOT NULL DEFAULT '',
    sleep_end    TEXT NOT NULL DEFAULT '',
    asleep       REAL NOT NULL DEFAULT 0,
    awake        REAL NOT NULL DEFAULT 0,
    deep         REAL NOT NULL DEFAULT 0,
    light        REAL NOT NULL DEFAULT 0,
    rem          REAL NOT NULL DEFAULT 0,
    respiratory  REAL NOT NULL DEFAULT 0,
    spo2         REAL NOT NULL DEFAULT 0,
    skin_temp    REAL NOT NULL DEFAULT 0,
    total_kcal   REAL NOT NULL DEFAULT 0,
    vo2max       REAL NOT NULL DEFAULT 0,
    data         TEXT NOT NULL
) STRICT;

CREATE TABLE workouts (
    id          TEXT PRIMARY KEY,
    date        TEXT NOT NULL REFERENCES health_days (date) ON DELETE CASCADE,
    type        TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    start       TEXT NOT NULL DEFAULT '',
    end         TEXT NOT NULL DEFAULT '',
    active_sec  REAL NOT NULL DEFAULT 0,
    calories    REAL NOT NULL DEFAULT 0,
    avg_hr      REAL NOT NULL DEFAULT 0,
    zone_min    REAL NOT NULL DEFAULT 0,
    distance_m  REAL NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX workouts_by_date ON workouts (date);

CREATE TABLE off_products (
    ean        TEXT PRIMARY KEY,
    nutriscore TEXT NOT NULL DEFAULT '',
    nova       INTEGER NOT NULL DEFAULT 0,
    not_found  INTEGER NOT NULL DEFAULT 0,
    data       TEXT NOT NULL
) STRICT;

CREATE TABLE weekly_reviews (
    week       TEXT PRIMARY KEY,
    model      TEXT NOT NULL DEFAULT '',
    cost_usd   REAL NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT '',
    data       TEXT NOT NULL
) STRICT;

-- The rest of the history, one JSON value each: health_from, devices,
-- health_sync, review_state.
CREATE TABLE synced_meta (
    key  TEXT PRIMARY KEY,
    data TEXT NOT NULL
) STRICT;
