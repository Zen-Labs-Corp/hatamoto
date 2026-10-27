package wpimport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/person"
)

// SiteMode says how the importer treats the target site.
type SiteMode string

const (
	// ModeEmpty: the target has no published content besides the starter
	// content of the setup wizard. The import sets the post and page URL
	// patterns to the WordPress structure, the site settings and the home page.
	ModeEmpty SiteMode = "empty"
	// ModeExisting: the target has content of its own. The import never
	// changes URL patterns, site settings or the home page: imported items
	// take the patterns that exist, old URLs get redirects, and what cannot be
	// resolved is reported.
	ModeExisting SiteMode = "existing"
)

// SiteState is what the target site holds before the import.
type SiteState struct {
	Mode SiteMode `json:"mode"`
	// Existing counts published entities that are neither starter content nor
	// imported from this source.
	Existing int `json:"existing"`
	// Starter counts untouched starter and first-boot entities (ignored).
	Starter int `json:"starter"`
	// Examples are a few slugs of the existing content.
	Examples []string `json:"examples,omitempty"`
}

// InspectSite decides the mode. Entities imported from this source (key
// prefix) do not count, so a re-run of an import into an empty site stays in
// the mode of the first run.
func InspectSite(ctx context.Context, pool *pgxpool.Pool, keyPrefix string) (*SiteState, error) {
	// People are not site content: every user has one (the admin asks for a
	// name and photo at first sign-in), so counting them made a fresh site
	// look like a site with content.
	rows, err := pool.Query(ctx, `
		SELECT e.slug, e.attributes FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE e.status = 'published' AND e.variant_key IS NULL AND s.slug <> $2
		  AND (e.metadata->>'import_key' IS NULL OR e.metadata->>'import_key' NOT LIKE $1)
		ORDER BY e.created_at`, escapeLike(keyPrefix)+"%", person.SchemaSlug)
	if err != nil {
		return nil, fmt.Errorf("inspect target site: %w", err)
	}
	defer rows.Close()
	st := &SiteState{Mode: ModeEmpty}
	for rows.Next() {
		var slug string
		var attrs json.RawMessage
		if err := rows.Scan(&slug, &attrs); err != nil {
			return nil, err
		}
		if isStarterContent(attrs) {
			st.Starter++
			continue
		}
		st.Existing++
		if len(st.Examples) < 5 {
			st.Examples = append(st.Examples, slug)
		}
	}
	if st.Existing > 0 {
		st.Mode = ModeExisting
	}
	return st, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// isStarterContent recognises what the setup wizard (admin/starter) leaves
// behind: entities carrying "starter": true, and the untouched first-boot home
// page, a single "welcome" section (legacy page_sections or the only component
// of a page_doc).
func isStarterContent(attrs json.RawMessage) bool {
	var a struct {
		Starter  bool `json:"starter"`
		Sections []struct {
			ID string `json:"id"`
		} `json:"page_sections"`
		Doc json.RawMessage `json:"page_doc"`
	}
	if json.Unmarshal(attrs, &a) != nil {
		return false
	}
	if a.Starter {
		return true
	}
	if len(a.Doc) > 0 {
		d, err := builder.Parse(a.Doc)
		if err != nil {
			return false
		}
		cs := builder.Components(d)
		return len(cs) == 1 && cs[0].ID == "welcome"
	}
	return len(a.Sections) == 1 && a.Sections[0].ID == "welcome"
}

// siteKey is the identity of a source site: lower-case host and path, so two
// WordPress sub-sites on one host (example.com/a and example.com/b) differ.
// The entity keys and the id map are both scoped by it.
func siteKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	return strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/")
}

// ApplySiteState records the mode on the scan report.
func (r *Report) ApplySiteState(s *SiteState) {
	if s == nil {
		return
	}
	r.SiteMode, r.ExistingContent = s.Mode, s.Existing
	if s.Mode == ModeExisting {
		r.ModeNote = fmt.Sprintf("the target site already has %d published items (%s): its URL patterns, site settings and home page are kept; imported items take the existing patterns, old URLs get redirects where no live page answers, and differences are reported",
			s.Existing, strings.Join(s.Examples, ", "))
	} else {
		r.ModeNote = "the target site is empty (starter content only): the WordPress permalink structure, site settings and home page are applied"
	}
}

// KeyPrefix is the prefix of the import keys of one source site.
func KeyPrefix(siteURL string) string { return "wp:" + siteKey(siteURL) + ":" }
