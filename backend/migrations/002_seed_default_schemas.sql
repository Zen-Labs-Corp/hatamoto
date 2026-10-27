-- +goose Up
-- Seed the two built-in content types the plugin-free core ships by default:
-- `page` and `post`. These were historically created out-of-band (not in the
-- pre-baseline migration chain), so a fresh baseline install had zero content
-- types. Idempotent via the slug unique constraint. `post` carries the former
-- `blog` schema (author is a plain text field per the plugin-free author model).
INSERT INTO hatamoto_entity_schemas (name, slug, description, schema, is_builtin)
VALUES
  ('Page', 'page', 'Static pages',
   '{"type": "object", "properties": {"content": {"type": "string", "title": "Content"}, "description": {"type": "string", "title": "Description"}, "featured_image": {"type": "string", "title": "Featured Image", "format": "uri"}}}'::jsonb,
   true),
  ('Post', 'post', 'Blog posts / articles',
   '{"type": "object", "properties": {"tags": {"type": "array", "items": {"type": "string"}, "title": "Tags"}, "author": {"type": "string", "title": "Author"}, "series": {"type": "string", "title": "Series Name", "description": "Display name of the series this post belongs to"}, "content": {"type": "string", "title": "Content"}, "excerpt": {"type": "string", "title": "Excerpt"}, "series_part": {"type": "integer", "title": "Part Number", "description": "Position in the series. 0 is the index page."}, "series_slug": {"type": "string", "title": "Series Slug", "description": "Slug of the series index entity; groups the parts together"}, "series_total": {"type": "integer", "title": "Total Parts", "description": "Number of parts excluding the index"}, "featured_image": {"type": "string", "title": "Featured Image", "format": "uri"}}}'::jsonb,
   true)
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DELETE FROM hatamoto_entity_schemas WHERE slug IN ('page', 'post') AND is_builtin = true;
