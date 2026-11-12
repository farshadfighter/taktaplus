-- +goose Up
-- API tokens for integrations: only the SHA-256 of the secret is stored.
CREATE TABLE api_tokens (
    id           bigserial PRIMARY KEY,
    token_id     text NOT NULL UNIQUE,          -- public part of tpk_<token_id>_<secret>
    secret_hash  bytea NOT NULL,
    name         text NOT NULL,
    role         text NOT NULL,
    read_only    boolean NOT NULL DEFAULT false,
    allowed_nets text[] NOT NULL DEFAULT '{}',
    expires_at   timestamptz,
    created_by   bigint NOT NULL REFERENCES users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    last_ip      text NOT NULL DEFAULT '',
    revoked_at   timestamptz
);

-- +goose Down
DROP TABLE api_tokens;
