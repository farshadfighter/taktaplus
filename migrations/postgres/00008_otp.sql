-- +goose Up
-- Panel settings, sealed with the master key (sms.ir key, LDAP bind, relay).
CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sms_messages (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    mobile      text NOT NULL,
    purpose     text NOT NULL,                -- radius, gateway, test
    username    text NOT NULL DEFAULT '',
    device_id   bigint REFERENCES devices (id) ON DELETE SET NULL,
    status      text NOT NULL CHECK (status IN ('sent', 'failed', 'blocked')),
    provider_id text NOT NULL DEFAULT '',
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX sms_messages_at_idx ON sms_messages (at DESC);
CREATE INDEX sms_messages_mobile_idx ON sms_messages (mobile, at DESC);

-- Users authenticated by the RADIUS server with an SMS code.
CREATE TABLE otp_users (
    id            bigserial PRIMARY KEY,
    username      text NOT NULL,
    name          text NOT NULL DEFAULT '',
    mobile        text NOT NULL,
    source        text NOT NULL CHECK (source IN ('local', 'ldap')),
    password_hash text NOT NULL DEFAULT '',   -- local users
    fortinet_group text NOT NULL DEFAULT '',  -- returned as Fortinet-Group-Name
    active        boolean NOT NULL DEFAULT true,
    failed        integer NOT NULL DEFAULT 0,
    locked_until  timestamptz,
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX otp_users_username_key ON otp_users (lower(username));

-- FortiGate local users with "two-factor sms", seen by the user sync; they
-- count towards the license like otp_users.
CREATE TABLE fortigate_sms_users (
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    username  text NOT NULL,
    mobile    text NOT NULL DEFAULT '',
    seen_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, username)
);

-- Per-device RADIUS shared secrets.
CREATE TABLE radius_clients (
    device_id bigint PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    secret    bytea NOT NULL,                 -- sealed
    enabled   boolean NOT NULL DEFAULT true
);

CREATE TABLE otp_events (
    id        bigserial PRIMARY KEY,
    at        timestamptz NOT NULL DEFAULT now(),
    username  text NOT NULL,
    device_id bigint REFERENCES devices (id) ON DELETE SET NULL,
    client_ip text NOT NULL DEFAULT '',
    event     text NOT NULL,                  -- challenge, accept, reject, locked
    detail    text NOT NULL DEFAULT ''
);
CREATE INDEX otp_events_at_idx ON otp_events (at DESC);

-- +goose Down
DROP TABLE otp_events;
DROP TABLE radius_clients;
DROP TABLE fortigate_sms_users;
DROP TABLE otp_users;
DROP TABLE sms_messages;
DROP TABLE settings;
