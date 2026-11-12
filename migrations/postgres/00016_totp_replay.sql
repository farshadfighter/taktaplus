-- +goose Up
-- The last TOTP time step a user signed in with: a code is accepted once.
ALTER TABLE users ADD COLUMN totp_last_step bigint;

-- +goose Down
ALTER TABLE users DROP COLUMN totp_last_step;
