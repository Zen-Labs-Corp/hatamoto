-- A database as hatamoto.dev looked before the locale removal: ten locales with
-- `au` the default, per-locale contact details in the site config, a
-- locale_specific `platform` page, a draft, a post, and the manual cutover
-- redirect that would shadow the page once it moves to /platform.
-- Applied by the integration tests on top of every migration before 202610151230.
INSERT INTO hatamoto_locales (code, country_code, language_code, name, timezone, is_active, is_default, display_order) VALUES
  ('au', 'AU', 'en-AU', 'Australia', 'Australia/Sydney', true, true, 1),
  ('us', 'US', 'en-US', 'United States', 'America/New_York', true, false, 2),
  ('uk', 'GB', 'en-GB', 'United Kingdom', 'Europe/London', true, false, 3),
  ('nz', 'NZ', 'en-NZ', 'New Zealand', 'Pacific/Auckland', true, false, 4),
  ('sg', 'SG', 'en-SG', 'Singapore', 'Asia/Singapore', true, false, 5),
  ('ae', 'AE', 'en-AE', 'United Arab Emirates', 'Asia/Dubai', true, false, 6),
  ('cn', 'CN', 'zh-CN', 'China', 'Asia/Shanghai', true, false, 7),
  ('at', 'AT', 'de-AT', 'Austria', 'Europe/Vienna', true, false, 8),
  ('de', 'DE', 'de-DE', 'Germany', 'Europe/Berlin', true, false, 9),
  ('ca', 'CA', 'en-CA', 'Canada', 'America/Toronto', true, false, 10);

INSERT INTO hatamoto_site_config (company_name, postal_addresses) VALUES ('Prodlike', '{
  "au": {"phone": "+61 2 5550 0100", "country": "AU", "business_hours": "Mon-Fri 9:00-17:30 AEST"},
  "uk": {"city": "London", "phone": "+44 20 7946 0958", "street": "1 Example Street", "country": "GB", "postal_code": "E1 0AA"}
}'::jsonb);

UPDATE hatamoto_entity_schemas SET settings = '{"archive_title": "Insights"}' WHERE slug = 'post';

INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, locale_scope, attributes)
  SELECT id, 'Platform', 'platform', 'published', now(), 'locale_specific', '{"content": "<p>The platform</p>"}'::jsonb
  FROM hatamoto_entity_schemas WHERE slug = 'page';
INSERT INTO hatamoto_entities (schema_id, name, slug, status, locale_scope, attributes)
  SELECT id, 'Untitled', 'untitled-nyyo21', 'draft', 'locale_specific', '{}'::jsonb
  FROM hatamoto_entity_schemas WHERE slug = 'page';
INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at, locale_scope, attributes)
  SELECT id, 'Hello', 'hello', 'published', now(), 'locale_neutral', '{"content": "<p>Hi</p>"}'::jsonb
  FROM hatamoto_entity_schemas WHERE slug = 'post';

INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag) VALUES ('/platform', '/', 301, 'go-theme-cutover');
