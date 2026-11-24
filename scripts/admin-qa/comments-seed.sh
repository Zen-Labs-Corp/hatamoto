#!/usr/bin/env bash
# Seed comments for the moderation screens: 3 commenters (new, known, blocked) and 8 comments on two
# "QA Post" entities: pending (held for a link, held by surge mode), approved, spam, trash, one dead (marked by the "example" plugin) and one
# approved reply written by the site owner. Comments stay switched off: the settings row, the block
# lists and the hashed ip columns are left alone. No draft rows (the hourly purge job deletes them).
# Idempotent: fixed ids and ON CONFLICT DO NOTHING; the per-entity counts are recomputed at the end.
# Throwaway QA databases only. Opt-in: run-local.sh calls it when QA_SEED_COMMENTS=1. Needs psql
# (in PG_BIN or on PATH) and the posts and owner that seed.sh creates.
# Usage: QA_USE_DATABASE_URL=1 DATABASE_URL=... scripts/admin-qa/comments-seed.sh
set -Eeuo pipefail
: "${DATABASE_URL:?set DATABASE_URL to the QA database}"
. "$(dirname "${BASH_SOURCE[0]}")/qa-env.sh"
qa_guard_database_url
PSQL=psql
[ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ] && PSQL=$PG_BIN/psql
OWNER=${QA_EMAIL:-owner@qa.test}
"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -v owner="$OWNER" <<'SQL'
BEGIN;
-- Columns follow the comments migration (202611202100_comments.sql).
-- email_hash is a placeholder, never a real HMAC; email_sealed is a 1-byte placeholder (NOT NULL, cannot be opened).
INSERT INTO hatamoto_commenters (id, email_hash, email_sealed, trust_level, approved_count, first_approved_at, last_seen_at) VALUES
  ('c0000000-0000-4000-8000-000000000001', 'qa-seed-1', '\x00', 'new', 0, NULL, now()),
  ('c0000000-0000-4000-8000-000000000002', 'qa-seed-2', '\x00', 'known', 4, now() - interval '20 days', now()),
  ('c0000000-0000-4000-8000-000000000003', 'qa-seed-3', '\x00', 'blocked', 0, NULL, now())
ON CONFLICT (id) DO NOTHING;

CREATE TEMP TABLE qc (n int, id uuid, entity_id uuid, parent_id uuid, status text, commenter_id uuid, author_name text,
                      body text, links boolean, verdict text, reasons text, site boolean, age interval, seq bigint) ON COMMIT DROP;
INSERT INTO qc (n, id, entity_id, parent_id, status, commenter_id, author_name, body, links, verdict, reasons, site, age) VALUES
  (1, 'd0000000-0000-4000-8000-000000000001', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 1' ORDER BY id LIMIT 1), NULL, 'pending',
     'c0000000-0000-4000-8000-000000000001', 'Ada Pending', 'Worth a look: https://example.test/offer', true, 'hold', '["contains a link","new commenter"]', false, interval '1 hour'),
  (2, 'd0000000-0000-4000-8000-000000000002', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 1' ORDER BY id LIMIT 1), NULL, 'pending',
     'c0000000-0000-4000-8000-000000000002', 'Ben Regular', 'Held while the site was busy.', false, 'hold', '["surge mode"]', false, interval '2 hours'),
  (3, 'd0000000-0000-4000-8000-000000000003', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 1' ORDER BY id LIMIT 1), NULL, 'approved',
     'c0000000-0000-4000-8000-000000000002', 'Ben Regular', 'Clear and useful, thank you.', false, 'allow', '[]', false, interval '3 days'),
  (4, 'd0000000-0000-4000-8000-000000000004', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 1' ORDER BY id LIMIT 1), NULL, 'spam',
     'c0000000-0000-4000-8000-000000000003', 'Cheap Deals', 'Buy now, best prices.', false, 'spam', '["blocked commenter"]', false, interval '4 days'),
  (5, 'd0000000-0000-4000-8000-000000000005', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 2' ORDER BY id LIMIT 1), NULL, 'trash',
     'c0000000-0000-4000-8000-000000000001', 'Ada Pending', 'Removed by a moderator.', false, 'allow', '[]', false, interval '5 days'),
  (6, 'd0000000-0000-4000-8000-000000000006', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 2' ORDER BY id LIMIT 1), NULL, 'approved',
     'c0000000-0000-4000-8000-000000000002', 'Ben Regular', 'A second approved comment.', false, 'allow', '[]', false, interval '2 days'),
  (7, 'd0000000-0000-4000-8000-000000000007', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 2' ORDER BY id LIMIT 1), NULL, 'pending',
     'c0000000-0000-4000-8000-000000000001', 'Ada Pending', 'See https://example.test/more for details.', true, 'hold', '["contains a link","new commenter"]', false, interval '30 minutes'),
  (8, 'd0000000-0000-4000-8000-000000000008', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 1' ORDER BY id LIMIT 1), 'd0000000-0000-4000-8000-000000000003', 'approved',
     NULL, 'Site', 'Glad it helped.', false, 'allow', '[]', true, interval '2 days'),
  (9, 'd0000000-0000-4000-8000-000000000009', (SELECT id FROM hatamoto_entities WHERE name = 'QA Post 2' ORDER BY id LIMIT 1), NULL, 'dead',
     'c0000000-0000-4000-8000-000000000002', 'spammer99', 'Marked dead by a plugin.', false, 'allow', '[]', false, interval '6 days');
