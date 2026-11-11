-- Who uploaded which note image (pentest F21): an image is saved before
-- the note that embeds it, so until then it belongs to no note. Each user
-- may have only a few such images waiting at a time, and they go after a day.
CREATE TABLE note_uploads (
    name       TEXT PRIMARY KEY, -- the file's name in note-images/
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX note_uploads_user ON note_uploads (user_id);
