-- The coach chat (phase 12): a person's conversations with Claude about
-- their data. Each thread belongs to one user and is seen only by them.
CREATE TABLE coach_threads (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX coach_threads_by_user ON coach_threads (user_id, updated_at);

-- One question or answer. text is what the page shows; tools the data the
-- answer looked at ([{name, args}]); api the Messages API turns of the
-- answer (assistant turns with tool calls and their results), so a follow-up
-- question sends the conversation as it was. status: done, failed;
-- queued and running come with the subscription backend. backend and model
-- say who wrote it; the tokens and cost are the API's (cost 0: unknown).
CREATE TABLE coach_messages (
    id            INTEGER PRIMARY KEY,
    thread_id     INTEGER NOT NULL REFERENCES coach_threads (id) ON DELETE CASCADE,
    role          TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    text          TEXT NOT NULL DEFAULT '',
    tools         TEXT NOT NULL DEFAULT '[]',
    api           TEXT NOT NULL DEFAULT '[]',
    status        TEXT NOT NULL DEFAULT 'done' CHECK (status IN ('done', 'failed', 'queued', 'running')),
    error         TEXT NOT NULL DEFAULT '',
    backend       TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read    INTEGER NOT NULL DEFAULT 0,
    cache_write   INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX coach_messages_by_thread ON coach_messages (thread_id, id);
