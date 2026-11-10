-- A benchmark result can belong to a Fitbit workout: the session it was
-- done in (fitbit_workout, the workout's id as the page knows it; '' for
-- none). The server checks the workout exists when it is linked.
ALTER TABLE wod_results ADD COLUMN fitbit_workout TEXT NOT NULL DEFAULT '';
