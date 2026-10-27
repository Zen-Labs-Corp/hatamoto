-- +goose Up
-- v1-03 subtract, part 2. Drops the storage behind features removed from core:
--   * the lead system: leads, activities, email events, magic and remember
--     tokens, merges, nurture queue, and lead sessions
--   * gated content (entities.gated)
--   * entity columns only a retired site used: content_role, fact_confidence,
--     successor_entity_id (and the series index)
--   * the roles viewer and client: every user holding one becomes an editor,
--     after their API keys are revoked and their sessions deleted. The users
--     table has no disabled/locked column, so the accounts themselves stay
--     (as editors, able to sign in with their password); an owner should review
--     them. Revoking first matters: API keys inherit their creator's role, and
--     dormant keys of a demoted creator must not come alive as editor keys.
--   * hatamoto_bot_visits and hatamoto_secrets (the latter is created by the
--     baseline but nothing reads it)
--
-- Destructive by design: lead data is deleted, not archived. Other data is
-- carried over where its meaning would otherwise change:
--   * gated entities that are live become drafts (the gate is gone, so leaving
--     them published would expose the full body)
--   * successor links are dropped without creating redirects: they were only
--     ever advisory (redirect explain/shadow), never served as a 301
--   * lead.* webhook events are stripped from subscriptions (a subscription
--     left with no events is deactivated)

-- Viewer/client users are about to become editors: kill their API keys and
-- sessions first.
UPDATE public.hatamoto_api_keys
   SET is_active = false, revoked_at = now(), updated_at = now()
 WHERE revoked_at IS NULL
   AND created_by IN (SELECT id FROM public.hatamoto_users WHERE role NOT IN ('owner', 'editor'));
DELETE FROM public.hatamoto_sessions
 WHERE user_id IN (SELECT id FROM public.hatamoto_users WHERE role NOT IN ('owner', 'editor'));

-- Lead sessions go first: they reference hatamoto_leads.
DELETE FROM public.hatamoto_sessions WHERE kind = 'lead' OR user_id IS NULL;
ALTER TABLE public.hatamoto_sessions DROP CONSTRAINT IF EXISTS chk_sessions_user_xor_lead;
ALTER TABLE public.hatamoto_sessions DROP CONSTRAINT IF EXISTS chk_sessions_kind;
ALTER TABLE public.hatamoto_sessions DROP COLUMN IF EXISTS lead_id;
ALTER TABLE public.hatamoto_sessions ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE public.hatamoto_sessions
    ADD CONSTRAINT chk_sessions_kind CHECK (kind = 'admin');

DROP TABLE IF EXISTS public.lead_activities;
DROP TABLE IF EXISTS public.lead_email_events;
DROP TABLE IF EXISTS public.lead_magic_tokens;
DROP TABLE IF EXISTS public.lead_merges;
DROP TABLE IF EXISTS public.lead_nurture_queue;
DROP TABLE IF EXISTS public.lead_remember_tokens;
DROP TABLE IF EXISTS public.hatamoto_leads;

UPDATE public.hatamoto_entities SET status = 'draft', updated_at = now()
 WHERE gated AND status IN ('published', 'scheduled');

-- Successors were never served as redirects (only explain/shadow used them),
-- so no redirect rows are backfilled; the column is simply dropped.

UPDATE public.hatamoto_webhooks w
   SET events = COALESCE((SELECT jsonb_agg(e) FROM jsonb_array_elements(w.events) e
                           WHERE e #>> '{}' NOT LIKE 'lead.%'), '[]'::jsonb),
       is_active = CASE WHEN EXISTS (SELECT 1 FROM jsonb_array_elements(w.events) e
                                      WHERE e #>> '{}' NOT LIKE 'lead.%')
                        THEN w.is_active ELSE false END,
       updated_at = now()
 WHERE EXISTS (SELECT 1 FROM jsonb_array_elements(w.events) e WHERE e #>> '{}' LIKE 'lead.%');

DROP INDEX IF EXISTS public.idx_entities_successor;
DROP INDEX IF EXISTS public.idx_entities_series_slug;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS gated;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS content_role;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS fact_confidence;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS successor_entity_id;

DROP TABLE IF EXISTS public.hatamoto_bot_visits;
DROP TABLE IF EXISTS public.hatamoto_secrets;

UPDATE public.hatamoto_users SET role = 'editor' WHERE role NOT IN ('owner', 'editor');
ALTER TABLE public.hatamoto_users DROP CONSTRAINT IF EXISTS chk_users_role;
ALTER TABLE public.hatamoto_users
    ADD CONSTRAINT chk_users_role CHECK (role IN ('owner', 'editor'));

-- +goose Down
-- Irreversible: lead rows and the dropped entity columns cannot be recovered.
-- Only the structural constraints are put back so the previous schema's
-- migrations can still be rolled back one step at a time.
ALTER TABLE public.hatamoto_users DROP CONSTRAINT IF EXISTS chk_users_role;
ALTER TABLE public.hatamoto_sessions DROP CONSTRAINT IF EXISTS chk_sessions_kind;
ALTER TABLE public.hatamoto_sessions ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE public.hatamoto_sessions
    ADD CONSTRAINT chk_sessions_kind CHECK (kind = ANY (ARRAY['admin'::text, 'lead'::text]));
