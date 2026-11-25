-- Site search for scripts written without spaces (Japanese, Chinese, Korean).
--
-- The 'simple' parser makes one lexeme of each run of these scripts between
-- punctuation marks, so a word inside a run never matched (#412). The search
-- vector now passes its text through hatamoto_search_text, which rewrites every
-- such run as overlapping bigrams: the token at each character is that
-- character and the next one in the run, and the run's last character stands
-- alone ("上映会" -> "上映 映会 会"). Queries are built the same way
-- (entity/search_cjk.go). Text without such characters is returned unchanged,
-- so every Latin-script vector, and its ranking, stays byte for byte what it
-- was. No extension is needed.
--
-- The character class is generated from entity.segmentlessRanges and a test
-- keeps them equal. In a text with such characters, HTML tags are removed
-- first (content only; tags split at a run would otherwise index their
-- attribute names) and the text is cut to 100000 characters, so the distinct
-- bigrams stay well under the 1 MB tsvector limit.
--
-- The function body is BEGIN ATOMIC: its references are bound when it is
-- created, so a restore with an empty search_path can compute the column.
--
-- hatamoto:contract unused-since v1.4
-- search_vector is dropped and added again, with the same name, type and index,
-- in this one transaction. The previous release's binary only reads it with
-- websearch_to_tsquery('simple', ...), which keeps matching every Latin-script
-- entry exactly as before; it just does not find words inside such runs.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION public.hatamoto_search_text(t text, html boolean) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
BEGIN ATOMIC
    SELECT CASE
        WHEN t !~ '[\u1100-\u11ff\u2e80-\u2fdf\u3005-\u3007\u3021-\u3029\u3031-\u3035\u3041-\u3096\u309d-\u309f\u30a1-\u30fa\u30fc-\u30ff\u3105-\u312f\u3131-\u318e\u31a0-\u31bf\u31f0-\u31ff\u3400-\u4dbf\u4e00-\u9fff\ua960-\ua97f\uac00-\ud7af\ud7b0-\ud7ff\uf900-\ufaff\uff66-\uff9f\uffa0-\uffdc\U00020000-\U0003134f]' THEN t
        ELSE (
            SELECT coalesce(string_agg(
                CASE
                    WHEN m.part[1] ~ '^[\u1100-\u11ff\u2e80-\u2fdf\u3005-\u3007\u3021-\u3029\u3031-\u3035\u3041-\u3096\u309d-\u309f\u30a1-\u30fa\u30fc-\u30ff\u3105-\u312f\u3131-\u318e\u31a0-\u31bf\u31f0-\u31ff\u3400-\u4dbf\u4e00-\u9fff\ua960-\ua97f\uac00-\ud7af\ud7b0-\ud7ff\uf900-\ufaff\uff66-\uff9f\uffa0-\uffdc\U00020000-\U0003134f]' THEN (
                        SELECT string_agg(substr(m.part[1], i, 2), ' ' ORDER BY i)
                        FROM generate_series(1, char_length(m.part[1])) AS i
                    )
                    ELSE m.part[1]
                END,
                ' ' ORDER BY m.n), '')
            FROM regexp_matches(
                left(CASE WHEN html THEN regexp_replace(t, '<[^>]*>', ' ', 'g') ELSE t END, 100000),
                '[\u1100-\u11ff\u2e80-\u2fdf\u3005-\u3007\u3021-\u3029\u3031-\u3035\u3041-\u3096\u309d-\u309f\u30a1-\u30fa\u30fc-\u30ff\u3105-\u312f\u3131-\u318e\u31a0-\u31bf\u31f0-\u31ff\u3400-\u4dbf\u4e00-\u9fff\ua960-\ua97f\uac00-\ud7af\ud7b0-\ud7ff\uf900-\ufaff\uff66-\uff9f\uffa0-\uffdc\U00020000-\U0003134f]+|[^\u1100-\u11ff\u2e80-\u2fdf\u3005-\u3007\u3021-\u3029\u3031-\u3035\u3041-\u3096\u309d-\u309f\u30a1-\u30fa\u30fc-\u30ff\u3105-\u312f\u3131-\u318e\u31a0-\u31bf\u31f0-\u31ff\u3400-\u4dbf\u4e00-\u9fff\ua960-\ua97f\uac00-\ud7af\ud7b0-\ud7ff\uf900-\ufaff\uff66-\uff9f\uffa0-\uffdc\U00020000-\U0003134f]+',
                'g') WITH ORDINALITY AS m(part, n)
        )
    END;
END;
-- +goose StatementEnd

ALTER TABLE hatamoto_entities DROP COLUMN search_vector;

ALTER TABLE hatamoto_entities
    ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', public.hatamoto_search_text(left(coalesce(name, ''), 1000), false)), 'A') ||
        setweight(to_tsvector('simple', public.hatamoto_search_text(left(coalesce(attributes->>'excerpt', ''), 5000), false)), 'B') ||
        setweight(to_tsvector('simple', public.hatamoto_search_text(left(coalesce(attributes->>'content', ''), 200000), true)), 'C')
    ) STORED;

CREATE INDEX idx_hatamoto_entities_search ON hatamoto_entities USING gin (search_vector);

-- +goose Down
ALTER TABLE hatamoto_entities DROP COLUMN IF EXISTS search_vector;
ALTER TABLE hatamoto_entities
    ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', left(coalesce(name, ''), 1000)), 'A') ||
        setweight(to_tsvector('simple', left(coalesce(attributes->>'excerpt', ''), 5000)), 'B') ||
        setweight(to_tsvector('simple', left(coalesce(attributes->>'content', ''), 200000)), 'C')
    ) STORED;
CREATE INDEX idx_hatamoto_entities_search ON hatamoto_entities USING gin (search_vector);
DROP FUNCTION IF EXISTS public.hatamoto_search_text(text, boolean);
