-- Built-in roles: author, contributor, reviewer and read-only join owner and editor.
--
-- Expand only: both role checks are widened. The previous release's binary
-- writes only 'owner' and 'editor', which the new checks still allow; it reads
-- an unknown role as no access at its role gates.
--
-- hatamoto:contract unused-since v1.0

-- +goose Up
ALTER TABLE public.hatamoto_users DROP CONSTRAINT IF EXISTS chk_users_role;
ALTER TABLE public.hatamoto_users ADD CONSTRAINT chk_users_role
    CHECK (role IN ('owner', 'editor', 'author', 'contributor', 'reviewer', 'read-only'));
ALTER TABLE public.hatamoto_user_tokens DROP CONSTRAINT IF EXISTS hatamoto_user_tokens_role_check;
ALTER TABLE public.hatamoto_user_tokens DROP CONSTRAINT IF EXISTS chk_user_tokens_role;
ALTER TABLE public.hatamoto_user_tokens ADD CONSTRAINT chk_user_tokens_role
    CHECK (role IN ('owner', 'editor', 'author', 'contributor', 'reviewer', 'read-only'));

-- +goose Down
-- Rows with the new roles are kept. The old checks are NOT VALID, so they are not
-- scanned now, but Postgres still enforces them whenever such a row is updated:
-- until a user with a new role is moved to owner or editor, updating that row fails.
ALTER TABLE public.hatamoto_user_tokens DROP CONSTRAINT IF EXISTS chk_user_tokens_role;
ALTER TABLE public.hatamoto_user_tokens ADD CONSTRAINT hatamoto_user_tokens_role_check CHECK (role IN ('owner', 'editor')) NOT VALID;
ALTER TABLE public.hatamoto_users DROP CONSTRAINT IF EXISTS chk_users_role;
ALTER TABLE public.hatamoto_users ADD CONSTRAINT chk_users_role CHECK (role IN ('owner', 'editor')) NOT VALID;
