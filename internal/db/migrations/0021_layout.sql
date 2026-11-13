-- The order of the dashboard's cards and the ones a user hid, as JSON
-- ({"order": [...], "hidden": [...]}), so it follows them to every device.
-- Empty: the page's own order with every card.
ALTER TABLE profiles ADD COLUMN layout TEXT NOT NULL DEFAULT '';
