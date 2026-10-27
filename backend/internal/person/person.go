// Package person is the read model of people: every human and every byline is
// an entity of the builtin `person` content type.
//
// A person is a normal entity (name, slug, attributes) so it gets a URL, a
// sitemap entry and revisions like any other content. Entities credit one
// through hatamoto_entities.byline_person_id (or byline_org for the site
// organisation), and a user account owns at most one person through
// hatamoto_user_people. Nothing here reads or exposes a user's email: only the
// person's own `public_email` attribute is ever public.
//
// hatamoto_authors is kept only so a rollback still renders bylines; it is not
// the source of bylines any more (see internal/author).
package person

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// SchemaSlug is the slug of the builtin person content type.
const SchemaSlug = "person"

// Person is one profile.
type Person struct {
	ID   uuid.UUID
	Slug string
	Name string

	JobTitle    string
	ShortBio    string
	Bio         string
	Photo       string // URL of the photo, "" without one
	PublicEmail string
	// SlugFixed is true once the person has been public: the address then stays
	// when the name changes.
	SlugFixed bool
	// Public is the opt-in: only a public person has a page, a sitemap entry
	// and a linked byline.
	Public bool
	// OnTeam marks people the site lists as its team (the on_team attribute).
	OnTeam bool
	// Links holds the profile links by key: website, linkedin, x, github.
	Links map[string]string
	// Expertise are free-form topic labels.
	Expertise []string
}

// linkLabels are the visible names of the profile links.
var linkLabels = map[string]string{"website": "Website", "linkedin": "LinkedIn", "x": "X", "github": "GitHub"}

// LabelledLinks returns the profile links with a visible label, in the order
// of SameAs.
func (p *Person) LabelledLinks() []Link {
	var out []Link
	for _, k := range linkKeys {
		if v := p.Links[k]; v != "" {
			out = append(out, Link{Label: linkLabels[k], URL: v})
		}
	}
	return out
}

// Link is a profile link and the name shown for it.
type Link struct{ Label, URL string }

// SameAs returns the profile links in a stable order (website, LinkedIn, ...).
func (p *Person) SameAs() []string {
	var out []string
	for _, k := range linkKeys {
		if v := p.Links[k]; v != "" {
			out = append(out, v)
		}
	}
	return out
}

// linkKeys are the attributes copied into Person.Links.
var linkKeys = []string{"website", "linkedin", "x", "github"}

// fromAttributes builds a Person from an entity row's columns.
func fromAttributes(id uuid.UUID, slug, name string, raw []byte) *Person {
	p := &Person{ID: id, Slug: slug, Name: name, Links: map[string]string{}}
	var attrs map[string]any
	_ = json.Unmarshal(raw, &attrs)
	str := func(k string) string {
		s, _ := attrs[k].(string)
		return strings.TrimSpace(s)
	}
	p.JobTitle = str("job_title")
	p.ShortBio = str("short_bio")
	p.Bio = str("bio")
	p.Photo = str("photo")
	p.PublicEmail = str("public_email")
	p.Public, _ = attrs["public"].(bool)
	p.OnTeam, _ = attrs["on_team"].(bool)
	for _, k := range linkKeys {
		if v := str(k); v != "" {
			p.Links[k] = v
		}
	}
	if list, ok := attrs["expertise"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				p.Expertise = append(p.Expertise, strings.TrimSpace(s))
			}
		}
	}
	return p
}