-- Replies point at a root: its path is that root's one segment (the root may be in this run or already stored).
-- Skip rows already seeded and rows whose post is missing; take one sequence value per row.
DELETE FROM qc WHERE entity_id IS NULL OR id IN (SELECT id FROM hatamoto_comments);
UPDATE qc SET seq = nextval('hatamoto_comments_seq');

INSERT INTO hatamoto_comments (id, seq, entity_id, parent_id, path, depth, status, body_raw, body_html, has_links, link_domains, body_hash,
                               commenter_id, author_name, author_user_id, spam_verdict, spam_reasons, created_at, updated_at, confirmed_at, approved_at, approved_by, dead_by)
SELECT q.id, q.seq, q.entity_id, q.parent_id,
       CASE WHEN q.parent_id IS NULL THEN '' ELSE lpad(to_hex(coalesce(pq.seq, p.seq)), 12, '0') || '.' END || lpad(to_hex(q.seq), 12, '0'),
       CASE WHEN q.parent_id IS NULL THEN 1 ELSE 2 END, q.status, q.body, '<p>' || replace(replace(replace(q.body, '&', '&amp;'), '<', '&lt;'), '>', '&gt;') || '</p>',
       q.links, CASE WHEN q.links THEN '{example.test}'::text[] ELSE '{}'::text[] END, '',
       q.commenter_id, q.author_name, CASE WHEN q.site THEN u.id END, q.verdict, q.reasons::jsonb,
       now() - q.age, now() - q.age, now() - q.age,
       CASE WHEN q.status = 'approved' THEN now() - q.age END, CASE WHEN q.status = 'approved' AND q.site THEN u.id END,
       CASE WHEN q.status = 'dead' THEN 'example' END
  FROM qc q
  LEFT JOIN qc pq ON pq.id = q.parent_id
  LEFT JOIN hatamoto_comments p ON p.id = q.parent_id
  LEFT JOIN hatamoto_users u ON q.site AND u.email = :'owner'
 ORDER BY q.n
ON CONFLICT (id) DO NOTHING;

-- Raw inserts bypass the model's recount, so set the per-entity counts the sidebar badge and cards read.
INSERT INTO hatamoto_comment_entities (entity_id, comment_count, pending_count)
SELECT entity_id, count(*) FILTER (WHERE status = 'approved'), count(*) FILTER (WHERE status = 'pending')
  FROM hatamoto_comments WHERE id::text LIKE 'd0000000-0000-4000-8000-0000000000%' GROUP BY entity_id
ON CONFLICT (entity_id) DO UPDATE SET comment_count = EXCLUDED.comment_count, pending_count = EXCLUDED.pending_count, updated_at = now();
COMMIT;
SQL
echo "comments-seed: 3 commenters and 9 comments (pending, approved, spam, trash, dead, a site reply) on QA Post 1 and 2; comments stay off"
