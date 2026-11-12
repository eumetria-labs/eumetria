-- How each second factor was set up (pentest re-test N5): with an enrolment
-- link ('link'), by an account that already needed and had a factor
-- ('elevated'), by one that needed none ('plain'), or before this was
-- recorded ('unknown'). When an account starts to need a factor, the
-- 'plain' and 'unknown' ones are removed: whoever knew a viewer's password
-- could have set them up. Also from where and under which role.
ALTER TABLE totp ADD COLUMN enrolled_via TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE totp ADD COLUMN created_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE totp ADD COLUMN created_role TEXT NOT NULL DEFAULT '';
ALTER TABLE webauthn_credentials ADD COLUMN enrolled_via TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE webauthn_credentials ADD COLUMN created_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE webauthn_credentials ADD COLUMN created_role TEXT NOT NULL DEFAULT '';
