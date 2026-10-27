-- +goose Up
-- Lets the default theme find a media row by the path part of its public URL
-- (srcset lookups) with an index range scan instead of a table scan:
-- reverse(public_url) LIKE reverse(path) || '%'.
CREATE INDEX idx_hatamoto_media_public_url_rev ON hatamoto_media (reverse(public_url) text_pattern_ops);

-- +goose Down
DROP INDEX IF EXISTS idx_hatamoto_media_public_url_rev;
