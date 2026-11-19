-- What a user says about their body, for the nutrients' reference values
-- (stats, nutrients.go) and the birthday greeting: sex ('', 'male' or
-- 'female') and date of birth ('YYYY-MM-DD' or '').
ALTER TABLE profiles ADD COLUMN sex TEXT NOT NULL DEFAULT '';
ALTER TABLE profiles ADD COLUMN birth_date TEXT NOT NULL DEFAULT '';
