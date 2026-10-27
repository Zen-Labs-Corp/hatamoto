--
-- hatamoto:contract unused-since v0.32
-- Only widens chk_sessions_kind. The previous binary writes only 'admin',
-- which the new CHECK still allows, and it rejects 'api' sessions, so it
-- fails closed.

-- +goose Up
-- JSON API cookie sessions get their own kind so the admin form cookie can
-- never authenticate /api/v1 and an API cookie never opens the admin UI.
ALTER TABLE public.hatamoto_sessions DROP CONSTRAINT IF EXISTS chk_sessions_kind;
ALTER TABLE public.hatamoto_sessions ADD CONSTRAINT chk_sessions_kind CHECK (kind IN ('admin','api'));
-- +goose Down
DELETE FROM public.hatamoto_sessions WHERE kind = 'api';
ALTER TABLE public.hatamoto_sessions DROP CONSTRAINT IF EXISTS chk_sessions_kind;
ALTER TABLE public.hatamoto_sessions ADD CONSTRAINT chk_sessions_kind CHECK (kind = 'admin');
