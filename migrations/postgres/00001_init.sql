-- +goose Up
-- Phase 0: only bookkeeping. Feature tables arrive with their modules.
CREATE TABLE app_meta (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app_meta;
