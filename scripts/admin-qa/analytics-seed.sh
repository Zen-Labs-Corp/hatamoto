#!/usr/bin/env bash
# Seed 100 days of analytics so every AI analytics screen looks like the prototype: charts, sparks,
# the category bars, the error tracker, the agents table and its purposes, coverage, first crawls
# and the assistants (the default seed has none: every chart is empty).
#   - crawler rollups for registry agents with their real purposes (training, search_index, user_fetch,
#     other_ai, plus a few non-AI crawlers for the response mix), weekly seasonality, a spike 9 days ago,
#     some days with many 4xx/5xx, a few spoofed and unverifiable agents;
#   - hits on the published entities (Zipf by rank), each live a few days after it was published, so
#     coverage, top pages by type, first crawls and time to first fetch fill in. About 15% of the
#     published pages are never fetched. Publish dates of published pages are spread over the range;
#   - AI referrals for chatgpt, perplexity, gemini, claude, copilot, grok, meta_ai and deepseek, and pageviews.
# Idempotent: it replaces what it wrote (agent rollups of the last 120 days, pageview and ai_referral rows,
# first crawls, log-sourced hits) in one transaction. Throwaway QA databases only.
# Opt-in: run-local.sh calls it when QA_SEED_ANALYTICS=1. Needs psql (in PG_BIN or on PATH).
# Usage: QA_USE_DATABASE_URL=1 DATABASE_URL=... scripts/admin-qa/analytics-seed.sh
set -Eeuo pipefail
: "${DATABASE_URL:?set DATABASE_URL to the QA database}"
. "$(dirname "${BASH_SOURCE[0]}")/qa-env.sh"
qa_guard_database_url
PSQL=psql
[ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ] && PSQL=$PG_BIN/psql
"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
BEGIN;
DELETE FROM hatamoto_agent_daily WHERE day >= current_date - 120;
DELETE FROM hatamoto_analytics_daily WHERE kind IN ('pageview', 'ai_referral') AND day >= current_date - 120;
DELETE FROM hatamoto_agent_first_crawl;
DELETE FROM hatamoto_agent_hits WHERE source = 'log' AND occurred_at >= current_date - 120;
UPDATE hatamoto_agent_tracking SET started_at = (current_date - 110)::timestamp AT TIME ZONE 'UTC';

-- The days: weekend dip, slow growth, a spike 9 days ago, and an error multiplier (two bad days, a few mild ones).
CREATE TEMP TABLE sd ON COMMIT DROP AS
SELECT d::date AS day, current_date - d::date AS ago,
       CASE extract(isodow FROM d)::int WHEN 6 THEN 0.62 WHEN 7 THEN 0.55 WHEN 1 THEN 0.95 WHEN 5 THEN 0.92 ELSE 1.08 END AS wk,
       (0.78 + 0.22 * (99 - (current_date - d::date)) / 99.0)
         * CASE current_date - d::date WHEN 9 THEN 2.4 WHEN 8 THEN 1.6 WHEN 7 THEN 1.2 ELSE 1 END AS trend,
       CASE WHEN current_date - d::date IN (3, 17) THEN 9 WHEN (current_date - d::date) % 13 = 5 THEN 2 ELSE 1 END AS bad,
       (abs(hashtext('n' || d::date::text)) % 1000) / 1000.0 AS noise
  FROM generate_series(current_date - 99, current_date, interval '1 day') d;

