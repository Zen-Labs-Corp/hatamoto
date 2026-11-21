-- +goose Up
-- Audit log page: retention (owner setting) and indexes for filtering agent calls.
-- Expand-only: the previous binary ignores the columns and indexes.
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS audit_retention_days int NOT NULL DEFAULT 365;
ALTER TABLE hatamoto_site_config ADD COLUMN IF NOT EXISTS agent_audit_retention_days int NOT NULL DEFAULT 90;
CREATE INDEX IF NOT EXISTS idx_hatamoto_mcp_audit_tool_at ON hatamoto_mcp_audit (tool, at DESC);
CREATE INDEX IF NOT EXISTS idx_hatamoto_mcp_audit_key_at ON hatamoto_mcp_audit (api_key_id, at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_hatamoto_mcp_audit_key_at;
DROP INDEX IF EXISTS idx_hatamoto_mcp_audit_tool_at;
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS agent_audit_retention_days;
ALTER TABLE hatamoto_site_config DROP COLUMN IF EXISTS audit_retention_days;
