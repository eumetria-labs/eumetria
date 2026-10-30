-- An admin can require a user to set up a second factor (PLAN M5b): until
-- they have one, they get no data, only their security settings.
ALTER TABLE users ADD COLUMN require_second_factor INTEGER NOT NULL DEFAULT 0;
