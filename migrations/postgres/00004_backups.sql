-- +goose Up
CREATE TABLE backup_policies (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    group_id    bigint REFERENCES device_groups (id) ON DELETE CASCADE, -- NULL with device_id NULL: all devices
    device_id   bigint REFERENCES devices (id) ON DELETE CASCADE,
    schedule    text NOT NULL,                 -- 5-field cron expression, server time zone
    keep_count  integer NOT NULL DEFAULT 30,   -- 0 = no count limit
    keep_days   integer NOT NULL DEFAULT 0,    -- 0 = no age limit
    enabled     boolean NOT NULL DEFAULT true,
    last_run_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (group_id IS NULL OR device_id IS NULL)
);

CREATE TABLE backups (
    id           bigserial PRIMARY KEY,
    device_id    bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    at           timestamptz NOT NULL DEFAULT now(),
    reason       text NOT NULL CHECK (reason IN ('scheduled', 'manual', 'pre_restore', 'on_change')),
    actor        text NOT NULL DEFAULT '',
    status       text NOT NULL CHECK (status IN ('ok', 'failed')),
    error        text NOT NULL DEFAULT '',
    sha256       text NOT NULL DEFAULT '',     -- of the normalized configuration
    size         bigint NOT NULL DEFAULT 0,
    firmware     text NOT NULL DEFAULT '',
    path         text NOT NULL DEFAULT '',     -- encrypted file, relative to the backup directory
    locked       boolean NOT NULL DEFAULT false,
    note         text NOT NULL DEFAULT ''
);
CREATE INDEX backups_device_idx ON backups (device_id, at DESC);

CREATE TABLE restore_jobs (
    id            bigserial PRIMARY KEY,
    device_id     bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    backup_id     bigint NOT NULL REFERENCES backups (id) ON DELETE CASCADE,
    pre_backup_id bigint REFERENCES backups (id) ON DELETE SET NULL,
    actor         text NOT NULL,
    status        text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'waiting', 'succeeded', 'failed')),
    error         text NOT NULL DEFAULT '',
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE UNIQUE INDEX restore_jobs_active_key ON restore_jobs (device_id) WHERE status IN ('running', 'waiting');

INSERT INTO backup_policies (name, schedule, keep_count) VALUES ('Daily', '30 1 * * *', 30);

-- +goose Down
DROP TABLE restore_jobs;
DROP TABLE backups;
DROP TABLE backup_policies;
