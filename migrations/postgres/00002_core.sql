-- +goose Up
CREATE TABLE users (
    id             bigserial PRIMARY KEY,
    username       text NOT NULL,
    name           text NOT NULL,
    email          text NOT NULL DEFAULT '',
    phone          text NOT NULL DEFAULT '',
    role           text NOT NULL,
    source         text NOT NULL DEFAULT 'local' CHECK (source IN ('local', 'ldap')),
    password_hash  text NOT NULL,
    totp_secret    bytea,                -- encrypted with the master key
    totp_enabled   boolean NOT NULL DEFAULT false,
    active         boolean NOT NULL DEFAULT true,
    failed_logins  integer NOT NULL DEFAULT 0,
    locked_until   timestamptz,
    last_login_at  timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_key ON users (lower(username));

CREATE TABLE sessions (
    id           text PRIMARY KEY,       -- sha256 of the session token
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    mfa_passed   boolean NOT NULL DEFAULT false,
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE audit_log (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    source      text NOT NULL,
    actor_id    bigint REFERENCES users (id),
    actor       text NOT NULL,
    ip          text NOT NULL DEFAULT '',
    action      text NOT NULL,
    object_type text NOT NULL DEFAULT '',
    object_id   text NOT NULL DEFAULT '',
    details     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_at_idx ON audit_log (at DESC);

-- The single installed license and its online state.
CREATE TABLE license_state (
    id                    integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    token                 text,
    installed_at          timestamptz,
    last_heartbeat_at     timestamptz,
    last_heartbeat_status text NOT NULL DEFAULT '',
    last_error            text NOT NULL DEFAULT '',
    max_seen_time         timestamptz        -- latest wall-clock time observed, for rollback detection
);
INSERT INTO license_state (id) VALUES (1);

-- +goose Down
DROP TABLE license_state;
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE users;
