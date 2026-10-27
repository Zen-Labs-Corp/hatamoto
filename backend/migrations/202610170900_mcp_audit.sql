-- +goose Up
-- One row per MCP tool call (read or write): which API key called what, with
-- which (size-capped) arguments, and whether it succeeded.
CREATE TABLE IF NOT EXISTS hatamoto_mcp_audit (
    id           bigserial PRIMARY KEY,
    at           timestamptz NOT NULL DEFAULT now(),
    api_key_id   uuid,
    api_key_name text NOT NULL DEFAULT '',
    tool         text NOT NULL,
    ok           boolean NOT NULL,
    error        text NOT NULL DEFAULT '',
    args         jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_hatamoto_mcp_audit_at ON hatamoto_mcp_audit (at DESC);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_mcp_audit;
