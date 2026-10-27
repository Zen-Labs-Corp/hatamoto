-- +goose Up
-- +goose StatementBegin
-- The pgvector extension was installed on early databases but no table uses it
-- and no Go code references it. Drop it only when that is still true: if a
-- column of type vector exists (an operator added one), keep the extension.
-- Dropping needs extension ownership; when the migration role lacks it we keep
-- the extension and say so rather than failing the whole migration run.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        IF EXISTS (
            SELECT 1
            FROM pg_attribute a
            JOIN pg_type t ON t.oid = a.atttypid
            JOIN pg_depend d ON d.objid = t.oid AND d.deptype = 'e'
            JOIN pg_extension e ON e.oid = d.refobjid AND e.extname = 'vector'
            WHERE a.attnum > 0 AND NOT a.attisdropped
        ) THEN
            RAISE NOTICE 'pgvector is in use by a column; keeping the extension';
        ELSE
            BEGIN
                DROP EXTENSION vector;
            EXCEPTION WHEN insufficient_privilege THEN
                RAISE NOTICE 'pgvector is unused but not owned by this role; drop it manually: DROP EXTENSION vector;';
            END;
        END IF;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Intentionally empty: the extension was unused, so there is nothing to restore.
SELECT 1;
