-- +goose Up
CREATE TABLE device_groups (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX device_groups_name_key ON device_groups (lower(name));

CREATE TABLE devices (
    id              bigserial PRIMARY KEY,
    name            text NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('fortigate', 'fortiweb')),
    host            text NOT NULL,
    https_port      integer NOT NULL DEFAULT 443,
    ssh_port        integer NOT NULL DEFAULT 22,
    transport_order text NOT NULL DEFAULT 'api_first'
                    CHECK (transport_order IN ('api_first', 'ssh_first', 'api_only', 'ssh_only')),
    group_id        bigint REFERENCES device_groups (id) ON DELETE SET NULL,
    tags            text[] NOT NULL DEFAULT '{}',
    credentials     bytea NOT NULL,           -- sealed JSON with the master key
    tls_pin         text NOT NULL DEFAULT '', -- SHA-256 of the device certificate
    ssh_host_key    text NOT NULL DEFAULT '', -- authorized_keys format

    -- Discovered by Probe.
    serial          text NOT NULL DEFAULT '',
    model           text NOT NULL DEFAULT '',
    firmware        text NOT NULL DEFAULT '',
    build           text NOT NULL DEFAULT '',
    hostname        text NOT NULL DEFAULT '',
    ha_mode         text NOT NULL DEFAULT '',
    ha_peers        text[] NOT NULL DEFAULT '{}',
    vdoms           text[] NOT NULL DEFAULT '{}',
    fortimanager    boolean NOT NULL DEFAULT false,

    status          text NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'online', 'offline', 'error')),
    last_error      text NOT NULL DEFAULT '',
    last_seen_at    timestamptz,
    last_probe_at   timestamptz,
    offline_since   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX devices_serial_key ON devices (serial) WHERE serial <> '';
CREATE UNIQUE INDEX devices_name_key ON devices (lower(name));

-- Latest health and signature snapshot per device.
CREATE TABLE device_status (
    device_id     bigint PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    at            timestamptz NOT NULL,
    cpu           double precision NOT NULL DEFAULT 0,
    mem           double precision NOT NULL DEFAULT 0,
    disk          double precision NOT NULL DEFAULT -1,
    sessions      bigint NOT NULL DEFAULT 0,
    uptime        bigint NOT NULL DEFAULT 0,
    interfaces    jsonb NOT NULL DEFAULT '[]',
    signatures    jsonb NOT NULL DEFAULT '[]',
    contracts     jsonb NOT NULL DEFAULT '[]',
    signatures_at timestamptz
);

-- Health time series (moves to ClickHouse with the reports module).
CREATE TABLE device_metrics (
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    at        timestamptz NOT NULL,
    cpu       double precision NOT NULL,
    mem       double precision NOT NULL,
    disk      double precision NOT NULL,
    sessions  bigint NOT NULL,
    PRIMARY KEY (device_id, at)
);

CREATE TABLE notification_channels (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('email', 'webhook', 'syslog')),
    config     bytea NOT NULL,                -- sealed JSON
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alert_rules (
    id               bigserial PRIMARY KEY,
    name             text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('offline', 'cpu', 'mem', 'disk', 'contract_expiry', 'license_expiry')),
    threshold        double precision NOT NULL DEFAULT 0, -- percent, or days for expiry rules
    duration_minutes integer NOT NULL DEFAULT 0,          -- condition must hold this long
    severity         text NOT NULL DEFAULT 'warning' CHECK (severity IN ('info', 'warning', 'critical')),
    group_id         bigint REFERENCES device_groups (id) ON DELETE CASCADE, -- NULL: all devices
    channel_ids      bigint[] NOT NULL DEFAULT '{}',
    repeat_minutes   integer NOT NULL DEFAULT 0,          -- re-notify while open; 0 = once
    enabled          boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alerts (
    id               bigserial PRIMARY KEY,
    rule_id          bigint NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    device_id        bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    subject          text NOT NULL DEFAULT '',   -- e.g. the contract name
    severity         text NOT NULL,
    message          text NOT NULL,
    state            text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'resolved')),
    opened_at        timestamptz NOT NULL DEFAULT now(),
    resolved_at      timestamptz,
    last_notified_at timestamptz,
    acked_by         text NOT NULL DEFAULT '',
    acked_at         timestamptz
);
CREATE UNIQUE INDEX alerts_open_key ON alerts (rule_id, device_id, subject) WHERE state = 'open';
CREATE INDEX alerts_opened_idx ON alerts (opened_at DESC);

-- Conditions seen but not yet held for the rule's duration.
CREATE TABLE alert_pending (
    rule_id   bigint NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    subject   text NOT NULL DEFAULT '',
    since     timestamptz NOT NULL,
    PRIMARY KEY (rule_id, device_id, subject)
);

INSERT INTO alert_rules (name, kind, threshold, duration_minutes, severity) VALUES
    ('Device unreachable',          'offline',         0,  3, 'critical'),
    ('High CPU',                    'cpu',             90, 5, 'warning'),
    ('High memory',                 'mem',             90, 5, 'warning'),
    ('Disk almost full',            'disk',            90, 0, 'warning'),
    ('FortiGuard contract expiring','contract_expiry', 30, 0, 'warning'),
    ('TaktaPlus license expiring',  'license_expiry',  30, 0, 'warning');

-- +goose Down
DROP TABLE alert_pending;
DROP TABLE alerts;
DROP TABLE alert_rules;
DROP TABLE notification_channels;
DROP TABLE device_metrics;
DROP TABLE device_status;
DROP TABLE devices;
DROP TABLE device_groups;
