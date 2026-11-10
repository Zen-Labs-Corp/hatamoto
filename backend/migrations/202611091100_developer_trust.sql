-- +goose Up
-- allow_developer is the owner's switch for installing developer packages; off on a fresh install.
ALTER TABLE hatamoto_package_config ADD COLUMN IF NOT EXISTS allow_developer boolean NOT NULL DEFAULT false;
-- developer marks a package installed from the developer index; developer_key is the base64 public key
-- that signed it, kept so the row can be verified at boot without the index.
ALTER TABLE hatamoto_packages ADD COLUMN IF NOT EXISTS developer boolean NOT NULL DEFAULT false;
ALTER TABLE hatamoto_packages ADD COLUMN IF NOT EXISTS developer_key text;
-- disabled_reason is why a package was switched off by a revocation; empty when it was not.
ALTER TABLE hatamoto_packages ADD COLUMN IF NOT EXISTS disabled_reason text;

-- +goose Down
ALTER TABLE hatamoto_packages DROP COLUMN IF EXISTS disabled_reason;
ALTER TABLE hatamoto_packages DROP COLUMN IF EXISTS developer_key;
ALTER TABLE hatamoto_packages DROP COLUMN IF EXISTS developer;
ALTER TABLE hatamoto_package_config DROP COLUMN IF EXISTS allow_developer;
