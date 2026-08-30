-- Server-side sessions.
--
-- Sessions are stored rather than being self-contained tokens, deliberately.
-- When a laptop is stolen an administrator has to be able to cut off access
-- now, and a stateless token cannot be revoked before it expires. The usual
-- workaround for that is a revocation list, which is a session table with
-- extra steps.
--
-- Neither secret is stored. `token_hash` and `csrf_hash` hold SHA-256 of the
-- values the client holds, so reading this table yields nothing that can be
-- replayed. SHA-256 rather than Argon2id is right here: the input is 32 bytes
-- of CSPRNG output, so there is no low-entropy guess to slow down.
--
-- ON DELETE CASCADE is why foreign keys are pinned on in the SQLite DSN.
-- Deleting a user must not leave their sessions behind and usable.
CREATE TABLE sessions (
    id           TEXT NOT NULL PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    csrf_hash    TEXT NOT NULL,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);
