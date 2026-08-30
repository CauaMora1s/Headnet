-- User accounts.
--
-- Two columns hold the email address. `email` is what the user typed, so it
-- can be displayed back to them faithfully; `email_norm` is the lower-cased
-- form and carries the unique index. Without that split, Ada@example.com and
-- ada@example.com would be two separate accounts, which is a confusing way to
-- lose access to your own network.
--
-- `password_hash` is nullable because an account authenticated by an external
-- identity provider has no local password. It stores an Argon2id hash in PHC
-- string format, which carries its own parameters, so the cost can be raised
-- later without invalidating existing passwords.
CREATE TABLE users (
    id            TEXT NOT NULL PRIMARY KEY,
    email         TEXT NOT NULL,
    email_norm    TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT,
    role          TEXT NOT NULL,
    provider      TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ
);

CREATE INDEX idx_users_role ON users (role);
