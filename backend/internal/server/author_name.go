package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/render"
)

// personBioRunes bounds the bio shown in a byline card.
const personBioRunes = 300

// people is the person store over the server's pool.
func (s *Server) people() *person.Store { return person.NewPoolStore(s.db.Pool()) }

// personPath is the public path of a person: their schema's URL pattern
// (/author/{slug} by default).
func (s *Server) personPath(ctx context.Context, slug string) string {
	return s.urlSnapshot(ctx).EntityPath(&entity.Entity{
		Slug: slug, Schema: &entity.EntitySchema{Slug: person.SchemaSlug},
	}, nil)
}

// bylines are the resolved credits of a batch of entities, read in two
// queries whatever the batch size.
type bylines struct {
	credits map[uuid.UUID]person.Credit
	people  map[uuid.UUID]*person.Person
	org     string // the site organisation's name
	// path is the public path of a person slug.
	path func(ctx context.Context, slug string) string
}

// bylinesOf resolves the byline of each entity id; a failure is logged and
// answered with no credits, so the plain-text bylines still render.
func (s *Server) bylinesOf(ctx context.Context, ids ...uuid.UUID) *bylines {
	b := &bylines{path: s.personPath, credits: map[uuid.UUID]person.Credit{}, people: map[uuid.UUID]*person.Person{}}
	if s.db == nil {
		return b
	}
	credits, err := s.people().Credits(ctx, ids)
	if err != nil {
		s.logger.Warn("render: entity bylines", "error", err)
		return b
	}
	b.credits = credits
	var personIDs []uuid.UUID
	for _, c := range credits {
		if c.PersonID != nil {
			personIDs = append(personIDs, *c.PersonID)
		}
		if c.Org && b.org == "" && s.siteConfig != nil {
			if cfg, err := s.siteConfig.Get(ctx); err == nil {
				b.org = strings.TrimSpace(cfg.CompanyName)
			}
		}
	}
	if b.people, err = s.people().ByIDs(ctx, personIDs); err != nil {
		s.logger.Warn("render: byline people", "error", err)
		b.people = map[uuid.UUID]*person.Person{}
	}
	return b
}

// view is the byline of entity id: its person (the name only when the person is
// not public) or the site organisation, else
// the plain attributes.author / author_name text (an entity credited to
// nobody). It never falls back to the account that created the entity. The
// view links to the person page only when that person is public and the
// entity's schema is one of linkSchemas (the schemas listed there). The
// organisation has no page.
func (b *bylines) view(ctx context.Context, id uuid.UUID, attrs map[string]any, schema string, linkSchemas []string) *render.AuthorView {
	c := b.credits[id]
	switch {
	case c.Org && b.org != "":
		return &render.AuthorView{Name: b.org, IsOrg: true}
	case c.PersonID != nil && b.people[*c.PersonID] != nil:
		p := b.people[*c.PersonID]
		// A person who is not public is a name and nothing else: no photo, job
		// title or bio, and no link.
		if !p.Public {
			return &render.AuthorView{Name: p.Name}
		}
		v := &render.AuthorView{Name: p.Name, Avatar: p.Photo, Bio: firstNonEmpty(p.ShortBio, plainText(p.Bio, personBioRunes)), JobTitle: p.JobTitle}
		if slices.Contains(linkSchemas, schema) {
			v.Slug, v.URL = p.Slug, b.path(ctx, p.Slug)
		}
		return v
	}
	name := firstNonEmpty(strings.TrimSpace(strAttr(attrs, "author")), strings.TrimSpace(strAttr(attrs, "author_name")))
	if name == "" {
		return nil
	}
	return &render.AuthorView{Name: name}
}

// applyAuthorName gives an entity that is credited to a person or the
// organisation but has no plain-text byline the credited name, so JSON-LD
// names the author. A byline already in attributes is left alone: it is what
// the page always showed, and what a rename of the person rewrites.
func (s *Server) applyAuthorName(ctx context.Context, ent *entity.Entity) {
	if ent == nil || s.db == nil {
		return
	}
	var attrs map[string]any
	if json.Unmarshal(ent.Attributes, &attrs) != nil || attrs == nil {
		return
	}
	for _, k := range []string{"author", "author_name"} {
		if v, ok := attrs[k].(string); ok && strings.TrimSpace(v) != "" {
			return
		}
	}
	v := s.bylinesOf(ctx, ent.ID).view(ctx, ent.ID, nil, "", nil)
	if v == nil {
		return
	}
	attrs["author"] = v.Name
	if b, err := json.Marshal(attrs); err == nil {
		ent.Attributes = b
	}
}

// applyJSONLD points the author of an article document at who the entity
// credits: the Person (with its @id and url when the person is public, so the
// byline links to the person's own Person document) or, for the site
// organisation, the Organization by @id. Documents without an author slot (not
// an article) and entities credited to nobody are left as generated.
func (b *bylines) applyJSONLD(ctx context.Context, id uuid.UUID, baseURL string, doc map[string]any) {
	if doc == nil || doc["@graph"] != nil {
		return
	}
	typ, _ := doc["@type"].(string)
	if _, has := doc["author"]; !has && !strings.Contains(typ, "Article") && typ != "BlogPosting" {
		return
	}
	c := b.credits[id]
	switch {
	case c.Org:
		doc["author"] = map[string]any{"@id": baseURL + "#organization"}
	case c.PersonID != nil && b.people[*c.PersonID] != nil:
		p := b.people[*c.PersonID]
		node := map[string]any{"@type": "Person", "name": p.Name}
		if p.Public {
			u := baseURL + b.path(ctx, p.Slug)
			node["@id"], node["url"] = u+"#person", u
		}
		doc["author"] = node
	}
}

// creditedNames are the display names of the entities credited to a person or
// the organisation (the site name), keyed by entity id; entities credited to
// nobody are absent. A feed uses them before the plain-text byline.
func (b *bylines) creditedNames(ids []uuid.UUID) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	for _, id := range ids {
		c := b.credits[id]
		switch {
		case c.Org && b.org != "":
			out[id] = b.org
		case c.PersonID != nil && b.people[*c.PersonID] != nil:
			out[id] = b.people[*c.PersonID].Name
		}
	}
	return out
}
