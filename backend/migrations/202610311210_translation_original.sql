-- The flagged original of a translation group. At most one member of a group
-- carries the flag; a group without a flagged member falls back to its
-- default-locale member, then to its oldest member, in the readers.
--
-- Expand only: one column with a default and one partial unique index. The
-- previous release's binary never reads or writes the column, so rolling back
-- to it loses nothing it showed before.
--
-- hatamoto:contract unused-since v1.0

-- +goose Up
ALTER TABLE hatamoto_entities ADD COLUMN IF NOT EXISTS translation_original boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS uq_entities_translation_original
    ON hatamoto_entities (translation_group)
    WHERE translation_original AND translation_group IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS uq_entities_translation_original;
ALTER TABLE hatamoto_entities DROP COLUMN IF EXISTS translation_original;
