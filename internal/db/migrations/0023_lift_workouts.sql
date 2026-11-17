-- A lift result can belong to a Fitbit workout, like a benchmark result
-- (0012_wod_links.sql): the session it was lifted in (fitbit_workout, the
-- workout's id as the page knows it; '' for none). The server checks the
-- workout exists when it is linked and takes its date.
ALTER TABLE lifts ADD COLUMN fitbit_workout TEXT NOT NULL DEFAULT '';
