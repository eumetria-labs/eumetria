-- Profiles (PLAN M7): what people say about themselves, all optional. A row
-- exists once they saved something. avatar is the picture's version (0: none),
-- which goes into its address; the file is avatars/<user id>.jpg in the data
-- folder.
CREATE TABLE profiles (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    first_name TEXT NOT NULL DEFAULT '',
    surname    TEXT NOT NULL DEFAULT '',
    email      TEXT NOT NULL DEFAULT '',
    instagram  TEXT NOT NULL DEFAULT '',
    avatar     INTEGER NOT NULL DEFAULT 0
) STRICT;
