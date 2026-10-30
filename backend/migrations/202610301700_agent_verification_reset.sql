-- +goose Up
-- Re-verify crawler history that was judged against the wrong address.
--
-- Behind a CDN and a local reverse proxy the client IP used to be the CDN
-- edge, so range-published agents were marked spoofed. The address derivation
-- is fixed; this resets the verdicts that can still be re-checked:
--
--  * spoofed, untruncated, inside the 7 day verify window -> pending, so the
--    verify worker judges them again. Only that window is re-checkable: an
--    older pending hit would be swept to unverifiable without a check, so
--    older untruncated rows keep their verdict.
--  * spoofed, truncated (any age) -> unverifiable. A truncated address is a
--    network, never evidence of a spoofed crawler.
--
-- hatamoto_agent_daily moves by the same per-(day, agent, path, status)
-- deltas: spoofed_hits drops by every reset hit, unverifiable_hits grows by
-- the truncated ones, so hits >= verified + spoofed + unverifiable still
-- holds (pending is the remainder). A delta, not a recompute: raw hits older
-- than the retention are gone. Daily rows are locked first in the writer's
-- order (day, agent_id, path, status; bytewise text) so a running verify
-- worker cannot deadlock with this. Re-running finds no spoofed row left to
-- reset and changes nothing.
-- +goose StatementBegin
SELECT 1 FROM public.hatamoto_agent_daily d
JOIN (
    SELECT DISTINCT agent_id, (occurred_at AT TIME ZONE 'UTC')::date AS day, path, COALESCE(status, 0) AS status
    FROM public.hatamoto_agent_hits
    WHERE verification = 'spoofed' AND agent_id IS NOT NULL
      AND (ip_truncated OR occurred_at > now() - interval '7 days')
) k ON d.day = k.day AND d.agent_id = k.agent_id AND d.path = k.path AND d.status = k.status
ORDER BY d.day, d.agent_id COLLATE "C", d.path COLLATE "C", d.status
FOR UPDATE OF d;

WITH moved AS (
    UPDATE public.hatamoto_agent_hits
    SET verification = CASE WHEN ip_truncated THEN 'unverifiable' ELSE 'pending' END
    WHERE verification = 'spoofed'
      AND (ip_truncated OR occurred_at > now() - interval '7 days')
    RETURNING agent_id, (occurred_at AT TIME ZONE 'UTC')::date AS day, path,
              COALESCE(status, 0) AS status, verification
), c AS (
    SELECT agent_id, day, path, status,
           count(*) AS n,
           count(*) FILTER (WHERE verification = 'unverifiable') AS u
    FROM moved
    WHERE agent_id IS NOT NULL
    GROUP BY 1, 2, 3, 4
)
UPDATE public.hatamoto_agent_daily d
SET spoofed_hits      = d.spoofed_hits - c.n,
    unverifiable_hits = d.unverifiable_hits + c.u
FROM c
WHERE d.day = c.day AND d.agent_id = c.agent_id AND d.path = c.path AND d.status = c.status;
-- +goose StatementEnd

-- +goose Down
-- Best-effort no-op: a reset to pending is state-preserving (the verify worker
-- derives the verdict again), and truncated rows cannot be told apart from
-- rows that were unverifiable all along.
SELECT 1;
