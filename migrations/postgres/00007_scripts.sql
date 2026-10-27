-- +goose Up
CREATE TABLE scripts (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    platform    text NOT NULL DEFAULT 'fortigate' CHECK (platform IN ('fortigate', 'fortiweb')),
    body        text NOT NULL,                 -- Go text/template
    read_only   boolean NOT NULL,              -- only get/show/diagnose lines
    updated_by  text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX scripts_name_key ON scripts (lower(name));

-- Template variables: global (scope_id 0), per group or per device.
CREATE TABLE variables (
    id       bigserial PRIMARY KEY,
    scope    text NOT NULL CHECK (scope IN ('global', 'group', 'device')),
    scope_id bigint NOT NULL DEFAULT 0,
    name     text NOT NULL,
    value    text NOT NULL,
    UNIQUE (scope, scope_id, name)
);

-- Output of read-only runs, which skip the change workflow.
CREATE TABLE script_runs (
    id        bigserial PRIMARY KEY,
    script_id bigint REFERENCES scripts (id) ON DELETE SET NULL,
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    at        timestamptz NOT NULL DEFAULT now(),
    actor     text NOT NULL,
    script    text NOT NULL,                   -- rendered text that ran
    output    text NOT NULL,
    ok        boolean NOT NULL,
    error     text NOT NULL DEFAULT ''
);
CREATE INDEX script_runs_idx ON script_runs (script_id, at DESC);

-- +goose Down
DROP TABLE script_runs;
DROP TABLE variables;
DROP TABLE scripts;
