-- 45 days of demo analytics: pageviews, AI referrals and crawler rollups for the
-- agents screens. Agent ids follow backend/internal/agents/registry.json (the admin
-- shows the registry name, operator and purpose). `lag` is how many days ago an agent
-- last fetched, `ver` the share of verified fetches, `spoof` the spoofed fetches per day (every 6th).
INSERT INTO hatamoto_analytics_daily (day, kind, key, count)
SELECT d::date, 'pageview', '', 420 + (45 - n) * 9 + (n % 7) * 55
  FROM (SELECT d, row_number() OVER (ORDER BY d) AS n FROM generate_series(current_date - 44, current_date, '1 day') d) s
ON CONFLICT DO NOTHING;
INSERT INTO hatamoto_analytics_daily (day, kind, key, count)
SELECT d::date, 'ai_referral', a.key, a.base + (n * a.step) % 17
  FROM (SELECT d, row_number() OVER (ORDER BY d) AS n FROM generate_series(current_date - 44, current_date, '1 day') d) s,
       (VALUES ('chatgpt', 18, 3), ('perplexity', 7, 5), ('claude', 6, 7), ('gemini', 3, 2)) AS a(key, base, step)
ON CONFLICT DO NOTHING;
INSERT INTO hatamoto_agent_daily (day, agent_id, path, status, operator, purpose, hits, verified_hits, spoofed_hits, unverifiable_hits, bytes_total)
SELECT d::date, a.id, p.path, p.status, a.operator, a.purpose, h,
       h * a.ver / 100,
       CASE WHEN a.spoof > 0 AND n % 6 = 0 AND p.path = '/' THEN a.spoof ELSE 0 END,
       h - h * a.ver / 100,
       h * 17000
  FROM (SELECT d, row_number() OVER (ORDER BY d) AS n FROM generate_series(current_date - 44, current_date, '1 day') d) s,
       (VALUES ('gptbot', 'OpenAI', 'training', 34, 0, 96, 2),
               ('oai-searchbot', 'OpenAI', 'search_index', 22, 0, 94, 0),
               ('chatgpt-user', 'OpenAI', 'user_fetch', 12, 0, 100, 0),
               ('claudebot', 'Anthropic', 'training', 26, 1, 91, 1),
               ('claude-searchbot', 'Anthropic', 'search_index', 9, 3, 88, 0),
               ('perplexitybot', 'Perplexity', 'search_index', 14, 0, 83, 0),
               ('ccbot', 'Common Crawl', 'training', 11, 6, 40, 0),
               ('meta-externalagent', 'Meta', 'training', 7, 12, 72, 1),
               ('google-extended', 'Google', 'training', 6, 2, 100, 0)) AS a(id, operator, purpose, base, lag, ver, spoof),
       (VALUES ('/', 200, 3), ('/blog', 200, 2), ('/blog/shipping-a-calmer-release-process', 200, 2), ('/about', 200, 1), ('/pricing', 404, 1), ('/contact', 500, 1)) AS p(path, status, w),
       LATERAL (SELECT GREATEST(1, (a.base * p.w + (n * 7 + length(a.id) * 3) % 13 - 4)::bigint) AS h) hh
 WHERE s.d::date <= current_date - a.lag
   AND NOT (p.status = 500 AND n % 9 <> 0) AND NOT (p.status = 404 AND n % 3 <> 0)
   AND NOT (a.id = 'chatgpt-user' AND n % 5 = 0)
ON CONFLICT DO NOTHING;
