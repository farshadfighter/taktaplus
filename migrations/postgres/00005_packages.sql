-- +goose Up
-- Where packages come from: the central repository, an HTTP mirror of it,
-- or the customer's own FTP/SFTP/TFTP server.
CREATE TABLE package_sources (
    id             bigserial PRIMARY KEY,
    name           text NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('central', 'http', 'ftp', 'sftp', 'tftp')),
    config         bytea NOT NULL,              -- sealed JSON
    schedule       text NOT NULL DEFAULT '',    -- cron; empty = manual only
    enabled        boolean NOT NULL DEFAULT true,
    last_sync_at   timestamptz,
    last_status    text NOT NULL DEFAULT '',    -- ok, failed
    last_error     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX package_sources_name_key ON package_sources (lower(name));

-- The local repository; files live under DATA_DIR/repo by sha256.
CREATE TABLE packages (
    id           bigserial PRIMARY KEY,
    sha256       text NOT NULL UNIQUE,
    file         text NOT NULL,
    size         bigint NOT NULL,
    platform     text NOT NULL CHECK (platform IN ('fortigate', 'fortiweb')),
    type         text NOT NULL,
    version      text NOT NULL,
    models       text[] NOT NULL DEFAULT '{*}',
    fw_min       text NOT NULL DEFAULT '',
    fw_max       text NOT NULL DEFAULT '',
    upgrade_from text[] NOT NULL DEFAULT '{}',
    notes        text NOT NULL DEFAULT '',
    released_at  timestamptz NOT NULL DEFAULT now(),
    source_id    bigint REFERENCES package_sources (id) ON DELETE SET NULL,
    origin       text NOT NULL DEFAULT '',      -- source name, "upload" or "bundle"
    verified     boolean NOT NULL DEFAULT false, -- described by a signed manifest
    withdrawn    boolean NOT NULL DEFAULT false, -- no longer offered by its source
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX packages_kind_idx ON packages (platform, type, released_at DESC);

CREATE TABLE package_sync_runs (
    id          bigserial PRIMARY KEY,
    source_id   bigint REFERENCES package_sources (id) ON DELETE CASCADE,
    origin      text NOT NULL,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status      text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'ok', 'failed')),
    added       integer NOT NULL DEFAULT 0,
    seen        integer NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT '',
    actor       text NOT NULL DEFAULT ''
);
CREATE INDEX package_sync_runs_idx ON package_sync_runs (started_at DESC);

-- +goose Down
DROP TABLE package_sync_runs;
DROP TABLE packages;
DROP TABLE package_sources;
