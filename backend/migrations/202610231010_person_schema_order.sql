-- +goose Up
-- hatamoto:backfill idempotent
-- Field order, editor groups and the `on_team` flag for the built-in `person`
-- type. The first person migration is shipped and not edited, so this one
-- adds them. A schema that already has `x-hm-order` was edited by a site (or
-- ran this already) and is left alone.
UPDATE public.hatamoto_entity_schemas s
SET "schema" = jsonb_set(
      s.schema || jsonb_build_object('x-hm-order', jsonb_build_array(
        'job_title', 'photo', 'short_bio', 'bio',
        'public_email', 'website', 'linkedin', 'x', 'github',
        'expertise', 'public', 'on_team')),
      '{properties}',
      COALESCE(s.schema->'properties', '{}'::jsonb)
        || (
          SELECT COALESCE(jsonb_object_agg(g.name,
                   COALESCE(s.schema->'properties'->g.name, '{}'::jsonb)
                   || jsonb_build_object('x-hm-group', g.grp)), '{}'::jsonb)
          FROM (VALUES
            ('job_title', 'Profile'), ('photo', 'Profile'), ('short_bio', 'Profile'), ('bio', 'Profile'),
            ('public_email', 'Contact and links'), ('website', 'Contact and links'),
            ('linkedin', 'Contact and links'), ('x', 'Contact and links'), ('github', 'Contact and links'),
            ('expertise', 'Expertise'),
            ('public', 'Visibility')
          ) AS g(name, grp)
          WHERE s.schema->'properties' ? g.name
        )
        || jsonb_build_object('on_team', jsonb_build_object(
             'type', 'boolean', 'title', 'On the team', 'default', false, 'x-hm-group', 'Visibility'))
    )
WHERE s.slug = 'person' AND s.is_builtin AND NOT (s.schema ? 'x-hm-order');

-- +goose Down
SELECT 1;
