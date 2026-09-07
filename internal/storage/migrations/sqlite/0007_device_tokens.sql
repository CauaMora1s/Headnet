-- Device credentials are returned once at enrolment; only SHA-256 is stored.
-- Existing inventory records deliberately receive no credential during upgrade.
-- Deletion cascades; revocation deletes the token in the revocation transaction.
-- Rolling back this migration destroys credentials and requires re-enrolment.
CREATE TABLE device_tokens (
    device_id  TEXT NOT NULL PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL
);
