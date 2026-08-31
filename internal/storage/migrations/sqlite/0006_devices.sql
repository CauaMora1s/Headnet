-- Devices on the network.
--
-- Note what is absent: there is no private key column, and no API shape with
-- anywhere to put one. A device generates its own key pair and sends only the
-- public half. That is what makes a stolen control plane survivable — there is
-- nothing here that could decrypt any traffic.
--
-- The unique constraint on `public_key` is deliberately permanent. A revoked
-- device's key stays blocked forever: a device that loses its key re-enrols and
-- generates a new one, so nothing legitimate needs the old one back, and a
-- revoked key that could be registered again would not really be revoked.
--
-- Addresses are recorded here for display and for the peer configuration that
-- Phase 3 will distribute. The allocation itself lives in ip_allocations, whose
-- primary key is what actually stops an address being issued twice; these
-- columns follow it inside the same transaction.
--
-- `enrolled_with` is ON DELETE SET NULL rather than CASCADE: deleting a setup
-- key must not delete the devices it enrolled. Losing the audit trail would be
-- bad; losing the machines would be worse.
CREATE TABLE devices (
    id            TEXT NOT NULL PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    public_key    TEXT NOT NULL UNIQUE,
    os            TEXT NOT NULL DEFAULT '',
    hostname      TEXT NOT NULL DEFAULT '',
    ipv4          TEXT,
    ipv6          TEXT,
    enrolled_with TEXT REFERENCES setup_keys (id) ON DELETE SET NULL,
    created_at    TIMESTAMP NOT NULL,
    last_seen_at  TIMESTAMP,
    revoked_at    TIMESTAMP
);

CREATE INDEX idx_devices_user ON devices (user_id);
CREATE INDEX idx_devices_revoked ON devices (revoked_at);
