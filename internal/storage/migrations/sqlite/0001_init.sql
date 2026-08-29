-- Server-scoped key/value metadata.
--
-- The control plane needs a small amount of state that belongs to the
-- deployment itself rather than to any user or device: the instance identity
-- generated on first start, and later the schema provenance markers that make
-- a restored backup recognisable.
--
-- Nothing secret belongs in this table. Key material lives on client devices,
-- and server-side secrets are supplied through configuration.
CREATE TABLE server_metadata (
    key        TEXT PRIMARY KEY,
    value      TEXT      NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
