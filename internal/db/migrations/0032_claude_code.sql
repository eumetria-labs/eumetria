-- Claude Code as a second AI backend (phase 12b). ai_state keeps what the
-- backends need across restarts, e.g. "claude_limit_until" (Unix seconds):
-- until then the subscription's usage limit is reached and jobs wait.
CREATE TABLE ai_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

-- A coach answer that waits for the limit to reset: status 'queued', due
-- at queued_until (Unix seconds); 0 for every other answer.
ALTER TABLE coach_messages ADD COLUMN queued_until INTEGER NOT NULL DEFAULT 0;
