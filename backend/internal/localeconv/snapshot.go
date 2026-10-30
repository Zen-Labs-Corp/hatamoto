package localeconv

import (
	"encoding/json"
	"time"

	"hatamoto.dev/core/internal/entity"
)

// Snapshot is the data the analysis reads: every non-variant entity, the
// schemas, the locale settings and the redirect sources. The analysis never
// touches the database; the store loads a Snapshot.
type Snapshot struct {
	Entities []Ent
	Schemas  []Schema
	Site     SiteState
	// RedirectSources are the source paths of the redirect rules.
	RedirectSources []string
}

// Ent is one entity row as the conversion sees it. IDs are uuid strings;
// ParentID and Group are empty when unset.
type Ent struct {
	ID          string
	Schema      string // schema slug
	Slug        string
	ParentID    string
	Locale      string
	Status      string
	Group       string // current translation_group
	Nested      bool   // slug is unique per parent (the nested unique index applies)
	Attrs       json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt *time.Time

	flat map[string]string
}

// Flat returns the top-level scalar attributes as strings.
func (e *Ent) Flat() map[string]string {
	if e.flat == nil {
		e.flat = entity.FlattenAttributes(e.Attrs)
		if e.flat == nil {
			e.flat = map[string]string{}
		}
	}
	return e.flat
}

func (e *Ent) published() bool { return e.Status == string(entity.StatusPublished) }

// Schema is the part of an entity schema the conversion reads and may edit.
type Schema struct {
	ID       string
	Slug     string
	Name     string
	Settings json.RawMessage
}

// SiteLocale is one stored public locale (the JSON shape of site_config.locales).
type SiteLocale struct {
	Tag     string `json:"tag"`
	Segment string `json:"segment"`
}

// SiteState is the locale-related site configuration.
type SiteState struct {
	Language  string
	Locales   []SiteLocale
	PrefixAll bool
	XDefault  string
	// HomepageID is the explicit homepage setting ("" when unset). The server
	// serves it for every locale before looking for a locale home, so the
	// conversion clears it.
	HomepageID string
	// TriggerGuardMissing is true when entity_locale_retag() lacks the
	// "two or more locales" guard (the site has not run the multi-locale migration).
	TriggerGuardMissing bool
}
