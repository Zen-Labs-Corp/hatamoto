-- The baseline used to name four hatamoto_leads indexes idx_<segment>_leads_*,
-- with a private name as the segment. 001_baseline.sql now creates them as
-- idx_leads_*; this renames the old ones on databases migrated before that.
-- Any index on hatamoto_leads in schema public that matches the old shape is
-- renamed by dropping the middle segment, unless the neutral name is taken.
-- A fresh install has only neutral names, so nothing matches and it is a no-op;
-- a second run finds nothing left to rename.
--
-- hatamoto:contract unused-since v0.32
-- The linter files RENAME under contract. No release refers to an index by
-- name (nothing hints, drops or ON CONFLICTs on them), so the previous binary
-- keeps working on the renamed indexes.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    neutral text;
BEGIN
    FOR r IN
        SELECT indexname
          FROM pg_indexes
         WHERE schemaname = 'public'
           AND tablename = 'hatamoto_leads'
           AND indexname ~ '^idx_.+_leads_(intent_score|intent_tier|merged_into|sales_engaged_at)$'
    LOOP
        neutral := regexp_replace(r.indexname, '^idx_.+_leads_', 'idx_leads_');
        IF to_regclass(format('public.%I', neutral)) IS NULL THEN
            EXECUTE format('ALTER INDEX public.%I RENAME TO %I', r.indexname, neutral);
        END IF;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: the old names carried a private segment and are not restored.
SELECT 1;
