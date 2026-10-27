-- +goose Up
-- Person profiles: one `person` entity per human, used as the byline source.
-- Expand only (docs/UPGRADES.md): the previous binary never reads the new
-- columns or the new table, and keeps writing author_id.
--
-- Entities point at people through a NEW column. author_id keeps its FK to
-- hatamoto_authors and is dual-written by the new binary, so a rollback still
-- renders bylines. byline_person_id has no REFERENCES on purpose: adding an FK
-- to an existing table is a contract change, so integrity is kept in code.
ALTER TABLE public.hatamoto_entities
    ADD COLUMN byline_person_id uuid,
    ADD COLUMN byline_org boolean NOT NULL DEFAULT false;

CREATE INDEX idx_entities_byline_person
    ON public.hatamoto_entities USING btree (byline_person_id)
    WHERE (byline_person_id IS NOT NULL);

-- A user owns at most one person (PK on user_id), and a person belongs to at
-- most one user (UNIQUE on entity_id). Both constraints are on a table created
-- in this file, so they are additive.
CREATE TABLE public.hatamoto_user_people (
    user_id uuid PRIMARY KEY REFERENCES public.hatamoto_users(id) ON DELETE CASCADE,
    entity_id uuid NOT NULL UNIQUE REFERENCES public.hatamoto_entities(id) ON DELETE CASCADE,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

-- The built-in `person` content type. A site that already made its own
-- `person` schema is left alone here; the backfill migration refuses to run
-- against a non-builtin one with a clear message.
INSERT INTO public.hatamoto_entity_schemas (name, slug, description, schema, settings, is_builtin)
VALUES (
  'Person', 'person', 'People: team members and bylines',
  '{
    "type": "object",
    "x-schema-org-type": "Person",
    "properties": {
      "job_title":    {"type": "string", "title": "Job title", "maxLength": 120},
      "short_bio":    {"type": "string", "title": "Short bio", "format": "textarea", "maxLength": 300},
      "bio":          {"type": "string", "title": "Bio", "x-hm-field": "richtext"},
      "photo":        {"type": "string", "title": "Photo", "x-media": true},
      "public":       {"type": "boolean", "title": "Public profile", "default": false},
      "public_email": {"type": "string", "title": "Public email", "format": "email"},
      "website":      {"type": "string", "title": "Website", "format": "uri"},
      "linkedin":     {"type": "string", "title": "LinkedIn", "format": "uri"},
      "x":            {"type": "string", "title": "X", "format": "uri"},
      "github":       {"type": "string", "title": "GitHub", "format": "uri"},
      "expertise":    {"type": "array", "title": "Expertise", "items": {"type": "string"}}
    }
  }'::jsonb,
  '{"url_pattern": "/author/{slug}"}'::jsonb,
  true
)
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS public.hatamoto_user_people;
DROP INDEX IF EXISTS public.idx_entities_byline_person;
ALTER TABLE public.hatamoto_entities DROP COLUMN IF EXISTS byline_org, DROP COLUMN IF EXISTS byline_person_id;
DELETE FROM public.hatamoto_entity_schemas WHERE slug = 'person' AND is_builtin = true;
