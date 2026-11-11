-- +goose Up
-- F4: shared object database, Policy Packages and installs (FortiGate, IPv4).

-- Objects: address, addrgrp, service, servicegrp, schedule, vip, ippool.
-- value holds the type's fields (see internal/policy).
CREATE TABLE policy_objects (
    id              bigserial PRIMARY KEY,
    type            text NOT NULL CHECK (type IN ('address', 'addrgrp', 'service', 'servicegrp', 'schedule', 'vip', 'ippool')),
    name            text NOT NULL,
    value           jsonb NOT NULL,
    per_device      boolean NOT NULL DEFAULT false, -- every device needs its own mapping
    comment         text NOT NULL DEFAULT '',
    updated_by      text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX policy_objects_name_key ON policy_objects (type, lower(name));

-- Dynamic mapping: the object's value on one device.
CREATE TABLE policy_object_mappings (
    object_id bigint NOT NULL REFERENCES policy_objects (id) ON DELETE CASCADE,
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    value     jsonb NOT NULL,
    PRIMARY KEY (object_id, device_id)
);

-- Interface mapping: logical zone → interface of a device.
CREATE TABLE interface_mappings (
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    zone      text NOT NULL,
    interface text NOT NULL,
    PRIMARY KEY (device_id, zone)
);

CREATE TABLE policy_packages (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    device_ids  bigint[] NOT NULL DEFAULT '{}',
    vdom        text NOT NULL DEFAULT 'root',
    version     integer NOT NULL DEFAULT 0,  -- latest published version
    draft       jsonb,                       -- unpublished policies, or NULL
    draft_base  integer,                     -- version the draft started from
    locked_by   text,
    locked_at   timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX policy_packages_name_key ON policy_packages (lower(name));

CREATE TABLE policy_package_versions (
    package_id bigint NOT NULL REFERENCES policy_packages (id) ON DELETE CASCADE,
    version    integer NOT NULL,
    policies   jsonb NOT NULL,
    note       text NOT NULL DEFAULT '',
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (package_id, version)
);

-- What was installed on each device, and local drift found since.
CREATE TABLE policy_installs (
    package_id   bigint NOT NULL REFERENCES policy_packages (id) ON DELETE CASCADE,
    device_id    bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    version      integer NOT NULL,
    installed_at timestamptz NOT NULL,
    objects      jsonb NOT NULL DEFAULT '[]', -- managed objects ("type:name") on the device
    drift        jsonb NOT NULL DEFAULT '[]', -- operations that would restore the package
    checked_at   timestamptz,
    PRIMARY KEY (package_id, device_id)
);

-- Objects found on import with a value other than the shared one.
CREATE TABLE policy_conflicts (
    id           bigserial PRIMARY KEY,
    object_id    bigint NOT NULL REFERENCES policy_objects (id) ON DELETE CASCADE,
    device_id    bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    device_value jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (object_id, device_id)
);

-- +goose Down
DROP TABLE policy_conflicts;
DROP TABLE policy_installs;
DROP TABLE policy_package_versions;
DROP TABLE policy_packages;
DROP TABLE interface_mappings;
DROP TABLE policy_object_mappings;
DROP TABLE policy_objects;
