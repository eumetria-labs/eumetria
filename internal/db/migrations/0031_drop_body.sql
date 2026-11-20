-- The Withings integration is gone (2026-11-20): Withings put API access
-- behind a subscription. Its table and settings go with it. 0029_body.sql
-- stays, as migrations are numbered without gaps.
DROP TABLE IF EXISTS body_measures;
DELETE FROM settings WHERE key LIKE 'withings%';
DELETE FROM synced_meta WHERE key IN ('body_sync', 'body_update');
