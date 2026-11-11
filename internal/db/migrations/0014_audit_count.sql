-- Repeated failures (the same failed sign-in from the same address within
-- a while) are one row with a count, not one row each (pentest F20): a
-- flood of guesses can't grow the log, and the backups, without bound, nor
-- bury what matters.
ALTER TABLE audit_log ADD COLUMN count INTEGER NOT NULL DEFAULT 1;
CREATE INDEX audit_log_repeat ON audit_log (action, ip, at);
