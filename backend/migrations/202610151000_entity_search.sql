-- +goose Up
-- Full-text search for the public /search page: a generated tsvector over the
-- entity name (weight A), attributes.excerpt (B) and attributes.content (C),
-- with a GIN index. The 'simple' configuration is language-neutral: no
-- stemming, no stop words, so it needs no per-site language setting.
-- Every input is cut with left(): to_tsvector raises "string is too long for
-- tsvector" past ~1 MB of distinct lexemes, which would fail the writes (and
-- this migration's table rewrite) on large imports. 200k characters of content
-- is far more than a search needs.
ALTER TABLE hatamoto_entities
    ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', left(coalesce(name, ''), 1000)), 'A') ||
        setweight(to_tsvector('simple', left(coalesce(attributes->>'excerpt', ''), 5000)), 'B') ||
        setweight(to_tsvector('simple', left(coalesce(attributes->>'content', ''), 200000)), 'C')
    ) STORED;

CREATE INDEX idx_hatamoto_entities_search ON hatamoto_entities USING gin (search_vector);

-- +goose Down
DROP INDEX IF EXISTS idx_hatamoto_entities_search;
ALTER TABLE hatamoto_entities DROP COLUMN IF EXISTS search_vector;
