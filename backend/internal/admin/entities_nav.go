package admin

import (
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/person"
)

// EntityNavEntry is one link of the Entities subnav. ID is the entity type's
// slug and the no-JS select's option value: empty for "All entities" (never a
// valid slug, so it selects the unfiltered list).
type EntityNavEntry struct {
	ID, Href, Label string
	Count           int
	Current         bool
}

// entitiesNavIn builds the subnav under prefix from the counts the list already
// loaded (schemas arrive ordered by name). active is the resolved type slug, ""
// for the unfiltered list and for an unknown ?type=.
func entitiesNavIn(prefix string, types []typeTab, active string) []EntityNavEntry {
	total := 0
	for _, t := range types {
		total += t.Count
	}
	out := []EntityNavEntry{{ID: "", Href: urlUnder(prefix, "entities"), Label: "All entities", Count: total, Current: active == ""}}
	for _, t := range types {
		// Empty types stay in the Create menu and Manage entity types, not in the subnav. With no
		// counts at all (a failed count, a fresh site) every type is shown.
		if t.Count == 0 && total > 0 && active != t.Slug {
			continue
		}
		href := urlUnder(prefix, "entities") + "?type=" + url.QueryEscape(t.Slug)
		if t.Slug == person.SchemaSlug {
			href = urlUnder(prefix, "people") // People has its own list; the generic one has no author columns
		}
		out = append(out, EntityNavEntry{ID: t.Slug, Href: href, Label: typePluralFor(t.Slug, t.Name), Count: t.Count, Current: active == t.Slug})
	}
	return out
}

// EntitiesNav is the Entities subnav the shell renders on the entity list (nil
// on every other page).
func (v *View) EntitiesNav() []EntityNavEntry { return v.entityNav }

// EntitiesNavManage reports whether the subnav links to the entity types
// settings page (owners only; editors cannot open it).
func (v *View) EntitiesNavManage() bool { return v.entityManage }

// typePluralFor is typePlural with the irregular person type spelled "People".
func typePluralFor(slug, name string) string {
	if slug == person.SchemaSlug {
		return "People"
	}
	return typePlural(name)
}

// typePlural derives a type's plural title from its name; the schema has no
// plural field. It covers s, x, ch, sh and consonant+y; irregular names
// ("Person") come out regular.
func typePlural(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return name + "es"
	case len(name) > 1 && strings.HasSuffix(lower, "y") && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2])):
		return name[:len(name)-1] + "ies"
	}
	return name + "s"
}
