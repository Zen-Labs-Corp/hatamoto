-- +goose Up
-- v1-19-first-party-plugins: outgoing webhooks are the first-party "webhooks"
-- plugin now. Hand the subscriptions over, then drop the core tables.
--
-- The plugin cannot read core tables, so each subscription is copied into the
-- core-only table hatamoto_plugin_handover (plugin 'webhooks', key
-- 'legacy:sub:<id>'). Only the host reads it: it hands the rows to a plugin of
-- that name through handover.list when it asks, and the plugin imports them
-- into its own schema and acknowledges each one (handover.ack), which deletes
-- it. Unlike kv, the plugin cannot write or enumerate other rows there, and the
-- signing secrets are not mixed in with the plugin's own state. Installing the
-- plugin later is fine: the rows wait. Delivery history is not carried over
-- (it is a log, not config).
-- The old signing secret was stored as the part after 'whsec_'; the plugin
-- keeps signing with 'whsec_' + that part, so receivers need no change.
CREATE TABLE hatamoto_plugin_handover (
    plugin     text NOT NULL,
    key        text NOT NULL,
    value      jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin, key)
);

INSERT INTO hatamoto_plugin_handover (plugin, key, value)
SELECT 'webhooks', 'legacy:sub:' || w.id::text,
       jsonb_build_object(
           'id', w.id,
           'name', w.name,
           'url', w.url,
           'secret', CASE WHEN w.secret_hash IS NULL OR w.secret_hash = '' THEN '' ELSE 'whsec_' || w.secret_hash END,
           'events', w.events,
           'headers', COALESCE(w.headers, '{}'::jsonb),
           'active', COALESCE(w.is_active, true),
           'max_retries', COALESCE(w.max_retries, 3),
           'retry_delay_seconds', COALESCE(w.retry_delay_seconds, 60),
           'timeout_seconds', COALESCE(w.timeout_seconds, 30))
  FROM public.hatamoto_webhooks w
ON CONFLICT (plugin, key) DO NOTHING;

DROP TABLE public.hatamoto_webhook_deliveries;
DROP TABLE public.hatamoto_webhooks;

-- API keys no longer have webhook scopes.
UPDATE public.hatamoto_api_keys
   SET scopes = COALESCE((SELECT jsonb_agg(s) FROM jsonb_array_elements(scopes) s
                           WHERE s #>> '{}' NOT IN ('read:webhooks', 'write:webhooks')), '[]'::jsonb)
 WHERE scopes::text LIKE '%:webhooks%';

-- +goose Down
-- Structure only, as the other down migrations do: the tables come back empty
-- and the subscriptions imported by the plugin stay with it. Subscriptions
-- that are still waiting for the plugin are the only copy, so Down refuses to
-- run while any are left: install the plugin (it imports them) or delete the
-- rows on purpose first.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hatamoto_plugin_handover) THEN
        RAISE EXCEPTION 'hatamoto_plugin_handover still holds % row(s) not yet imported by their plugin; start the plugin so it imports them, or delete them first',
            (SELECT count(*) FROM hatamoto_plugin_handover);
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE hatamoto_plugin_handover;

CREATE TABLE public.hatamoto_webhooks (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    name character varying(255) NOT NULL,
    description text,
    url text NOT NULL,
    secret_id uuid,
    events jsonb DEFAULT '[]'::jsonb NOT NULL,
    headers jsonb DEFAULT '{}'::jsonb,
    is_active boolean DEFAULT true,
    max_retries integer DEFAULT 3,
    retry_delay_seconds integer DEFAULT 60,
    timeout_seconds integer DEFAULT 30,
    total_deliveries bigint DEFAULT 0,
    successful_deliveries bigint DEFAULT 0,
    failed_deliveries bigint DEFAULT 0,
    last_delivery_at timestamp with time zone,
    last_success_at timestamp with time zone,
    last_failure_at timestamp with time zone,
    last_error text,
    created_by uuid REFERENCES public.hatamoto_users(id) ON DELETE SET NULL,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    secret_hash character varying(64),
    CONSTRAINT hatamoto_webhooks_max_retries_check CHECK (((max_retries >= 0) AND (max_retries <= 10))),
    CONSTRAINT hatamoto_webhooks_timeout_check CHECK (((timeout_seconds >= 1) AND (timeout_seconds <= 120))),
    CONSTRAINT hatamoto_webhooks_url_check CHECK ((url ~ '^https?://'::text))
);
CREATE INDEX idx_webhooks_events ON public.hatamoto_webhooks USING gin (events);
CREATE INDEX idx_webhooks_secret_hash ON public.hatamoto_webhooks USING btree (id, secret_hash) WHERE (secret_hash IS NOT NULL);
CREATE TRIGGER webhooks_updated_at BEFORE UPDATE ON public.hatamoto_webhooks FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();

CREATE TABLE public.hatamoto_webhook_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL PRIMARY KEY,
    webhook_id uuid NOT NULL REFERENCES public.hatamoto_webhooks(id) ON DELETE CASCADE,
    event_type character varying(100) NOT NULL,
    event_id uuid NOT NULL,
    payload jsonb NOT NULL,
    status character varying(50) DEFAULT 'pending'::character varying,
    attempt_number integer DEFAULT 1,
    max_attempts integer DEFAULT 3,
    response_status integer,
    response_body text,
    response_headers jsonb,
    response_time_ms integer,
    error_message text,
    scheduled_at timestamp with time zone DEFAULT now(),
    delivered_at timestamp with time zone,
    next_retry_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now(),
    CONSTRAINT hatamoto_webhook_deliveries_status_check CHECK (((status)::text = ANY (ARRAY[('pending'::character varying)::text, ('processing'::character varying)::text, ('delivered'::character varying)::text, ('failed'::character varying)::text, ('cancelled'::character varying)::text])))
);
CREATE INDEX idx_webhook_deliveries_created ON public.hatamoto_webhook_deliveries USING btree (webhook_id, created_at DESC);
CREATE INDEX idx_webhook_deliveries_pending ON public.hatamoto_webhook_deliveries USING btree (scheduled_at) WHERE ((status)::text = ANY (ARRAY[('pending'::character varying)::text, ('processing'::character varying)::text]));
CREATE INDEX idx_webhook_deliveries_retry ON public.hatamoto_webhook_deliveries USING btree (next_retry_at) WHERE (((status)::text = 'pending'::text) AND (next_retry_at IS NOT NULL));
CREATE INDEX idx_webhook_deliveries_status ON public.hatamoto_webhook_deliveries USING btree (status);
CREATE INDEX idx_webhook_deliveries_webhook ON public.hatamoto_webhook_deliveries USING btree (webhook_id);
