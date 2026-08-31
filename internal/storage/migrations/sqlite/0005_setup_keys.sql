-- Setup keys: credentials that enrol a device without an interactive login.
--
-- A setup key is a credential, so it is stored the way every other credential
-- here is stored: only SHA-256 of it. Reading this table yields nothing that
-- can enrol anything. The key itself is shown exactly once, at creation.
--
-- `display_hint` holds only the leading characters, so an operator can
-- recognise a key in a list without the list being a source of usable keys.
--
-- `expires_at` is nullable, but null has to be chosen deliberately: a key
-- created without an expiry gets a default one. A credential that never
-- expires is a credential you will still be finding in shell histories years
-- from now.
--
-- `max_uses` of 0 means unlimited; 1 makes the key single-use, which is the
-- right choice for one-off provisioning.
--
-- `tags` carries the key's scope. Phase 6's policy engine reads it. Nothing
-- enforces it yet, and that is stated rather than implied.
CREATE TABLE setup_keys (
    id           TEXT      NOT NULL PRIMARY KEY,
    key_hash     TEXT      NOT NULL UNIQUE,
    display_hint TEXT      NOT NULL,
    description  TEXT      NOT NULL DEFAULT '',
    tags         TEXT      NOT NULL DEFAULT '',
    created_by   TEXT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP,
    max_uses     INTEGER   NOT NULL DEFAULT 0,
    uses         INTEGER   NOT NULL DEFAULT 0,
    revoked_at   TIMESTAMP
);

CREATE INDEX idx_setup_keys_created_by ON setup_keys (created_by);
