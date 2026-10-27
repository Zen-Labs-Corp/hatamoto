-- +goose Up
-- NOTE: the pgvector `vector` extension was removed from this file once (v1-05) so a
-- plain postgres:16 can migrate. Existing databases already recorded version 1, and
-- 202610150116 drops vector where present. See migrations/README.md.
-- Hatamoto Core plugin-free baseline schema (squash of migrations 001-127).
-- Generated from live hatamoto_core minus 8 plugin tables + geo_location_id + backup_124 + FORCE RLS.

--
-- PostgreSQL database dump
--

-- Dumped from database version 16.12 (Ubuntu 16.12-1.pgdg24.04+1)
-- Dumped by pg_dump version 16.12 (Ubuntu 16.12-1.pgdg24.04+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: citext; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public;


--
-- Name: EXTENSION citext; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION citext IS 'data type for case-insensitive character strings';


--
-- Name: uuid-ossp; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS "uuid-ossp" WITH SCHEMA public;


--
-- Name: EXTENSION "uuid-ossp"; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION "uuid-ossp" IS 'generate universally unique identifiers (UUIDs)';


--
-- Name: update_updated_at(); Type: FUNCTION; Schema: public; Owner: -
--

-- +goose StatementBegin
CREATE FUNCTION public.update_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: hatamoto_404_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_404_log (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    path text NOT NULL,
    hit_count integer DEFAULT 1 NOT NULL,
    last_referrer text,
    last_user_agent text,
    first_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    ignored boolean DEFAULT false NOT NULL,
    redirect_id uuid
);


--
-- Name: hatamoto_api_key_usage; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_api_key_usage (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    api_key_id uuid NOT NULL,
    endpoint character varying(255) NOT NULL,
    method character varying(10) NOT NULL,
    status_code integer,
    response_time_ms integer,
    ip_address character varying(45),
    user_agent text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: hatamoto_api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_api_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    description text,
    key_prefix character varying(16) NOT NULL,
    key_hash character varying(64) NOT NULL,
    scopes jsonb DEFAULT '["read:entities"]'::jsonb,
    rate_limit integer DEFAULT 1000,
    expires_at timestamp with time zone,
    last_used_at timestamp with time zone,
    last_used_ip character varying(45),
    request_count bigint DEFAULT 0,
    is_active boolean DEFAULT true,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    revoked_at timestamp with time zone,
    revoked_by uuid
);


--
-- Name: hatamoto_bot_visits; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_bot_visits (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    bot_name text NOT NULL,
    bot_category text NOT NULL,
    path text NOT NULL,
    hit_count integer DEFAULT 1 NOT NULL,
    visit_date date DEFAULT CURRENT_DATE NOT NULL,
    last_user_agent text,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_entities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entities (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    schema_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    slug character varying(255) NOT NULL,
    status character varying(20) DEFAULT 'draft'::character varying,
    attributes jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb,
    version integer DEFAULT 1,
    created_by uuid,
    updated_by uuid,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    published_at timestamp with time zone,
    editorial jsonb DEFAULT '{}'::jsonb,
    parent_id uuid,
    content_role character varying(30) DEFAULT 'standard'::character varying,
    locale_scope character varying(20) DEFAULT 'locale_specific'::character varying,
    locale_code character varying(5),
    gated boolean DEFAULT false NOT NULL,
    fact_confidence numeric,
    successor_entity_id uuid,
    CONSTRAINT hatamoto_entities_scheduled_needs_date CHECK ((((status)::text <> 'scheduled'::text) OR (published_at IS NOT NULL))),
    CONSTRAINT hatamoto_entities_status_check CHECK (((status)::text = ANY (ARRAY[('draft'::character varying)::text, ('scheduled'::character varying)::text, ('published'::character varying)::text, ('archived'::character varying)::text, ('redirected'::character varying)::text])))
);


--
-- Name: COLUMN hatamoto_entities.editorial; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.hatamoto_entities.editorial IS 'Editorial provenance: reviewed_by, reviewed_at, fact_checked, sources[], last_verified_at';


--
-- Name: hatamoto_entity_link_audit; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entity_link_audit (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source_entity_id uuid NOT NULL,
    target_entity_id uuid NOT NULL,
    old_href text NOT NULL,
    new_href text NOT NULL,
    anchor_text text NOT NULL,
    reason character varying(32) NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_entity_link_index; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entity_link_index (
    source_entity_id uuid NOT NULL,
    target_entity_id uuid NOT NULL,
    target_locale_code character varying(8),
    anchor_text text NOT NULL,
    occurrence_index integer NOT NULL,
    href text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_entity_locale_overrides; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entity_locale_overrides (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    entity_id uuid NOT NULL,
    locale_code character varying(10) NOT NULL,
    attribute_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_overrides jsonb DEFAULT '{}'::jsonb,
    seo_overrides jsonb DEFAULT '{}'::jsonb,
    slug_override character varying(255),
    status character varying(20) DEFAULT 'inherit'::character varying,
    version integer DEFAULT 1,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    CONSTRAINT chk_status CHECK (((status)::text = ANY (ARRAY[('inherit'::character varying)::text, ('draft'::character varying)::text, ('published'::character varying)::text])))
);


--
-- Name: hatamoto_entity_schemas; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entity_schemas (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(100) NOT NULL,
    slug character varying(100) NOT NULL,
    description text,
    schema jsonb NOT NULL,
    is_builtin boolean DEFAULT false,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    ui_schema jsonb DEFAULT '{}'::jsonb
);


--
-- Name: hatamoto_entity_terms; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entity_terms (
    entity_id uuid NOT NULL,
    term_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_entity_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_entity_versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    entity_id uuid NOT NULL,
    version integer NOT NULL,
    attributes jsonb NOT NULL,
    changed_by uuid,
    change_summary text,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: hatamoto_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    visitor_id uuid,
    lead_id uuid,
    event_type character varying(64) NOT NULL,
    entity_id uuid,
    session_id uuid,
    url text,
    referrer text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL
)
PARTITION BY RANGE (occurred_at);


--
-- Name: hatamoto_events_2026_09; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_events_2026_09 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    visitor_id uuid,
    lead_id uuid,
    event_type character varying(64) NOT NULL,
    entity_id uuid,
    session_id uuid,
    url text,
    referrer text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_events_2026_10; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_events_2026_10 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    visitor_id uuid,
    lead_id uuid,
    event_type character varying(64) NOT NULL,
    entity_id uuid,
    session_id uuid,
    url text,
    referrer text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_events_2026_11; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_events_2026_11 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    visitor_id uuid,
    lead_id uuid,
    event_type character varying(64) NOT NULL,
    entity_id uuid,
    session_id uuid,
    url text,
    referrer text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_events_2026_12; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_events_2026_12 (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    visitor_id uuid,
    lead_id uuid,
    event_type character varying(64) NOT NULL,
    entity_id uuid,
    session_id uuid,
    url text,
    referrer text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_events_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_events_default (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    visitor_id uuid,
    lead_id uuid,
    event_type character varying(64) NOT NULL,
    entity_id uuid,
    session_id uuid,
    url text,
    referrer text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_form_submissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_form_submissions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    form_id uuid NOT NULL,
    data jsonb NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb,
    status character varying(50) DEFAULT 'new'::character varying,
    created_at timestamp with time zone DEFAULT now(),
    CONSTRAINT hatamoto_form_submissions_status_check CHECK (((status)::text = ANY (ARRAY[('new'::character varying)::text, ('read'::character varying)::text, ('archived'::character varying)::text, ('spam'::character varying)::text])))
);


--
-- Name: hatamoto_forms; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_forms (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    slug character varying(255) NOT NULL,
    description text,
    fields jsonb DEFAULT '[]'::jsonb NOT NULL,
    settings jsonb DEFAULT '{}'::jsonb,
    status character varying(50) DEFAULT 'active'::character varying,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    CONSTRAINT hatamoto_forms_status_check CHECK (((status)::text = ANY (ARRAY[('active'::character varying)::text, ('inactive'::character varying)::text, ('archived'::character varying)::text])))
);


--
-- Name: hatamoto_intent_signals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_intent_signals (
    event_type character varying(64) NOT NULL,
    weight integer DEFAULT 1 NOT NULL,
    decay_days integer DEFAULT 30 NOT NULL,
    description text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_internal_link_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_internal_link_rules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_entity_id uuid NOT NULL,
    rule_type character varying(30) DEFAULT 'keyword'::character varying NOT NULL,
    match_value character varying(500) NOT NULL,
    priority integer DEFAULT 50,
    max_links_per_source integer DEFAULT 1,
    is_active boolean DEFAULT true,
    anchor_text character varying(500),
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    source character varying(30) DEFAULT 'manual'::character varying
);


--
-- Name: COLUMN hatamoto_internal_link_rules.source; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.hatamoto_internal_link_rules.source IS 'How this rule was created: manual, auto_name, auto_partial, auto_tag, auto_sibling, bulk_import';


--
-- Name: hatamoto_leads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_leads (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email public.citext NOT NULL,
    email_normalized text GENERATED ALWAYS AS (lower(TRIM(BOTH FROM (email)::text))) STORED,
    name character varying(200) NOT NULL,
    verified_at timestamp with time zone,
    phone character varying(50),
    phone_country character varying(16),
    country character varying(100),
    company character varying(200),
    job_title character varying(150),
    industry character varying(120),
    company_size character varying(40),
    website character varying(255),
    linkedin_url character varying(255),
    timezone character varying(60),
    locale character varying(16),
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    source_entity_id uuid,
    source_url text,
    source_locale character varying(8),
    utm_source character varying(120),
    utm_medium character varying(120),
    utm_campaign character varying(120),
    utm_term character varying(120),
    utm_content character varying(120),
    referrer_url text,
    ip_first_seen inet,
    ip_last_seen inet,
    user_agent_first_seen text,
    user_agent_last_seen text,
    last_seen_at timestamp with time zone,
    unsubscribed_at timestamp with time zone,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    score integer DEFAULT 0 NOT NULL,
    score_band character varying(16) DEFAULT 'cold'::character varying NOT NULL,
    scored_at timestamp with time zone,
    email_status text DEFAULT 'ok'::text NOT NULL,
    email_status_reason text,
    email_status_changed_at timestamp with time zone,
    sales_engaged_at timestamp with time zone,
    merged_into_id uuid,
    intent_score integer DEFAULT 0 NOT NULL,
    intent_facets jsonb DEFAULT '{}'::jsonb NOT NULL,
    intent_tier character varying(16),
    CONSTRAINT hatamoto_leads_email_status_check CHECK ((email_status = ANY (ARRAY['ok'::text, 'soft_bouncing'::text, 'hard_bounced'::text, 'complained'::text, 'suppressed'::text])))
);


--
-- Name: COLUMN hatamoto_leads.sales_engaged_at; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.hatamoto_leads.sales_engaged_at IS 'Set when the lead completes the strategy-session wizard (graduates to sales). Suppresses all future nurture sends. Never auto-cleared — re-engagement is a manual admin action.';


--
-- Name: hatamoto_locales; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_locales (
    code character varying(10) NOT NULL,
    country_code character varying(2) NOT NULL,
    language_code character varying(5) NOT NULL,
    name character varying(100) NOT NULL,
    currency character varying(3),
    timezone character varying(50),
    phone_prefix character varying(10),
    is_active boolean DEFAULT true,
    display_order integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now(),
    is_default boolean DEFAULT false NOT NULL
);


--
-- Name: hatamoto_media; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_media (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    filename character varying(255) NOT NULL,
    original_filename character varying(255) NOT NULL,
    mime_type character varying(100) NOT NULL,
    file_size bigint NOT NULL,
    storage_provider character varying(20) DEFAULT 'local'::character varying,
    storage_path character varying(500) NOT NULL,
    public_url character varying(500),
    width integer,
    height integer,
    variants jsonb DEFAULT '[]'::jsonb,
    alt_text character varying(500),
    caption text,
    folder character varying(255) DEFAULT '/'::character varying,
    uploaded_by uuid,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: hatamoto_menu_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_menu_items (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    menu_id uuid NOT NULL,
    parent_id uuid,
    label character varying(100) NOT NULL,
    link_type character varying(20) NOT NULL,
    entity_id uuid,
    url character varying(500),
    schema_slug character varying(100),
    "position" integer DEFAULT 0,
    open_in_new_tab boolean DEFAULT false,
    css_classes character varying(200),
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    CONSTRAINT hatamoto_menu_items_link_type_check CHECK (((link_type)::text = ANY (ARRAY[('entity'::character varying)::text, ('external'::character varying)::text, ('archive'::character varying)::text, ('custom'::character varying)::text])))
);


--
-- Name: hatamoto_menus; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_menus (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(100) NOT NULL,
    slug character varying(100) NOT NULL,
    location character varying(50),
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: hatamoto_redirects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_redirects (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source_path text NOT NULL,
    target_url text NOT NULL,
    status_code integer DEFAULT 301 NOT NULL,
    hit_count integer DEFAULT 0 NOT NULL,
    is_active boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source_tag text DEFAULT 'admin-ui'::text,
    CONSTRAINT chk_redirects_status_code CHECK ((status_code = ANY (ARRAY[301, 302, 307, 308])))
);


--
-- Name: hatamoto_search_console; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_search_console (
    singleton boolean DEFAULT true NOT NULL,
    refresh_token_enc text,
    site_url text,
    connected_at timestamp with time zone,
    last_used_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hatamoto_search_console_singleton_check CHECK ((singleton = true))
);


--
-- Name: hatamoto_secrets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_secrets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    key character varying(255) NOT NULL,
    encrypted_value bytea NOT NULL,
    nonce bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_section_templates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_section_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    description text DEFAULT ''::text,
    section_type character varying(50) NOT NULL,
    content jsonb DEFAULT '{}'::jsonb NOT NULL,
    tags text[] DEFAULT '{}'::text[],
    usage_count integer DEFAULT 0,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: hatamoto_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    refresh_token character varying(255),
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    session_token_hash bytea,
    lead_id uuid,
    kind text DEFAULT 'admin'::text NOT NULL,
    last_used_at timestamp with time zone,
    user_agent text,
    ip inet,
    CONSTRAINT chk_sessions_kind CHECK ((kind = ANY (ARRAY['admin'::text, 'lead'::text]))),
    CONSTRAINT chk_sessions_user_xor_lead CHECK (((user_id IS NOT NULL) <> (lead_id IS NOT NULL)))
);


--
-- Name: TABLE hatamoto_sessions; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.hatamoto_sessions IS 'Server-side sessions for both admin (kind=admin, user_id set) and lead (kind=lead, lead_id set) auth. Replaces JWT-based auth as of PR 1 of Path B (multi-tenancy strip). session_token_hash is SHA-256 of the opaque cookie value; the value itself never lives in the DB.';


--
-- Name: hatamoto_site_config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_site_config (
    id boolean DEFAULT true NOT NULL,
    company_name text NOT NULL,
    logo_url text,
    custom_domain text,
    primary_color text,
    theme_extra jsonb DEFAULT '{}'::jsonb NOT NULL,
    tagline text,
    timezone text,
    homepage_entity_id uuid,
    notification_email text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    postal_address jsonb,
    postal_addresses jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT hatamoto_site_config_id_check CHECK ((id = true))
);


--
-- Name: hatamoto_terms; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_terms (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    kind character varying(16) NOT NULL,
    slug character varying(128) NOT NULL,
    name character varying(128) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hatamoto_terms_kind_check CHECK (((kind)::text = ANY (ARRAY[('tag'::character varying)::text, ('category'::character varying)::text])))
);


--
-- Name: hatamoto_users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_users (
    id uuid NOT NULL,
    email text NOT NULL,
    password_hash text,
    role text DEFAULT 'editor'::text NOT NULL,
    name text,
    avatar_url text,
    last_login_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hatamoto_visitors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_visitors (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    first_seen timestamp with time zone DEFAULT now() NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL,
    identified_lead_id uuid,
    identified_at timestamp with time zone,
    ip inet,
    user_agent text
);


--
-- Name: hatamoto_webhook_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_webhook_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    webhook_id uuid NOT NULL,
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


--
-- Name: hatamoto_webhooks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hatamoto_webhooks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
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
    created_by uuid,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    secret_hash character varying(64),
    CONSTRAINT hatamoto_webhooks_max_retries_check CHECK (((max_retries >= 0) AND (max_retries <= 10))),
    CONSTRAINT hatamoto_webhooks_timeout_check CHECK (((timeout_seconds >= 1) AND (timeout_seconds <= 120))),
    CONSTRAINT hatamoto_webhooks_url_check CHECK ((url ~ '^https?://'::text))
);


--
-- Name: lead_activities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lead_activities (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lead_id uuid NOT NULL,
    entity_id uuid,
    session_id uuid,
    event_type character varying(64) NOT NULL,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    ip inet,
    user_agent text,
    referrer text,
    url text
);


--
-- Name: lead_email_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lead_email_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lead_id uuid NOT NULL,
    message_id text NOT NULL,
    ses_message_id text,
    event_type text NOT NULL,
    bounce_type text,
    bounce_subtype text,
    complaint_feedback_type text,
    diagnostic_code text,
    email_address text NOT NULL,
    email_purpose text,
    raw_event jsonb,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT lead_email_events_event_type_check CHECK ((event_type = ANY (ARRAY['send'::text, 'delivery'::text, 'bounce'::text, 'complaint'::text, 'reject'::text, 'suppressed'::text, 'open'::text, 'click'::text])))
);


--
-- Name: lead_magic_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lead_magic_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lead_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    purpose character varying(32) DEFAULT 'login'::character varying NOT NULL,
    requested_ip inet,
    requested_ua text,
    redirect_to text,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    consumed_ip inet,
    consumed_ua text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: lead_merges; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lead_merges (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source_id uuid NOT NULL,
    target_id uuid NOT NULL,
    merged_at timestamp with time zone DEFAULT now() NOT NULL,
    merged_by uuid,
    reason text,
    source_snapshot jsonb NOT NULL,
    CONSTRAINT lead_merges_distinct CHECK ((source_id <> target_id))
);


--
-- Name: lead_nurture_queue; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lead_nurture_queue (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lead_id uuid NOT NULL,
    step integer NOT NULL,
    scheduled_for timestamp with time zone NOT NULL,
    sent_at timestamp with time zone,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    last_attempt_at timestamp with time zone
);


--
-- Name: lead_remember_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.lead_remember_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    lead_id uuid NOT NULL,
    family_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    revoked_reason text,
    last_ip inet,
    last_ua text,
    CONSTRAINT chk_lead_remember_tokens_reason CHECK (((revoked_reason IS NULL) OR (revoked_reason = ANY (ARRAY['rotated'::text, 'reuse_detected'::text, 'logout'::text, 'admin'::text, 'lead_scrubbed'::text, 'expired'::text]))))
);


--
-- Name: TABLE lead_remember_tokens; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.lead_remember_tokens IS 'Long-lived remember-me tokens for lead sessions. Stored in frontend localStorage; consumed via /api/v1/leads/session/refresh to mint a fresh nx_lead cookie. Single-use rotation in family_id chains. Raw token never persisted — only SHA-256 hash.';


--
-- Name: COLUMN lead_remember_tokens.family_id; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.lead_remember_tokens.family_id IS 'Chain of rotated tokens. Burning the family (UPDATE WHERE family_id) is the reuse-detection response.';


--
-- Name: COLUMN lead_remember_tokens.revoked_reason; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.lead_remember_tokens.revoked_reason IS 'One of: rotated (normal single-use consumption), reuse_detected (revoked row was presented again), logout (explicit user logout), admin (manual scrub), lead_scrubbed (lead soft-deleted/unsubscribed).';


--
-- Name: hatamoto_events_2026_09; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events ATTACH PARTITION public.hatamoto_events_2026_09 FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');


--
-- Name: hatamoto_events_2026_10; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events ATTACH PARTITION public.hatamoto_events_2026_10 FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');


--
-- Name: hatamoto_events_2026_11; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events ATTACH PARTITION public.hatamoto_events_2026_11 FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');


--
-- Name: hatamoto_events_2026_12; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events ATTACH PARTITION public.hatamoto_events_2026_12 FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');


--
-- Name: hatamoto_events_default; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events ATTACH PARTITION public.hatamoto_events_default DEFAULT;


--
-- Name: hatamoto_locales chk_locales_one_default; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_locales
    ADD CONSTRAINT chk_locales_one_default EXCLUDE USING btree (is_default WITH =) WHERE (is_default);


--
-- Name: hatamoto_404_log hatamoto_404_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_404_log
    ADD CONSTRAINT hatamoto_404_log_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_api_key_usage hatamoto_api_key_usage_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_api_key_usage
    ADD CONSTRAINT hatamoto_api_key_usage_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_api_keys hatamoto_api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_api_keys
    ADD CONSTRAINT hatamoto_api_keys_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_bot_visits hatamoto_bot_visits_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_bot_visits
    ADD CONSTRAINT hatamoto_bot_visits_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_entities hatamoto_entities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entities
    ADD CONSTRAINT hatamoto_entities_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_entity_link_audit hatamoto_entity_link_audit_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_audit
    ADD CONSTRAINT hatamoto_entity_link_audit_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_entity_link_index hatamoto_entity_link_index_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_index
    ADD CONSTRAINT hatamoto_entity_link_index_pkey PRIMARY KEY (source_entity_id, target_entity_id, occurrence_index);


--
-- Name: hatamoto_entity_locale_overrides hatamoto_entity_locale_overrides_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_locale_overrides
    ADD CONSTRAINT hatamoto_entity_locale_overrides_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_entity_schemas hatamoto_entity_schemas_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_schemas
    ADD CONSTRAINT hatamoto_entity_schemas_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_entity_schemas hatamoto_entity_schemas_slug_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_schemas
    ADD CONSTRAINT hatamoto_entity_schemas_slug_unique UNIQUE (slug);


--
-- Name: hatamoto_entity_terms hatamoto_entity_terms_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_terms
    ADD CONSTRAINT hatamoto_entity_terms_pkey PRIMARY KEY (entity_id, term_id);


--
-- Name: hatamoto_entity_versions hatamoto_entity_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_versions
    ADD CONSTRAINT hatamoto_entity_versions_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_entity_versions hatamoto_entity_versions_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_versions
    ADD CONSTRAINT hatamoto_entity_versions_unique UNIQUE (entity_id, version);


--
-- Name: hatamoto_events hatamoto_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events
    ADD CONSTRAINT hatamoto_events_pkey PRIMARY KEY (id, occurred_at);


--
-- Name: hatamoto_events_2026_09 hatamoto_events_2026_09_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events_2026_09
    ADD CONSTRAINT hatamoto_events_2026_09_pkey PRIMARY KEY (id, occurred_at);


--
-- Name: hatamoto_events_2026_10 hatamoto_events_2026_10_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events_2026_10
    ADD CONSTRAINT hatamoto_events_2026_10_pkey PRIMARY KEY (id, occurred_at);


--
-- Name: hatamoto_events_2026_11 hatamoto_events_2026_11_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events_2026_11
    ADD CONSTRAINT hatamoto_events_2026_11_pkey PRIMARY KEY (id, occurred_at);


--
-- Name: hatamoto_events_2026_12 hatamoto_events_2026_12_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events_2026_12
    ADD CONSTRAINT hatamoto_events_2026_12_pkey PRIMARY KEY (id, occurred_at);


--
-- Name: hatamoto_events_default hatamoto_events_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_events_default
    ADD CONSTRAINT hatamoto_events_default_pkey PRIMARY KEY (id, occurred_at);


--
-- Name: hatamoto_form_submissions hatamoto_form_submissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_form_submissions
    ADD CONSTRAINT hatamoto_form_submissions_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_forms hatamoto_forms_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_forms
    ADD CONSTRAINT hatamoto_forms_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_forms hatamoto_forms_slug_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_forms
    ADD CONSTRAINT hatamoto_forms_slug_unique UNIQUE (slug);


--
-- Name: hatamoto_intent_signals hatamoto_intent_signals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_intent_signals
    ADD CONSTRAINT hatamoto_intent_signals_pkey PRIMARY KEY (event_type);


--
-- Name: hatamoto_internal_link_rules hatamoto_internal_link_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_internal_link_rules
    ADD CONSTRAINT hatamoto_internal_link_rules_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_leads hatamoto_leads_email_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_leads
    ADD CONSTRAINT hatamoto_leads_email_unique UNIQUE (email_normalized);


--
-- Name: hatamoto_leads hatamoto_leads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_leads
    ADD CONSTRAINT hatamoto_leads_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_internal_link_rules hatamoto_link_rules_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_internal_link_rules
    ADD CONSTRAINT hatamoto_link_rules_unique UNIQUE (target_entity_id, rule_type, match_value);


--
-- Name: hatamoto_locales hatamoto_locales_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_locales
    ADD CONSTRAINT hatamoto_locales_pkey PRIMARY KEY (code);


--
-- Name: hatamoto_media hatamoto_media_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_media
    ADD CONSTRAINT hatamoto_media_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_menu_items hatamoto_menu_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_menu_items
    ADD CONSTRAINT hatamoto_menu_items_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_menus hatamoto_menus_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_menus
    ADD CONSTRAINT hatamoto_menus_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_menus hatamoto_menus_slug_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_menus
    ADD CONSTRAINT hatamoto_menus_slug_unique UNIQUE (slug);


--
-- Name: hatamoto_redirects hatamoto_redirects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_redirects
    ADD CONSTRAINT hatamoto_redirects_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_search_console hatamoto_search_console_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_search_console
    ADD CONSTRAINT hatamoto_search_console_pkey PRIMARY KEY (singleton);


--
-- Name: hatamoto_secrets hatamoto_secrets_key_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_secrets
    ADD CONSTRAINT hatamoto_secrets_key_unique UNIQUE (key);


--
-- Name: hatamoto_secrets hatamoto_secrets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_secrets
    ADD CONSTRAINT hatamoto_secrets_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_section_templates hatamoto_section_templates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_section_templates
    ADD CONSTRAINT hatamoto_section_templates_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_sessions hatamoto_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_sessions
    ADD CONSTRAINT hatamoto_sessions_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_sessions hatamoto_sessions_refresh_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_sessions
    ADD CONSTRAINT hatamoto_sessions_refresh_token_key UNIQUE (refresh_token);


--
-- Name: hatamoto_site_config hatamoto_site_config_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_site_config
    ADD CONSTRAINT hatamoto_site_config_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_terms hatamoto_terms_kind_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_terms
    ADD CONSTRAINT hatamoto_terms_kind_slug_key UNIQUE (kind, slug);


--
-- Name: hatamoto_terms hatamoto_terms_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_terms
    ADD CONSTRAINT hatamoto_terms_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_users hatamoto_users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_users
    ADD CONSTRAINT hatamoto_users_email_key UNIQUE (email);


--
-- Name: hatamoto_users hatamoto_users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_users
    ADD CONSTRAINT hatamoto_users_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_visitors hatamoto_visitors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_visitors
    ADD CONSTRAINT hatamoto_visitors_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_webhook_deliveries hatamoto_webhook_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_webhook_deliveries
    ADD CONSTRAINT hatamoto_webhook_deliveries_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_webhooks hatamoto_webhooks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_webhooks
    ADD CONSTRAINT hatamoto_webhooks_pkey PRIMARY KEY (id);


--
-- Name: lead_activities lead_activities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_activities
    ADD CONSTRAINT lead_activities_pkey PRIMARY KEY (id);


--
-- Name: lead_email_events lead_email_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_email_events
    ADD CONSTRAINT lead_email_events_pkey PRIMARY KEY (id);


--
-- Name: lead_magic_tokens lead_magic_tokens_hash_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_magic_tokens
    ADD CONSTRAINT lead_magic_tokens_hash_unique UNIQUE (token_hash);


--
-- Name: lead_magic_tokens lead_magic_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_magic_tokens
    ADD CONSTRAINT lead_magic_tokens_pkey PRIMARY KEY (id);


--
-- Name: lead_merges lead_merges_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_merges
    ADD CONSTRAINT lead_merges_pkey PRIMARY KEY (id);


--
-- Name: lead_nurture_queue lead_nurture_queue_lead_step_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_nurture_queue
    ADD CONSTRAINT lead_nurture_queue_lead_step_unique UNIQUE (lead_id, step);


--
-- Name: lead_nurture_queue lead_nurture_queue_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_nurture_queue
    ADD CONSTRAINT lead_nurture_queue_pkey PRIMARY KEY (id);


--
-- Name: lead_remember_tokens lead_remember_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_remember_tokens
    ADD CONSTRAINT lead_remember_tokens_pkey PRIMARY KEY (id);


--
-- Name: hatamoto_404_log uq_404_log_path; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_404_log
    ADD CONSTRAINT uq_404_log_path UNIQUE (path);


--
-- Name: hatamoto_entity_locale_overrides uq_entity_locale; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_locale_overrides
    ADD CONSTRAINT uq_entity_locale UNIQUE (entity_id, locale_code);


--
-- Name: hatamoto_redirects uq_redirects_source; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_redirects
    ADD CONSTRAINT uq_redirects_source UNIQUE (source_path);


--
-- Name: hatamoto_bot_visits_upsert; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX hatamoto_bot_visits_upsert ON public.hatamoto_bot_visits USING btree (bot_name, path, visit_date);


--
-- Name: idx_hatamoto_events_type_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_events_type_time ON ONLY public.hatamoto_events USING btree (event_type, occurred_at DESC);


--
-- Name: hatamoto_events_2026_09_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_09_event_type_occurred_at_idx ON public.hatamoto_events_2026_09 USING btree (event_type, occurred_at DESC);


--
-- Name: idx_hatamoto_events_lead_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_events_lead_time ON ONLY public.hatamoto_events USING btree (lead_id, occurred_at DESC) WHERE (lead_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_09_lead_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_09_lead_id_occurred_at_idx ON public.hatamoto_events_2026_09 USING btree (lead_id, occurred_at DESC) WHERE (lead_id IS NOT NULL);


--
-- Name: idx_hatamoto_events_received_brin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_events_received_brin ON ONLY public.hatamoto_events USING brin (received_at);


--
-- Name: hatamoto_events_2026_09_received_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_09_received_at_idx ON public.hatamoto_events_2026_09 USING brin (received_at);


--
-- Name: idx_hatamoto_events_visitor_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_events_visitor_time ON ONLY public.hatamoto_events USING btree (visitor_id, occurred_at DESC) WHERE (visitor_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_09_visitor_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_09_visitor_id_occurred_at_idx ON public.hatamoto_events_2026_09 USING btree (visitor_id, occurred_at DESC) WHERE (visitor_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_10_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_10_event_type_occurred_at_idx ON public.hatamoto_events_2026_10 USING btree (event_type, occurred_at DESC);


--
-- Name: hatamoto_events_2026_10_lead_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_10_lead_id_occurred_at_idx ON public.hatamoto_events_2026_10 USING btree (lead_id, occurred_at DESC) WHERE (lead_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_10_received_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_10_received_at_idx ON public.hatamoto_events_2026_10 USING brin (received_at);


--
-- Name: hatamoto_events_2026_10_visitor_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_10_visitor_id_occurred_at_idx ON public.hatamoto_events_2026_10 USING btree (visitor_id, occurred_at DESC) WHERE (visitor_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_11_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_11_event_type_occurred_at_idx ON public.hatamoto_events_2026_11 USING btree (event_type, occurred_at DESC);


--
-- Name: hatamoto_events_2026_11_lead_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_11_lead_id_occurred_at_idx ON public.hatamoto_events_2026_11 USING btree (lead_id, occurred_at DESC) WHERE (lead_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_11_received_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_11_received_at_idx ON public.hatamoto_events_2026_11 USING brin (received_at);


--
-- Name: hatamoto_events_2026_11_visitor_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_11_visitor_id_occurred_at_idx ON public.hatamoto_events_2026_11 USING btree (visitor_id, occurred_at DESC) WHERE (visitor_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_12_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_12_event_type_occurred_at_idx ON public.hatamoto_events_2026_12 USING btree (event_type, occurred_at DESC);


--
-- Name: hatamoto_events_2026_12_lead_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_12_lead_id_occurred_at_idx ON public.hatamoto_events_2026_12 USING btree (lead_id, occurred_at DESC) WHERE (lead_id IS NOT NULL);


--
-- Name: hatamoto_events_2026_12_received_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_12_received_at_idx ON public.hatamoto_events_2026_12 USING brin (received_at);


--
-- Name: hatamoto_events_2026_12_visitor_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_2026_12_visitor_id_occurred_at_idx ON public.hatamoto_events_2026_12 USING btree (visitor_id, occurred_at DESC) WHERE (visitor_id IS NOT NULL);


--
-- Name: hatamoto_events_default_event_type_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_default_event_type_occurred_at_idx ON public.hatamoto_events_default USING btree (event_type, occurred_at DESC);


--
-- Name: hatamoto_events_default_lead_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_default_lead_id_occurred_at_idx ON public.hatamoto_events_default USING btree (lead_id, occurred_at DESC) WHERE (lead_id IS NOT NULL);


--
-- Name: hatamoto_events_default_received_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_default_received_at_idx ON public.hatamoto_events_default USING brin (received_at);


--
-- Name: hatamoto_events_default_visitor_id_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX hatamoto_events_default_visitor_id_occurred_at_idx ON public.hatamoto_events_default USING btree (visitor_id, occurred_at DESC) WHERE (visitor_id IS NOT NULL);


--
-- Name: idx_api_key_usage_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_key_usage_created ON public.hatamoto_api_key_usage USING btree (api_key_id, created_at DESC);


--
-- Name: idx_api_key_usage_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_key_usage_key ON public.hatamoto_api_key_usage USING btree (api_key_id);


--
-- Name: idx_api_keys_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_keys_hash ON public.hatamoto_api_keys USING btree (key_hash);


--
-- Name: idx_api_keys_prefix; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_keys_prefix ON public.hatamoto_api_keys USING btree (key_prefix);


--
-- Name: idx_entities_attributes; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_attributes ON public.hatamoto_entities USING gin (attributes);


--
-- Name: idx_entities_enabled_locales; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_enabled_locales ON public.hatamoto_entities USING gin (((attributes -> 'enabled_locales'::text)) jsonb_path_ops) WHERE ((locale_scope)::text = 'locale_specific'::text);


--
-- Name: idx_entities_locale_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_locale_code ON public.hatamoto_entities USING btree (locale_code);


--
-- Name: idx_entities_parent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_parent ON public.hatamoto_entities USING btree (parent_id) WHERE (parent_id IS NOT NULL);


--
-- Name: idx_entities_scheduled_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_scheduled_due ON public.hatamoto_entities USING btree (published_at) WHERE ((status)::text = 'scheduled'::text);


--
-- Name: idx_entities_schema; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_schema ON public.hatamoto_entities USING btree (schema_id);


--
-- Name: idx_entities_series_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_series_slug ON public.hatamoto_entities USING btree (((attributes ->> 'series_slug'::text))) WHERE (attributes ? 'series_slug'::text);


--
-- Name: idx_entities_successor; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_successor ON public.hatamoto_entities USING btree (successor_entity_id) WHERE (successor_entity_id IS NOT NULL);


--
-- Name: idx_entities_url; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entities_url ON public.hatamoto_entities USING gin (attributes jsonb_path_ops);


--
-- Name: idx_entity_link_audit_source_recent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_link_audit_source_recent ON public.hatamoto_entity_link_audit USING btree (source_entity_id, created_at DESC);


--
-- Name: idx_entity_link_index_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_link_index_source ON public.hatamoto_entity_link_index USING btree (source_entity_id);


--
-- Name: idx_entity_link_index_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_link_index_target ON public.hatamoto_entity_link_index USING btree (target_entity_id, target_locale_code);


--
-- Name: idx_entity_locale_overrides_attrs; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_locale_overrides_attrs ON public.hatamoto_entity_locale_overrides USING gin (attribute_overrides);


--
-- Name: idx_entity_locale_overrides_entity; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_locale_overrides_entity ON public.hatamoto_entity_locale_overrides USING btree (entity_id);


--
-- Name: idx_entity_locale_overrides_locale; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_locale_overrides_locale ON public.hatamoto_entity_locale_overrides USING btree (locale_code);


--
-- Name: idx_entity_versions_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_versions_created ON public.hatamoto_entity_versions USING btree (entity_id, version DESC);


--
-- Name: idx_entity_versions_entity; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_versions_entity ON public.hatamoto_entity_versions USING btree (entity_id);


--
-- Name: idx_form_submissions_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_form_submissions_created ON public.hatamoto_form_submissions USING btree (form_id, created_at DESC);


--
-- Name: idx_form_submissions_form; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_form_submissions_form ON public.hatamoto_form_submissions USING btree (form_id);


--
-- Name: idx_form_submissions_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_form_submissions_status ON public.hatamoto_form_submissions USING btree (form_id, status);


--
-- Name: idx_hatamoto_entity_terms_term; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_entity_terms_term ON public.hatamoto_entity_terms USING btree (term_id, entity_id);


--
-- Name: idx_hatamoto_terms_kind_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_terms_kind_name ON public.hatamoto_terms USING btree (kind, name);


--
-- Name: idx_hatamoto_visitors_identified_lead; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_visitors_identified_lead ON public.hatamoto_visitors USING btree (identified_lead_id) WHERE (identified_lead_id IS NOT NULL);


--
-- Name: idx_hatamoto_visitors_last_seen; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_hatamoto_visitors_last_seen ON public.hatamoto_visitors USING btree (last_seen DESC);


--
-- Name: idx_lead_activities_entity_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_activities_entity_time ON public.lead_activities USING btree (entity_id, occurred_at DESC) WHERE (entity_id IS NOT NULL);


--
-- Name: idx_lead_activities_lead_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_activities_lead_time ON public.lead_activities USING btree (lead_id, occurred_at DESC);


--
-- Name: idx_lead_activities_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_activities_session ON public.lead_activities USING btree (session_id, occurred_at) WHERE (session_id IS NOT NULL);


--
-- Name: idx_lead_email_events_lead_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_email_events_lead_time ON public.lead_email_events USING btree (lead_id, occurred_at DESC);


--
-- Name: idx_lead_merges_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_merges_source ON public.lead_merges USING btree (source_id);


--
-- Name: idx_lead_merges_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_merges_target ON public.lead_merges USING btree (target_id, merged_at DESC);


--
-- Name: idx_lead_remember_tokens_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_remember_tokens_expires ON public.lead_remember_tokens USING btree (expires_at);


--
-- Name: idx_lead_remember_tokens_family; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_remember_tokens_family ON public.lead_remember_tokens USING btree (family_id);


--
-- Name: idx_lead_remember_tokens_lead_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_lead_remember_tokens_lead_active ON public.lead_remember_tokens USING btree (lead_id, family_id) WHERE (revoked_at IS NULL);


--
-- Name: idx_leads_email_normalized; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_leads_email_normalized ON public.hatamoto_leads USING btree (email_normalized);


--
-- Name: idx_link_rules_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_link_rules_target ON public.hatamoto_internal_link_rules USING btree (target_entity_id);


--
-- Name: idx_locales_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_locales_active ON public.hatamoto_locales USING btree (code) WHERE is_active;


--
-- Name: idx_magic_tokens_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_magic_tokens_expires ON public.lead_magic_tokens USING btree (expires_at) WHERE (used_at IS NULL);


--
-- Name: idx_magic_tokens_lead_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_magic_tokens_lead_active ON public.lead_magic_tokens USING btree (lead_id, created_at DESC) WHERE (used_at IS NULL);


--
-- Name: idx_menu_items_menu; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_menu_items_menu ON public.hatamoto_menu_items USING btree (menu_id);


--
-- Name: idx_menu_items_parent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_menu_items_parent ON public.hatamoto_menu_items USING btree (parent_id);


--
-- Name: idx_menu_items_position; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_menu_items_position ON public.hatamoto_menu_items USING btree (menu_id, parent_id, "position");


--
-- Name: idx_leads_intent_score; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_leads_intent_score ON public.hatamoto_leads USING btree (intent_score DESC) WHERE (deleted_at IS NULL);


--
-- Name: idx_leads_intent_tier; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_leads_intent_tier ON public.hatamoto_leads USING btree (intent_tier) WHERE ((deleted_at IS NULL) AND (intent_tier IS NOT NULL));


--
-- Name: idx_leads_merged_into; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_leads_merged_into ON public.hatamoto_leads USING btree (merged_into_id) WHERE (merged_into_id IS NOT NULL);


--
-- Name: idx_leads_sales_engaged_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_leads_sales_engaged_at ON public.hatamoto_leads USING btree (sales_engaged_at DESC) WHERE (sales_engaged_at IS NOT NULL);


--
-- Name: idx_nurture_lead; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_nurture_lead ON public.lead_nurture_queue USING btree (lead_id);


--
-- Name: idx_nurture_pending_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_nurture_pending_due ON public.lead_nurture_queue USING btree (scheduled_for) WHERE ((status)::text = 'pending'::text);


--
-- Name: idx_sessions_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_expires ON public.hatamoto_sessions USING btree (expires_at);


--
-- Name: idx_sessions_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_expires_at ON public.hatamoto_sessions USING btree (expires_at);


--
-- Name: idx_sessions_lead_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_lead_id ON public.hatamoto_sessions USING btree (lead_id) WHERE (lead_id IS NOT NULL);


--
-- Name: idx_sessions_token; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_token ON public.hatamoto_sessions USING btree (refresh_token);


--
-- Name: idx_sessions_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_user ON public.hatamoto_sessions USING btree (user_id);


--
-- Name: idx_sessions_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_user_id ON public.hatamoto_sessions USING btree (user_id) WHERE (user_id IS NOT NULL);


--
-- Name: idx_webhook_deliveries_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_created ON public.hatamoto_webhook_deliveries USING btree (webhook_id, created_at DESC);


--
-- Name: idx_webhook_deliveries_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_pending ON public.hatamoto_webhook_deliveries USING btree (scheduled_at) WHERE ((status)::text = ANY (ARRAY[('pending'::character varying)::text, ('processing'::character varying)::text]));


--
-- Name: idx_webhook_deliveries_retry; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_retry ON public.hatamoto_webhook_deliveries USING btree (next_retry_at) WHERE (((status)::text = 'pending'::text) AND (next_retry_at IS NOT NULL));


--
-- Name: idx_webhook_deliveries_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_status ON public.hatamoto_webhook_deliveries USING btree (status);


--
-- Name: idx_webhook_deliveries_webhook; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_webhook ON public.hatamoto_webhook_deliveries USING btree (webhook_id);


--
-- Name: idx_webhooks_events; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhooks_events ON public.hatamoto_webhooks USING gin (events);


--
-- Name: idx_webhooks_secret_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhooks_secret_hash ON public.hatamoto_webhooks USING btree (id, secret_hash) WHERE (secret_hash IS NOT NULL);


--
-- Name: uq_lead_email_events_dedup; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_lead_email_events_dedup ON public.lead_email_events USING btree (lead_id, message_id, event_type, COALESCE(ses_message_id, ''::text));


--
-- Name: uq_lead_remember_tokens_active_family; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_lead_remember_tokens_active_family ON public.lead_remember_tokens USING btree (family_id) WHERE (revoked_at IS NULL);


--
-- Name: uq_lead_remember_tokens_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_lead_remember_tokens_hash ON public.lead_remember_tokens USING btree (token_hash);


--
-- Name: uq_sessions_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_sessions_token_hash ON public.hatamoto_sessions USING btree (session_token_hash) WHERE (session_token_hash IS NOT NULL);


--
-- Name: hatamoto_events_2026_09_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_type_time ATTACH PARTITION public.hatamoto_events_2026_09_event_type_occurred_at_idx;


--
-- Name: hatamoto_events_2026_09_lead_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_lead_time ATTACH PARTITION public.hatamoto_events_2026_09_lead_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_09_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.hatamoto_events_pkey ATTACH PARTITION public.hatamoto_events_2026_09_pkey;


--
-- Name: hatamoto_events_2026_09_received_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_received_brin ATTACH PARTITION public.hatamoto_events_2026_09_received_at_idx;


--
-- Name: hatamoto_events_2026_09_visitor_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_visitor_time ATTACH PARTITION public.hatamoto_events_2026_09_visitor_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_10_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_type_time ATTACH PARTITION public.hatamoto_events_2026_10_event_type_occurred_at_idx;


--
-- Name: hatamoto_events_2026_10_lead_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_lead_time ATTACH PARTITION public.hatamoto_events_2026_10_lead_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_10_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.hatamoto_events_pkey ATTACH PARTITION public.hatamoto_events_2026_10_pkey;


--
-- Name: hatamoto_events_2026_10_received_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_received_brin ATTACH PARTITION public.hatamoto_events_2026_10_received_at_idx;


--
-- Name: hatamoto_events_2026_10_visitor_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_visitor_time ATTACH PARTITION public.hatamoto_events_2026_10_visitor_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_11_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_type_time ATTACH PARTITION public.hatamoto_events_2026_11_event_type_occurred_at_idx;


--
-- Name: hatamoto_events_2026_11_lead_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_lead_time ATTACH PARTITION public.hatamoto_events_2026_11_lead_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_11_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.hatamoto_events_pkey ATTACH PARTITION public.hatamoto_events_2026_11_pkey;


--
-- Name: hatamoto_events_2026_11_received_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_received_brin ATTACH PARTITION public.hatamoto_events_2026_11_received_at_idx;


--
-- Name: hatamoto_events_2026_11_visitor_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_visitor_time ATTACH PARTITION public.hatamoto_events_2026_11_visitor_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_12_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_type_time ATTACH PARTITION public.hatamoto_events_2026_12_event_type_occurred_at_idx;


--
-- Name: hatamoto_events_2026_12_lead_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_lead_time ATTACH PARTITION public.hatamoto_events_2026_12_lead_id_occurred_at_idx;


--
-- Name: hatamoto_events_2026_12_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.hatamoto_events_pkey ATTACH PARTITION public.hatamoto_events_2026_12_pkey;


--
-- Name: hatamoto_events_2026_12_received_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_received_brin ATTACH PARTITION public.hatamoto_events_2026_12_received_at_idx;


--
-- Name: hatamoto_events_2026_12_visitor_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_visitor_time ATTACH PARTITION public.hatamoto_events_2026_12_visitor_id_occurred_at_idx;


--
-- Name: hatamoto_events_default_event_type_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_type_time ATTACH PARTITION public.hatamoto_events_default_event_type_occurred_at_idx;


--
-- Name: hatamoto_events_default_lead_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_lead_time ATTACH PARTITION public.hatamoto_events_default_lead_id_occurred_at_idx;


--
-- Name: hatamoto_events_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.hatamoto_events_pkey ATTACH PARTITION public.hatamoto_events_default_pkey;


--
-- Name: hatamoto_events_default_received_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_received_brin ATTACH PARTITION public.hatamoto_events_default_received_at_idx;


--
-- Name: hatamoto_events_default_visitor_id_occurred_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_hatamoto_events_visitor_time ATTACH PARTITION public.hatamoto_events_default_visitor_id_occurred_at_idx;


--
-- Name: hatamoto_api_keys api_keys_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER api_keys_updated_at BEFORE UPDATE ON public.hatamoto_api_keys FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_entities entities_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER entities_updated_at BEFORE UPDATE ON public.hatamoto_entities FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_entity_locale_overrides entity_locale_overrides_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER entity_locale_overrides_updated_at BEFORE UPDATE ON public.hatamoto_entity_locale_overrides FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_entity_schemas entity_schemas_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER entity_schemas_updated_at BEFORE UPDATE ON public.hatamoto_entity_schemas FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_forms forms_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER forms_updated_at BEFORE UPDATE ON public.hatamoto_forms FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: lead_nurture_queue lead_nurture_queue_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER lead_nurture_queue_updated_at BEFORE UPDATE ON public.lead_nurture_queue FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_leads leads_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER leads_updated_at BEFORE UPDATE ON public.hatamoto_leads FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_internal_link_rules link_rules_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER link_rules_updated_at BEFORE UPDATE ON public.hatamoto_internal_link_rules FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_menu_items menu_items_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER menu_items_updated_at BEFORE UPDATE ON public.hatamoto_menu_items FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_menus menus_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER menus_updated_at BEFORE UPDATE ON public.hatamoto_menus FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_redirects redirects_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER redirects_updated_at BEFORE UPDATE ON public.hatamoto_redirects FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_secrets secrets_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER secrets_updated_at BEFORE UPDATE ON public.hatamoto_secrets FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_section_templates section_templates_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER section_templates_updated_at BEFORE UPDATE ON public.hatamoto_section_templates FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_webhooks webhooks_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER webhooks_updated_at BEFORE UPDATE ON public.hatamoto_webhooks FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: hatamoto_404_log fk_404_log_redirect; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_404_log
    ADD CONSTRAINT fk_404_log_redirect FOREIGN KEY (redirect_id) REFERENCES public.hatamoto_redirects(id) ON DELETE SET NULL;


--
-- Name: lead_activities fk_activity_entity; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_activities
    ADD CONSTRAINT fk_activity_entity FOREIGN KEY (entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE SET NULL;


--
-- Name: lead_activities fk_activity_lead; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_activities
    ADD CONSTRAINT fk_activity_lead FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE CASCADE;


--
-- Name: hatamoto_api_key_usage fk_api_key_usage_key; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_api_key_usage
    ADD CONSTRAINT fk_api_key_usage_key FOREIGN KEY (api_key_id) REFERENCES public.hatamoto_api_keys(id) ON DELETE CASCADE;


--
-- Name: hatamoto_api_keys fk_api_keys_created_by; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_api_keys
    ADD CONSTRAINT fk_api_keys_created_by FOREIGN KEY (created_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: hatamoto_api_keys fk_api_keys_revoked_by; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_api_keys
    ADD CONSTRAINT fk_api_keys_revoked_by FOREIGN KEY (revoked_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: lead_email_events fk_email_events_lead; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_email_events
    ADD CONSTRAINT fk_email_events_lead FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entities fk_entities_created_by; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entities
    ADD CONSTRAINT fk_entities_created_by FOREIGN KEY (created_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: hatamoto_entities fk_entities_schema; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entities
    ADD CONSTRAINT fk_entities_schema FOREIGN KEY (schema_id) REFERENCES public.hatamoto_entity_schemas(id) ON DELETE SET NULL;


--
-- Name: hatamoto_entities fk_entities_updated_by; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entities
    ADD CONSTRAINT fk_entities_updated_by FOREIGN KEY (updated_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: hatamoto_entity_versions fk_entity_versions_entity; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_versions
    ADD CONSTRAINT fk_entity_versions_entity FOREIGN KEY (entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_versions fk_entity_versions_user; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_versions
    ADD CONSTRAINT fk_entity_versions_user FOREIGN KEY (changed_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: hatamoto_form_submissions fk_form_submissions_form; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_form_submissions
    ADD CONSTRAINT fk_form_submissions_form FOREIGN KEY (form_id) REFERENCES public.hatamoto_forms(id) ON DELETE CASCADE;


--
-- Name: hatamoto_leads fk_leads_source_entity; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_leads
    ADD CONSTRAINT fk_leads_source_entity FOREIGN KEY (source_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE SET NULL;


--
-- Name: hatamoto_internal_link_rules fk_link_rules_target; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_internal_link_rules
    ADD CONSTRAINT fk_link_rules_target FOREIGN KEY (target_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: lead_magic_tokens fk_magic_lead; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_magic_tokens
    ADD CONSTRAINT fk_magic_lead FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE CASCADE;


--
-- Name: hatamoto_media fk_media_uploaded_by; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_media
    ADD CONSTRAINT fk_media_uploaded_by FOREIGN KEY (uploaded_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: hatamoto_menu_items fk_menu_items_entity; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_menu_items
    ADD CONSTRAINT fk_menu_items_entity FOREIGN KEY (entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE SET NULL;


--
-- Name: hatamoto_menu_items fk_menu_items_menu; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_menu_items
    ADD CONSTRAINT fk_menu_items_menu FOREIGN KEY (menu_id) REFERENCES public.hatamoto_menus(id) ON DELETE CASCADE;


--
-- Name: hatamoto_menu_items fk_menu_items_parent; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_menu_items
    ADD CONSTRAINT fk_menu_items_parent FOREIGN KEY (parent_id) REFERENCES public.hatamoto_menu_items(id) ON DELETE CASCADE;


--
-- Name: lead_nurture_queue fk_nurture_lead; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_nurture_queue
    ADD CONSTRAINT fk_nurture_lead FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE CASCADE;


--
-- Name: hatamoto_sessions fk_sessions_user; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_sessions
    ADD CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES public.hatamoto_users(id) ON DELETE CASCADE;


--
-- Name: hatamoto_webhook_deliveries fk_webhook_deliveries_webhook; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_webhook_deliveries
    ADD CONSTRAINT fk_webhook_deliveries_webhook FOREIGN KEY (webhook_id) REFERENCES public.hatamoto_webhooks(id) ON DELETE CASCADE;


--
-- Name: hatamoto_webhooks fk_webhooks_created_by; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_webhooks
    ADD CONSTRAINT fk_webhooks_created_by FOREIGN KEY (created_by) REFERENCES public.hatamoto_users(id) ON DELETE SET NULL;


--
-- Name: hatamoto_entities hatamoto_entities_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entities
    ADD CONSTRAINT hatamoto_entities_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.hatamoto_entities(id) ON DELETE SET NULL;


--
-- Name: hatamoto_entities hatamoto_entities_successor_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entities
    ADD CONSTRAINT hatamoto_entities_successor_entity_id_fkey FOREIGN KEY (successor_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE SET NULL;


--
-- Name: hatamoto_entity_link_audit hatamoto_entity_link_audit_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_audit
    ADD CONSTRAINT hatamoto_entity_link_audit_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.hatamoto_users(id);


--
-- Name: hatamoto_entity_link_audit hatamoto_entity_link_audit_source_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_audit
    ADD CONSTRAINT hatamoto_entity_link_audit_source_entity_id_fkey FOREIGN KEY (source_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_link_audit hatamoto_entity_link_audit_target_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_audit
    ADD CONSTRAINT hatamoto_entity_link_audit_target_entity_id_fkey FOREIGN KEY (target_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_link_index hatamoto_entity_link_index_source_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_index
    ADD CONSTRAINT hatamoto_entity_link_index_source_entity_id_fkey FOREIGN KEY (source_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_link_index hatamoto_entity_link_index_target_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_link_index
    ADD CONSTRAINT hatamoto_entity_link_index_target_entity_id_fkey FOREIGN KEY (target_entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_locale_overrides hatamoto_entity_locale_overrides_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_locale_overrides
    ADD CONSTRAINT hatamoto_entity_locale_overrides_entity_id_fkey FOREIGN KEY (entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_locale_overrides hatamoto_entity_locale_overrides_locale_code_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_locale_overrides
    ADD CONSTRAINT hatamoto_entity_locale_overrides_locale_code_fkey FOREIGN KEY (locale_code) REFERENCES public.hatamoto_locales(code);


--
-- Name: hatamoto_entity_terms hatamoto_entity_terms_entity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_terms
    ADD CONSTRAINT hatamoto_entity_terms_entity_id_fkey FOREIGN KEY (entity_id) REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE;


--
-- Name: hatamoto_entity_terms hatamoto_entity_terms_term_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_entity_terms
    ADD CONSTRAINT hatamoto_entity_terms_term_id_fkey FOREIGN KEY (term_id) REFERENCES public.hatamoto_terms(id) ON DELETE CASCADE;


--
-- Name: hatamoto_events hatamoto_events_lead_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE public.hatamoto_events
    ADD CONSTRAINT hatamoto_events_lead_id_fkey FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE SET NULL;


--
-- Name: hatamoto_leads hatamoto_leads_merged_into_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_leads
    ADD CONSTRAINT hatamoto_leads_merged_into_id_fkey FOREIGN KEY (merged_into_id) REFERENCES public.hatamoto_leads(id) ON DELETE RESTRICT;


--
-- Name: hatamoto_section_templates hatamoto_section_templates_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_section_templates
    ADD CONSTRAINT hatamoto_section_templates_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.hatamoto_users(id);


--
-- Name: hatamoto_sessions hatamoto_sessions_lead_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_sessions
    ADD CONSTRAINT hatamoto_sessions_lead_id_fkey FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE CASCADE;


--
-- Name: hatamoto_visitors hatamoto_visitors_identified_lead_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hatamoto_visitors
    ADD CONSTRAINT hatamoto_visitors_identified_lead_id_fkey FOREIGN KEY (identified_lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE SET NULL;


--
-- Name: lead_merges lead_merges_source_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_merges
    ADD CONSTRAINT lead_merges_source_id_fkey FOREIGN KEY (source_id) REFERENCES public.hatamoto_leads(id) ON DELETE RESTRICT;


--
-- Name: lead_merges lead_merges_target_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_merges
    ADD CONSTRAINT lead_merges_target_id_fkey FOREIGN KEY (target_id) REFERENCES public.hatamoto_leads(id) ON DELETE RESTRICT;


--
-- Name: lead_remember_tokens lead_remember_tokens_lead_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.lead_remember_tokens
    ADD CONSTRAINT lead_remember_tokens_lead_id_fkey FOREIGN KEY (lead_id) REFERENCES public.hatamoto_leads(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--


-- Restore the search_path the pg_dump header blanked, so goose can record the version.
SELECT pg_catalog.set_config('search_path', '"$user", public', false);

-- +goose Down
-- +goose StatementBegin
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
-- +goose StatementEnd
