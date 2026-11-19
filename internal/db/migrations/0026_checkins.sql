-- The daily check-in: how the day felt, from 1 (low) to 5 (high), each
-- optional, and a short note. And tags that explain a day: some are set by
-- people, "alcohol" also by the page from the foods logged; a tag the page
-- would set that someone removed is kept as 'dismissed', so it stays away.
CREATE TABLE checkins (
    date       TEXT PRIMARY KEY,
    energy     INTEGER CHECK (energy BETWEEN 1 AND 5),
    soreness   INTEGER CHECK (soreness BETWEEN 1 AND 5),
    stress     INTEGER CHECK (stress BETWEEN 1 AND 5),
    mood       INTEGER CHECK (mood BETWEEN 1 AND 5),
    note       TEXT NOT NULL DEFAULT '',
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE day_tags (
    date   TEXT NOT NULL,
    tag    TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('user', 'dismissed')),
    PRIMARY KEY (date, tag)
) STRICT;
