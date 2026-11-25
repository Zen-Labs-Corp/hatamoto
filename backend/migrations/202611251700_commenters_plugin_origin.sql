-- Comments: a commenter a plugin vouches for (comments.identity) has no email.
-- origin_plugin and origin_account name the plugin and its own account id; such a
-- row stores email_hash 'plugin:<uuid>' (never an HMAC: those are base64url, no ':')
-- and email_sealed '' (the value OpenEmail already reads as no email). An email
-- commenter a plugin links keeps its email and gains the two columns. Two nullable
-- columns, a check and a unique index the previous release never reads or writes: both
-- hold for rows with NULL origin, so the marker below is the documented way to say so
-- (the floor only stops at v1.4; no major release is implied).
-- Rollback floor: unchanged (an older binary treats such a commenter as one whose
-- email cannot be opened: it is asked to sign in, and its idle purge may remove one
-- that never posted).

-- hatamoto:contract unused-since v1.4

-- +goose Up
ALTER TABLE public.hatamoto_commenters ADD COLUMN IF NOT EXISTS origin_plugin text;
ALTER TABLE public.hatamoto_commenters ADD COLUMN IF NOT EXISTS origin_account text;
ALTER TABLE public.hatamoto_commenters DROP CONSTRAINT IF EXISTS hatamoto_commenters_origin_check;
ALTER TABLE public.hatamoto_commenters ADD CONSTRAINT hatamoto_commenters_origin_check
    CHECK ((origin_plugin IS NULL) = (origin_account IS NULL));
CREATE UNIQUE INDEX IF NOT EXISTS hatamoto_commenters_origin_key
    ON public.hatamoto_commenters (origin_plugin, origin_account);

-- +goose Down
DROP INDEX IF EXISTS public.hatamoto_commenters_origin_key;
ALTER TABLE public.hatamoto_commenters DROP CONSTRAINT IF EXISTS hatamoto_commenters_origin_check;
ALTER TABLE public.hatamoto_commenters DROP COLUMN IF EXISTS origin_account;
ALTER TABLE public.hatamoto_commenters DROP COLUMN IF EXISTS origin_plugin;
