-- +goose Up
-- Static export over SFTP: the connection fields of the SFTP target. The credential (password or private key,
-- named by sftp_auth) is sealed with its own AAD, never in clear; sftp_host_key is the SHA256 fingerprint the
-- owner trusted. Expand only: the previous release never reads these columns.
ALTER TABLE hatamoto_static_hosting_settings
    ADD COLUMN IF NOT EXISTS sftp_host          text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sftp_port          integer NOT NULL DEFAULT 22,
    ADD COLUMN IF NOT EXISTS sftp_user          text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sftp_auth          text    NOT NULL DEFAULT 'password',
    ADD COLUMN IF NOT EXISTS sftp_secret_sealed bytea,
    ADD COLUMN IF NOT EXISTS sftp_dir           text    NOT NULL DEFAULT 'public_html',
    ADD COLUMN IF NOT EXISTS sftp_host_key      text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sftp_htaccess      boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE hatamoto_static_hosting_settings
    DROP COLUMN IF EXISTS sftp_host,
    DROP COLUMN IF EXISTS sftp_port,
    DROP COLUMN IF EXISTS sftp_user,
    DROP COLUMN IF EXISTS sftp_auth,
    DROP COLUMN IF EXISTS sftp_secret_sealed,
    DROP COLUMN IF EXISTS sftp_dir,
    DROP COLUMN IF EXISTS sftp_host_key,
    DROP COLUMN IF EXISTS sftp_htaccess;
