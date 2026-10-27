-- +goose Up
-- Outgoing webhooks are a core feature again (reverses v1-19-first-party-plugins
-- for webhooks only). The tables have the shape the webhooks plugin kept in its
-- own schema; ids are text so the plugin's uuid strings copy verbatim.
--
-- Subscriptions parked in hatamoto_plugin_handover (the plugin never imported
-- them) are copied here, sanitised like the plugin's sanitizeLegacy, and the
-- drained rows are deleted. The table itself stays: v0.27.0 still deletes from
-- it (PluginDB.Drop) and reads it (handover.list), so the next minor drops it. Subscriptions already living in the plugin's
-- schema (plugin_webhooks) are NOT read here: the boot step copies them through
-- the plugin's own role.
CREATE TABLE hatamoto_webhooks (
    id                  text PRIMARY KEY,
    name                text NOT NULL,
    url                 text NOT NULL,
    secret              text NOT NULL,
    events              jsonb NOT NULL DEFAULT '[]',
    headers             jsonb NOT NULL DEFAULT '{}',
    active              boolean NOT NULL DEFAULT true,
    max_retries         int NOT NULL DEFAULT 3,
    retry_delay_seconds int NOT NULL DEFAULT 60,
    timeout_seconds     int NOT NULL DEFAULT 5,
    allow_http          boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE hatamoto_webhook_deliveries (
    id              text PRIMARY KEY,
    subscription_id text NOT NULL REFERENCES hatamoto_webhooks(id) ON DELETE CASCADE,
    event           text NOT NULL,
    payload         text NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts        int NOT NULL DEFAULT 0,
    max_attempts    int NOT NULL DEFAULT 4,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    leased_until    timestamptz,
    response_status int,
    response_body   text,
    error           text,
    duration_ms     int,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_attempt_at timestamptz,
    delivered_at    timestamptz
);
CREATE INDEX hatamoto_webhook_deliveries_due
    ON hatamoto_webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX hatamoto_webhook_deliveries_by_subscription
    ON hatamoto_webhook_deliveries (subscription_id, created_at DESC);

-- Drain the hand-over table. Rules of sanitizeLegacy: numbers clamped
-- (retries 0-10, delay 0-3600, timeout 1-5), events filtered to the known ones
-- and deduped in order, headers the sender owns dropped (at most 20), name
-- falls back to the url (120 chars), allow_http follows the url scheme (it
-- worked before; the UI flags it), a row left without events or with an
-- unusable url is inserted paused.
-- Active flag: a hand-over row created within the last hour came from
-- 202610201100 running in this same upgrade (a direct jump from the pre-plugin
-- core), so that subscription was delivering a moment ago and keeps its flag.
-- An older row has waited for a plugin since a previous upgrade, nothing has
-- delivered it, and none must start by surprise: it is inserted paused and the
-- owner resumes it in Settings > Webhooks.
-- Name: the webhook validator caps a name at 120 bytes, left() cuts at 120
-- characters, so a long multi-byte name stays over the cap and is inserted
-- paused (the owner must shorten it before resuming).
INSERT INTO hatamoto_webhooks (id, name, url, secret, events, headers, active,
                               max_retries, retry_delay_seconds, timeout_seconds, allow_http)
SELECT c.id,
       left(c.name, 120),
       c.url,
       c.secret,
       c.events,
       c.headers,
       c.fresh AND c.want_active AND jsonb_array_length(c.events) > 0 AND c.url_ok
         AND octet_length(left(c.name, 120)) <= 120,
       least(greatest(c.max_retries, 0), 10),
       least(greatest(c.retry_delay_seconds, 0), 3600),
       least(greatest(c.timeout_seconds, 1), 5),
       lower(c.url) LIKE 'http://%'
  FROM (
    SELECT b.id,
           CASE WHEN b.name = '' THEN b.url ELSE b.name END AS name,
           b.url,
           b.secret,
           b.want_active,
           b.created_at > now() - interval '1 hour' AS fresh,
           b.max_retries, b.retry_delay_seconds, b.timeout_seconds,
           (SELECT coalesce(jsonb_agg(e.ev ORDER BY e.ord), '[]'::jsonb)
              FROM (SELECT DISTINCT ON (t.ev) t.ev, t.ord
                      FROM jsonb_array_elements_text(
                               CASE WHEN jsonb_typeof(b.value->'events') = 'array' THEN b.value->'events' ELSE '[]'::jsonb END
                           ) WITH ORDINALITY AS t(ev, ord)
                     WHERE t.ev IN ('entity.created', 'entity.updated', 'entity.published',
                                    'entity.deleted', 'form.submitted', 'import.completed')
                     ORDER BY t.ev, t.ord) e) AS events,
           (SELECT coalesce(jsonb_object_agg(h.k, h.v), '{}'::jsonb)
              FROM (SELECT x.key AS k, x.value AS v
                      FROM jsonb_each(
                               CASE WHEN jsonb_typeof(b.value->'headers') = 'object' THEN b.value->'headers' ELSE '{}'::jsonb END
                           ) x
                     WHERE jsonb_typeof(x.value) = 'string'
                       AND x.key <> '' AND x.key !~ E'[ :\r\n]'
                       AND (x.value #>> '{}') !~ E'[\r\n]'
                       AND lower(x.key) <> ALL (ARRAY['host', 'content-type', 'content-length', 'user-agent',
                            'connection', 'transfer-encoding', 'upgrade', 'te', 'trailer',
                            'proxy-authorization', 'proxy-connection'])
                     ORDER BY x.key
                     LIMIT 20) h) AS headers,
           (b.url ~* '^https?://' AND b.authority <> '' AND b.authority !~ '@') AS url_ok
      FROM (
        SELECT v.value,
               v.value->>'id' AS id,
               btrim(coalesce(v.value->>'name', ''), E' \t\r\n') AS name,
               btrim(coalesce(v.value->>'url', ''), E' \t\r\n') AS url,
               coalesce(substring(btrim(coalesce(v.value->>'url', ''), E' \t\r\n') FROM '^[A-Za-z]+://([^/?#]*)'), '') AS authority,
               coalesce(v.value->>'secret', '') AS secret,
               coalesce((v.value->>'active')::boolean, false) AS want_active,
               v.created_at,
               coalesce((v.value->>'max_retries')::int, 0) AS max_retries,
               coalesce((v.value->>'retry_delay_seconds')::int, 0) AS retry_delay_seconds,
               coalesce((v.value->>'timeout_seconds')::int, 0) AS timeout_seconds
          FROM hatamoto_plugin_handover v
         WHERE v.plugin = 'webhooks' AND v.key LIKE 'legacy:sub:%'
      ) b
     WHERE coalesce(b.id, '') <> '' AND b.url <> ''
  ) c
ON CONFLICT (id) DO NOTHING;

-- Drained rows hold webhook secrets; do not leave a second copy behind.
-- hatamoto:backfill idempotent
DELETE FROM hatamoto_plugin_handover WHERE plugin = 'webhooks' AND key LIKE 'legacy:sub:%';

-- +goose Down
-- The drained hand-over rows were deleted, so hatamoto_webhooks holds the only
-- copy: refuse while any exist. The hand-over table was never dropped, so
-- 202610201100's Down still finds it.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hatamoto_webhooks) THEN
        RAISE EXCEPTION 'hatamoto_webhooks still holds % subscription(s) that exist nowhere else; delete them on purpose before rolling back',
            (SELECT count(*) FROM hatamoto_webhooks);
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE hatamoto_webhook_deliveries;
DROP TABLE hatamoto_webhooks;
