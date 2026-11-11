-- +goose Up
CREATE TABLE system_backups (
    id          bigserial PRIMARY KEY,
    trigger     text NOT NULL,              -- manual, schedule, pre_upgrade
    status      text NOT NULL DEFAULT 'running', -- running, ok, failed
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    file        text NOT NULL DEFAULT '',
    size        bigint NOT NULL DEFAULT 0,
    sftp_status text NOT NULL DEFAULT '',   -- '', ok, failed
    error       text NOT NULL DEFAULT '',
    created_by  text NOT NULL DEFAULT ''
);
CREATE INDEX system_backups_started ON system_backups (started_at DESC);

CREATE TABLE system_upgrades (
    id           bigserial PRIMARY KEY,
    version      text NOT NULL,
    from_version text NOT NULL,
    notes        text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'queued', -- queued, running, done, failed, rolled_back
    step         text NOT NULL DEFAULT '',
    message      text NOT NULL DEFAULT '',
    log          text NOT NULL DEFAULT '',
    uploaded_by  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);

-- +goose Down
DROP TABLE system_upgrades;
DROP TABLE system_backups;
