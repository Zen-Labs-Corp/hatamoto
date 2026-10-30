-- +goose Up
-- Record of locale conversions: one row per applied conversion plus one row per
-- entity the conversion changed, holding the before and after state so a
-- conversion can be reverted.
--
-- Two new tables only. The previous release's binary never reads or writes
-- them, so rolling back to it loses nothing it showed before. Down drops both
-- tables and with them the whole conversion history; roll back by running the
-- previous binary on the migrated database, not by running Down.
--
-- The entity id in the row table deliberately has no foreign key: a revert has
-- to report entities that were deleted since the conversion.
CREATE TABLE hatamoto_locale_conversions (
    id              bigserial PRIMARY KEY,
    applied_at      timestamptz NOT NULL DEFAULT now(),
    applied_by      text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'applied' CHECK (status IN ('applied', 'reverted')),
    reverted_at     timestamptz,
    reverted_by     text,
    plan            jsonb NOT NULL,
    report          jsonb NOT NULL,
    settings_before jsonb NOT NULL,
    settings_after  jsonb NOT NULL,
    schemas_before  jsonb NOT NULL DEFAULT '[]',
    schemas_after   jsonb NOT NULL DEFAULT '[]'
);

CREATE TABLE hatamoto_locale_conversion_rows (
    conversion_id    bigint NOT NULL REFERENCES hatamoto_locale_conversions (id) ON DELETE CASCADE,
    entity_id        uuid NOT NULL,
    before_locale    text NOT NULL,
    before_slug      text NOT NULL,
    before_parent_id uuid,
    before_group     uuid,
    before_attrs     jsonb,
    after_locale     text NOT NULL,
    after_slug       text NOT NULL,
    after_parent_id  uuid,
    after_group      uuid,
    after_updated_at timestamptz NOT NULL,
    PRIMARY KEY (conversion_id, entity_id)
);

-- +goose Down
DROP TABLE hatamoto_locale_conversion_rows;
DROP TABLE hatamoto_locale_conversions;
