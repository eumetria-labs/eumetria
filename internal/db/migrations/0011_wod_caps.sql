-- Time caps: a workout for time may have one (time_cap, in seconds, 0 for
-- none), and a result that didn't finish in it is capped: its value is
-- then the reps done, not seconds. Every finished time beats every capped
-- result; among capped ones, more reps are better.
ALTER TABLE wods ADD COLUMN time_cap INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wod_results ADD COLUMN capped INTEGER NOT NULL DEFAULT 0;
