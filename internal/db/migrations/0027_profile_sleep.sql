-- The sleep settings of a profile, moved from the site settings: the sleep
-- goal in minutes (0: the site's old setting, else 8 h), the weekdays off
-- (Go's numbers, Sunday 0; '' for Saturday and Sunday) and the region whose
-- public holidays count as days off ('' for none). Only the site owner's
-- are used: the sleep on the site is theirs.
ALTER TABLE profiles ADD COLUMN sleep_goal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE profiles ADD COLUMN free_days TEXT NOT NULL DEFAULT '';
ALTER TABLE profiles ADD COLUMN holidays TEXT NOT NULL DEFAULT '';