-- Registry agents: base fetches a day, share of unverifiable, whether spoofers use the name, how often it shows up.
CREATE TEMP TABLE sa (id text, operator text, purpose text, base int, urate numeric, spoof boolean, p numeric) ON COMMIT DROP;
INSERT INTO sa VALUES
  ('gptbot', 'OpenAI', 'training', 230, 0, true, 1), ('claudebot', 'Anthropic', 'training', 150, 0, true, 1),
  ('ccbot', 'Common Crawl', 'training', 70, 1, true, 1), ('meta-externalagent', 'Meta', 'training', 90, 0.1, false, 1),
  ('bytespider', 'ByteDance', 'training', 45, 1, false, 1), ('amazonbot', 'Amazon', 'training', 60, 0.3, true, 1),
  ('cohere-ai', 'Cohere', 'training', 12, 1, false, 0.3), ('diffbot', 'Diffbot', 'training', 14, 1, false, 0.4),
  ('oai-searchbot', 'OpenAI', 'search_index', 110, 0, false, 1), ('claude-searchbot', 'Anthropic', 'search_index', 55, 0, false, 1),
  ('perplexitybot', 'Perplexity', 'search_index', 75, 0, true, 1),
  ('chatgpt-user', 'OpenAI', 'user_fetch', 205, 0, false, 1), ('claude-user', 'Anthropic', 'user_fetch', 80, 0, false, 1),
  ('perplexity-user', 'Perplexity', 'user_fetch', 65, 0, false, 1), ('meta-externalfetcher', 'Meta', 'user_fetch', 32, 0, false, 1),
  ('duckassistbot', 'DuckDuckGo', 'user_fetch', 26, 0, false, 1), ('mistralai-user', 'Mistral', 'user_fetch', 18, 0, false, 0.6),
  ('googleother', 'Google', 'other_ai', 38, 0, false, 1),
  -- not AI: they only add to the response mix
  ('googlebot', 'Google', 'search', 260, 0, false, 1), ('bingbot', 'Microsoft', 'search', 120, 0, false, 1),
  ('ahrefsbot', 'Ahrefs', 'seo', 90, 0.5, false, 1), ('facebookexternalhit', 'Meta', 'social', 40, 0, false, 1);

-- Published pages: the home page first, the other pages next, then posts by a stable hash. Posts go live a few
-- days after they were published (most within a day), the last 15% by rank are never fetched.
CREATE TEMP TABLE ent ON COMMIT DROP AS
SELECT x.*, 1 / power(x.rank, 0.75) AS w FROM (
  SELECT e.id, s.slug AS type,
         CASE WHEN s.slug = 'page' AND e.slug = 'home' THEN '/' WHEN s.slug = 'post' THEN '/blog/' || e.slug ELSE '/' || e.slug END AS path,
         row_number() OVER (ORDER BY (e.slug = 'home') DESC, (s.slug = 'page') DESC, abs(hashtext(e.id::text))) AS rank,
         count(*) OVER () AS n,
         CASE WHEN s.slug = 'page' THEN current_date - 120 ELSE current_date - 15 - abs(hashtext('p' || e.id::text)) % 80 END AS pub_day,
         abs(hashtext('l' || e.id::text)) % 100 AS lagh
    FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
   WHERE e.status = 'published' AND e.variant_key IS NULL) x;
ALTER TABLE ent ADD COLUMN live_day date, ADD COLUMN pub_ts timestamptz, ADD COLUMN wn numeric;
UPDATE ent SET live_day = pub_day + CASE WHEN lagh < 35 THEN 0 WHEN lagh < 65 THEN 1 + lagh % 3 WHEN lagh < 85 THEN 4 + lagh % 4 ELSE 8 + lagh % 7 END,
               pub_ts = (pub_day::timestamp + (9 + abs(hashtext('h' || id::text)) % 7) * interval '1 hour') AT TIME ZONE 'UTC';
UPDATE ent SET wn = CASE WHEN rank <= ceil(0.85 * n) THEN w ELSE 0 END;
UPDATE ent SET wn = wn / (SELECT sum(wn) FROM ent);
UPDATE hatamoto_entities e SET published_at = ent.pub_ts FROM ent WHERE ent.id = e.id AND ent.type = 'post';

