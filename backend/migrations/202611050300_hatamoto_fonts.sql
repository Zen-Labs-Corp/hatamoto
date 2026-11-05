-- +goose Up
-- Fonts the owner uploaded or downloaded once from Google Fonts. The bytes live
-- in the row (at most 2 MB each, 60 rows) so export, import and the database
-- backup carry them. Expand-only; validation lives in Go.
CREATE TABLE IF NOT EXISTS hatamoto_fonts (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    family        text        NOT NULL,
    weight        integer     NOT NULL CHECK (weight BETWEEN 100 AND 900),
    style         text        NOT NULL CHECK (style IN ('normal', 'italic')),
    source        text        NOT NULL CHECK (source IN ('upload', 'google')),
    subset        text        NOT NULL DEFAULT '',
    unicode_range text        NOT NULL DEFAULT '',
    hash          char(64)    NOT NULL,
    data          bytea       NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hatamoto_fonts_family_idx ON hatamoto_fonts (lower(family));
CREATE INDEX IF NOT EXISTS hatamoto_fonts_hash_idx ON hatamoto_fonts (hash);

-- +goose Down
DROP TABLE IF EXISTS hatamoto_fonts;
