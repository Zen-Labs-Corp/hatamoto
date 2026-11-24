-- Comments: a dead status (kept forever), a plugin-written rank, show-dead flags.
-- hatamoto:contract unused-since v1.4
-- Widens hatamoto_comments_status_check with 'dead' and swaps the approved-only roots
-- index for two indexes over approved and dead roots. Rollback floor: the previous
-- release never writes 'dead' or rank; it reads status = 'approved' only, so it hides
-- dead rows and their replies, and refuses admin actions on them (bad transition).

-- +goose Up
ALTER TABLE public.hatamoto_comments ADD COLUMN IF NOT EXISTS rank double precision NOT NULL DEFAULT 0;
ALTER TABLE public.hatamoto_comments ADD COLUMN IF NOT EXISTS dead_by text;
ALTER TABLE public.hatamoto_comments DROP CONSTRAINT IF EXISTS hatamoto_comments_status_check;
ALTER TABLE public.hatamoto_comments ADD CONSTRAINT hatamoto_comments_status_check
    CHECK (status IN ('draft','pending','approved','spam','trash','dead'));
CREATE INDEX IF NOT EXISTS hatamoto_comments_roots_live_idx ON public.hatamoto_comments (entity_id, created_at DESC, seq DESC)
    WHERE parent_id IS NULL AND status IN ('approved','dead');
CREATE INDEX IF NOT EXISTS hatamoto_comments_roots_rank_idx ON public.hatamoto_comments (entity_id, rank DESC, created_at DESC, seq DESC)
    WHERE parent_id IS NULL AND status IN ('approved','dead');
DROP INDEX IF EXISTS public.hatamoto_comments_roots_idx;
CREATE TABLE IF NOT EXISTS public.hatamoto_comment_viewer_flags (
    plugin text NOT NULL,
    commenter_key text NOT NULL,
    show_dead boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin, commenter_key)
);

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_comment_viewer_flags;
CREATE INDEX IF NOT EXISTS hatamoto_comments_roots_idx ON public.hatamoto_comments (entity_id, created_at DESC, seq DESC) WHERE parent_id IS NULL AND status = 'approved';
DROP INDEX IF EXISTS public.hatamoto_comments_roots_rank_idx;
DROP INDEX IF EXISTS public.hatamoto_comments_roots_live_idx;
UPDATE public.hatamoto_comments SET status = 'trash', updated_at = now() WHERE status = 'dead';
ALTER TABLE public.hatamoto_comments DROP CONSTRAINT IF EXISTS hatamoto_comments_status_check;
ALTER TABLE public.hatamoto_comments ADD CONSTRAINT hatamoto_comments_status_check
    CHECK (status IN ('draft','pending','approved','spam','trash'));
ALTER TABLE public.hatamoto_comments DROP COLUMN IF EXISTS dead_by;
ALTER TABLE public.hatamoto_comments DROP COLUMN IF EXISTS rank;