-- Paths that are not pages: crawl files, a redirect, and the failures the Health section lists.
CREATE TEMP TABLE sp (path text, status int, w numeric, err boolean) ON COMMIT DROP;
INSERT INTO sp VALUES
  ('/robots.txt', 200, 0.05, false), ('/sitemap.xml', 200, 0.03, false), ('/llms.txt', 200, 0.025, false), ('/old-blog', 301, 0.02, false),
  ('/old-pricing', 404, 0.016, true), ('/blog/feed.xml', 404, 0.011, true), ('/wp-login.php', 404, 0.008, true), ('/docs/v1/setup', 410, 0.007, true),
  ('/.well-known/ai.txt', 404, 0.006, true), ('/products/legacy-kit', 404, 0.005, true), ('/api/search', 403, 0.004, true), ('/tag/ai', 404, 0.003, true),
  ('/search', 500, 0.0015, true), ('/reports/export', 503, 0.0012, true);

-- Crawler rollups: stochastic rounding keeps the long tail of small counts.
INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, entity_id, hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total)
SELECT r.day, r.agent_id, r.path, r.status, r.operator, r.purpose, r.entity_id, r.hits,
       r.hits - r.u - r.sp, r.sp, r.u, r.hits * (9000 + abs(hashtext(r.path)) % 24000)
  FROM (
    SELECT c.*, LEAST(c.hits, round(c.hits * c.urate)::bigint) AS u,
           CASE WHEN c.spoof AND c.status = 200 AND c.path IN ('/robots.txt', '/sitemap.xml') AND abs(hashtext(c.agent_id || c.day::text || 's')) % 100 < 30
                THEN LEAST(c.hits - LEAST(c.hits, round(c.hits * c.urate)::bigint), GREATEST(1, c.hits / 6)) ELSE 0 END AS sp
      FROM (
        SELECT q.*, floor(q.x)::bigint + CASE WHEN (abs(hashtext(q.agent_id || q.path || q.day::text)) % 1000) / 1000.0 < q.x - floor(q.x) THEN 1 ELSE 0 END AS hits
          FROM (
            SELECT b.*, e.path, e.id AS entity_id, 200 AS status,
                   b.h * 0.80 * e.wn * (0.4 + (abs(hashtext(b.agent_id || e.path)) % 120) / 100.0) AS x
              FROM (SELECT d.day, d.bad, a.id AS agent_id, a.operator, a.purpose, a.urate, a.spoof,
                           CASE WHEN abs(hashtext(a.id || 'p' || d.day::text)) % 100 < a.p * 100
                                THEN a.base * d.wk * d.trend * (0.85 + 0.3 * (abs(hashtext(a.id || d.day::text)) % 1000) / 1000.0) ELSE 0 END AS h
                      FROM sd d CROSS JOIN sa a) b
              JOIN ent e ON e.wn > 0 AND b.day >= e.live_day WHERE b.h > 0
            UNION ALL
            SELECT b.*, s.path, NULL::uuid, s.status,
                   b.h * s.w * CASE WHEN s.err THEN 0.45 * b.bad ELSE 1 END * (0.5 + (abs(hashtext(b.agent_id || s.path || b.day::text)) % 100) / 100.0)
              FROM (SELECT d.day, d.bad, a.id AS agent_id, a.operator, a.purpose, a.urate, a.spoof,
                           CASE WHEN abs(hashtext(a.id || 'p' || d.day::text)) % 100 < a.p * 100
                                THEN a.base * d.wk * d.trend * (0.85 + 0.3 * (abs(hashtext(a.id || d.day::text)) % 1000) / 1000.0) ELSE 0 END AS h
                      FROM sd d CROSS JOIN sa a) b
              CROSS JOIN sp s WHERE b.h > 0) q) c) r
 WHERE r.hits > 0
ON CONFLICT DO NOTHING;

