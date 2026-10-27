-- +goose Up
-- How many approvals changes to a group's devices need (group_id NULL is
-- the default). A change needs the highest count among its targets.
CREATE TABLE approval_policies (
    id        bigserial PRIMARY KEY,
    group_id  bigint REFERENCES device_groups (id) ON DELETE CASCADE,
    required  integer NOT NULL CHECK (required BETWEEN 0 AND 2)
);
CREATE UNIQUE INDEX approval_policies_group_key ON approval_policies (coalesce(group_id, 0));
INSERT INTO approval_policies (group_id, required) VALUES (NULL, 1);

-- Recurring windows in which approved changes run (group_id NULL: all).
CREATE TABLE maintenance_windows (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    group_id   bigint REFERENCES device_groups (id) ON DELETE CASCADE,
    weekdays   integer[] NOT NULL,           -- 0 = Sunday … 6 = Saturday
    start_time text NOT NULL,                -- HH:MM in the server time zone
    minutes    integer NOT NULL CHECK (minutes BETWEEN 15 AND 1440),
    enabled    boolean NOT NULL DEFAULT true
);

CREATE TABLE changes (
    id            bigserial PRIMARY KEY,
    kind          text NOT NULL,             -- deployment, script, firmware
    title         text NOT NULL,
    description   text NOT NULL DEFAULT '',
    spec          jsonb NOT NULL,            -- kind-specific
    status        text NOT NULL DEFAULT 'draft' CHECK (status IN
                  ('draft', 'pending', 'approved', 'running', 'succeeded', 'failed', 'partial', 'cancelled', 'rejected')),
    run_mode      text NOT NULL DEFAULT 'window' CHECK (run_mode IN ('window', 'at', 'now')),
    run_at        timestamptz,               -- run_mode 'at'
    urgent_reason text NOT NULL DEFAULT '',  -- run_mode 'now' skips the window
    concurrency   integer NOT NULL DEFAULT 4 CHECK (concurrency BETWEEN 1 AND 50),
    canary        integer NOT NULL DEFAULT 0 CHECK (canary >= 0), -- devices run first, alone
    max_failures  integer NOT NULL DEFAULT 0 CHECK (max_failures >= 0), -- stop after more failures
    backup_first  boolean NOT NULL DEFAULT true,
    required_approvals integer NOT NULL DEFAULT 0,
    created_by    bigint REFERENCES users (id),
    creator       text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    submitted_at  timestamptz,
    started_at    timestamptz,
    finished_at   timestamptz
);
CREATE INDEX changes_status_idx ON changes (status, created_at DESC);

CREATE TABLE change_targets (
    id            bigserial PRIMARY KEY,
    change_id     bigint NOT NULL REFERENCES changes (id) ON DELETE CASCADE,
    device_id     bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    position      integer NOT NULL,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    preview       text NOT NULL DEFAULT '',  -- what will run on this device
    output        text NOT NULL DEFAULT '',
    error         text NOT NULL DEFAULT '',
    pre_backup_id bigint REFERENCES backups (id) ON DELETE SET NULL,
    started_at    timestamptz,
    finished_at   timestamptz,
    UNIQUE (change_id, device_id)
);

CREATE TABLE change_approvals (
    id        bigserial PRIMARY KEY,
    change_id bigint NOT NULL REFERENCES changes (id) ON DELETE CASCADE,
    user_id   bigint REFERENCES users (id),
    username  text NOT NULL,
    decision  text NOT NULL CHECK (decision IN ('approve', 'reject')),
    comment   text NOT NULL DEFAULT '',
    at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (change_id, user_id)
);

ALTER TABLE backups DROP CONSTRAINT backups_reason_check;
ALTER TABLE backups ADD CONSTRAINT backups_reason_check CHECK (reason IN ('scheduled', 'manual', 'pre_restore', 'on_change', 'pre_change'));

-- +goose Down
ALTER TABLE backups DROP CONSTRAINT backups_reason_check;
ALTER TABLE backups ADD CONSTRAINT backups_reason_check CHECK (reason IN ('scheduled', 'manual', 'pre_restore', 'on_change'));
DROP TABLE change_approvals;
DROP TABLE change_targets;
DROP TABLE changes;
DROP TABLE maintenance_windows;
DROP TABLE approval_policies;
