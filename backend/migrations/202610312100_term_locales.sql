-- A term's locale and translation link, for the admin only. The locale is ''
-- (every locale) for all existing terms and the translation group is NULL
-- (not linked). Public readers never select either column.
--
-- Expand only: two columns with defaults and two partial indexes. The
-- (kind, slug) unique constraint is untouched. The previous release's binary
-- never reads or writes the columns, so rolling back to it loses nothing it
-- showed before.
--
-- hatamoto:contract unused-since v1.0

-- +goose Up
ALTER TABLE hatamoto_terms ADD COLUMN IF NOT EXISTS locale text NOT NULL DEFAULT '';
ALTER TABLE hatamoto_terms ADD COLUMN IF NOT EXISTS translation_group uuid;
CREATE UNIQUE INDEX IF NOT EXISTS uq_terms_group_locale
    ON hatamoto_terms (translation_group, locale)
    WHERE translation_group IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_terms_translation_group
    ON hatamoto_terms (translation_group)
    WHERE translation_group IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_terms_translation_group;
DROP INDEX IF EXISTS uq_terms_group_locale;
ALTER TABLE hatamoto_terms DROP COLUMN IF EXISTS translation_group;
ALTER TABLE hatamoto_terms DROP COLUMN IF EXISTS locale;
