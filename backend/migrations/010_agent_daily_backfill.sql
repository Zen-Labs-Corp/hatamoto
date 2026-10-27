-- +goose Up
-- Copy the legacy bot-visit aggregates into the agent rollup. The old table is
-- left untouched (read-compat for /bot-visits*) and is no longer written.
-- Legacy paths were the internal /api/v1/public/... form; normalise them.
-- Legacy rows were never verified, so they count as unverifiable. Idempotent.
-- Legacy bot_name values (the old bottrack knownBots list) are mapped
-- explicitly to (registry agent id, real operator, registry purpose); ids must
-- exist in internal/agents/registry.json (enforced by a Go test). Notes:
--   * 'Google-Extended (Gemini)' is a robots.txt token, never a real UA; the old
--     detector still produced rows for it, so they are kept under the registry's
--     google-extended agent rather than dropped.
--   * MajesticBot -> mj12bot.
-- Names not in the map fall back to a slug of bot_name, bot_name as operator and
-- a purpose derived from the legacy category.
WITH legacy_map (bot_name, agent_id, operator, purpose) AS (
    VALUES
        ('Googlebot', 'googlebot', 'Google', 'search'),
        ('Bingbot', 'bingbot', 'Microsoft', 'search'),
        ('DuckDuckBot', 'duckduckbot', 'DuckDuckGo', 'search'),
        ('YandexBot', 'yandexbot', 'Yandex', 'search'),
        ('Baiduspider', 'baiduspider', 'Baidu', 'search'),
        ('Applebot', 'applebot', 'Apple', 'search'),
        ('Sogou', 'sogou', 'Sogou', 'search'),
        ('GPTBot', 'gptbot', 'OpenAI', 'training'),
        ('ChatGPT-User', 'chatgpt-user', 'OpenAI', 'user_fetch'),
        ('OAI-SearchBot', 'oai-searchbot', 'OpenAI', 'search_index'),
        ('ClaudeBot', 'claudebot', 'Anthropic', 'training'),
        ('PerplexityBot', 'perplexitybot', 'Perplexity', 'search_index'),
        ('Google-Extended (Gemini)', 'google-extended', 'Google', 'training'),
        ('AmazonBot', 'amazonbot', 'Amazon', 'training'),
        ('FacebookBot', 'facebookbot', 'Meta', 'social'),
        ('Twitterbot', 'twitterbot', 'X', 'social'),
        ('LinkedInBot', 'linkedinbot', 'LinkedIn', 'social'),
        ('Pinterestbot', 'pinterestbot', 'Pinterest', 'social'),
        ('AhrefsBot', 'ahrefsbot', 'Ahrefs', 'seo'),
        ('SemrushBot', 'semrushbot', 'Semrush', 'seo'),
        ('Screaming Frog', 'screaming-frog', 'Screaming Frog', 'seo'),
        ('MajesticBot', 'mj12bot', 'Majestic', 'seo'),
        ('DotBot', 'dotbot', 'Moz', 'seo'),
        ('Internet Archive', 'internet-archive', 'Internet Archive', 'archive')
),
src AS (
    SELECT
        v.visit_date,
        v.path,
        v.hit_count,
        COALESCE(m.agent_id, trim(both '-' from lower(regexp_replace(v.bot_name, '[^A-Za-z0-9]+', '-', 'g')))) AS agent_id,
        COALESCE(m.operator, v.bot_name) AS operator,
        COALESCE(m.purpose, CASE v.bot_category
            WHEN 'search' THEN 'search'
            WHEN 'social' THEN 'social'
            WHEN 'seo' THEN 'seo'
            WHEN 'archive' THEN 'archive'
            ELSE 'other_ai'
        END) AS purpose
    FROM public.hatamoto_bot_visits v
    LEFT JOIN legacy_map m ON m.bot_name = v.bot_name
)
INSERT INTO public.hatamoto_agent_daily
    (day, agent_id, path, status, operator, purpose, entity_id, hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total)
SELECT
    visit_date,
    agent_id,
    regexp_replace(path, '^/api/v1/public', ''),
    200,
    min(operator),
    min(purpose),
    NULL,
    sum(hit_count),
    0, 0,
    sum(hit_count),
    0
FROM src
GROUP BY visit_date, agent_id, regexp_replace(path, '^/api/v1/public', '')
ON CONFLICT DO NOTHING;

-- +goose Down
-- Best-effort: the backfill is not tracked per row, so nothing is removed.
SELECT 1;