-- First crawls: the first AI fetch of each post that went live inside the range; a few have no publish time on record.
INSERT INTO hatamoto_agent_first_crawl (entity_id, agent_id, first_at, published_at)
SELECT f.entity_id, f.agent_id,
       (f.day::timestamp + (extract(hour FROM e.pub_ts AT TIME ZONE 'UTC') + 1 + abs(hashtext('f' || f.entity_id::text)) % 6) * interval '1 hour') AT TIME ZONE 'UTC',
       CASE WHEN abs(hashtext('r' || f.entity_id::text)) % 100 < 5 THEN NULL ELSE e.pub_ts END
  FROM (SELECT DISTINCT ON (d.entity_id) d.entity_id, d.agent_id, d.day
          FROM hatamoto_agent_daily d
         WHERE d.entity_id IS NOT NULL AND d.status < 400 AND d.purpose IN ('training', 'search_index', 'user_fetch', 'other_ai')
         ORDER BY d.entity_id, d.day, d.hits DESC, d.agent_id) f
  JOIN ent e ON e.id = f.entity_id
 WHERE e.pub_day >= current_date - 99
ON CONFLICT DO NOTHING;

-- The newest hit of every agent today (last seen shows a time), and sixty spoofed hits for the spoof log.
INSERT INTO hatamoto_agent_hits (occurred_at, agent_id, operator, purpose, verification, ip, ip_truncated, method, path, status, bytes, duration_ms, source)
SELECT now() - (abs(hashtext('t' || id)) % 600) * interval '1 minute', id, operator, purpose,
       CASE WHEN urate = 1 THEN 'unverifiable' ELSE 'verified' END, ('198.51.100.' || (abs(hashtext(id)) % 250 + 1))::inet, false, 'GET', '/blog', 200, 24000, 40, 'log'
  FROM sa WHERE p = 1 AND purpose IN ('training', 'search_index', 'user_fetch', 'other_ai');
INSERT INTO hatamoto_agent_hits (occurred_at, agent_id, operator, purpose, verification, ip, ip_truncated, method, path, status, bytes, duration_ms, source)
SELECT CASE WHEN n <= 5 THEN now() - n * interval '7 minutes' ELSE (current_date - (n % 60))::timestamp AT TIME ZONE 'UTC' + (n * 37 % 1440) * interval '1 minute' END,
       a.id, a.operator, a.purpose, 'spoofed', ('203.0.113.' || (n % 250 + 1))::inet, false, 'GET',
       CASE WHEN n % 2 = 0 THEN '/robots.txt' ELSE '/sitemap.xml' END, 200, 900, 12, 'log'
  FROM generate_series(1, 60) n
  JOIN LATERAL (SELECT * FROM sa WHERE spoof ORDER BY id OFFSET (n - 1) % 5 LIMIT 1) a ON true;

-- Pageviews with the same week and spike, and AI referrals by assistant (about 3.8% of pageviews).
INSERT INTO hatamoto_analytics_daily (day, kind, key, count)
SELECT day, 'pageview', '', round(2900 * wk * (0.85 + 0.15 * (99 - ago) / 99.0) * CASE WHEN ago = 9 THEN 1.25 ELSE 1 END * (0.93 + 0.14 * noise))
  FROM sd ON CONFLICT DO NOTHING;
INSERT INTO hatamoto_analytics_daily (day, kind, key, count)
SELECT sd.day, 'ai_referral', a.key,
       GREATEST(1, round(2900 * sd.wk * (0.85 + 0.15 * (99 - sd.ago) / 99.0) * 0.038 * (0.8 + 0.4 * sd.noise) * CASE WHEN sd.ago = 9 THEN 1.8 ELSE 1 END
                         * a.share * (0.85 + 0.3 * (abs(hashtext(a.key || sd.day::text)) % 100) / 100.0)))
  FROM sd, (VALUES ('chatgpt', 0.56), ('perplexity', 0.15), ('gemini', 0.10), ('claude', 0.084), ('copilot', 0.057), ('grok', 0.026), ('meta_ai', 0.015), ('deepseek', 0.011)) AS a(key, share)
ON CONFLICT DO NOTHING;
COMMIT;
SQL
echo "analytics-seed: 100 days of crawler rollups, first crawls, spoofed hits, pageviews and AI referrals"
