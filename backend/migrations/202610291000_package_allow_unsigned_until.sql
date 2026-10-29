-- +goose Up
-- allow_unsigned_until bounds the unsigned-package opt-in; NULL means until it is turned off.
ALTER TABLE hatamoto_package_config ADD COLUMN IF NOT EXISTS allow_unsigned_until TIMESTAMPTZ;

-- +goose Down
ALTER TABLE hatamoto_package_config DROP COLUMN IF EXISTS allow_unsigned_until;
