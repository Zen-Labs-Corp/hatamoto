// Package portability writes and reads the full-site archive behind
// `hatamoto export` and `hatamoto import` (format described in docs/EXPORT.md).
package portability

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// FormatName and FormatVersion identify the archive. A reader refuses a newer
// version than it knows; the version only changes when the layout does.
const (
	FormatName    = "hatamoto-export"
	FormatVersion = 1
)

// Archive entry names.
const (
	formatEntry   = "format.json"
	manifestEntry = "manifest.json"
	dataPrefix    = "data/"
	mediaPrefix   = "media/"
	dataSuffix    = ".jsonl"
)

// resetRequiredHash is stored for every imported user: it is not a valid
// password hash, so nobody can sign in until `hatamoto user reset-password`.
const resetRequiredHash = "!reset-required"

// Header is the first entry of the archive.
type Header struct {
	Format            string    `json:"format"`
	Version           int       `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	SchemaVersion     int64     `json:"schema_version"` // newest applied database migration
	IncludeSubmission bool      `json:"include_form_submissions"`
	Tables            []string  `json:"tables"` // order the data files appear in
}

// Manifest is the last entry: what the archive must contain. An import checks
// it, so a truncated or tampered archive fails instead of importing half a site.
type Manifest struct {
	Rows       map[string]int64 `json:"rows"`
	MediaFiles int              `json:"media_files"`
	MediaBytes int64            `json:"media_bytes"`
	// MediaMissing counts referenced files the export did not find on disk. It
	// is informational; an import ignores it.
	MediaMissing int `json:"media_missing,omitempty"`
}

// table describes one exported table.
type table struct {
	name string
	// selfRef is a column that points at the table's own id; import inserts
	// the rows without it and sets it afterwards.
	selfRef string
	// drop lists columns left out of the export.
	drop []string
	// forced is a JSON object of values merged over each row on import.
	forced string
	// pii marks tables left out by --no-submissions.
	pii bool
	// optional tables are skipped when the database does not have them.
	optional bool
}

// tables lists what is exported, parents before children. Not exported, on
// purpose: API keys and sessions (secrets), agent hits, analytics
// and 404 logs (history, not content), the link index (rebuilt on save),
// installed packages (plugins and themes: reinstall them, see docs/EXPORT.md),
// plugin credentials, settings and files, and plugin schemas.
var tables = []table{
	{name: "hatamoto_entity_schemas"},
	{name: "hatamoto_users", drop: []string{"password_hash", "failed_login_attempts", "locked_until"},
		forced: `{"password_hash":"` + resetRequiredHash + `"}`},
	{name: "hatamoto_site_config"},
	// Media and authors come before entities: authors point at an avatar and
	// entities at an author.
	{name: "hatamoto_media"},
	{name: "hatamoto_authors"},
	{name: "hatamoto_entities", selfRef: "parent_id"},
	// People link users to person entities, so after both.
	{name: "hatamoto_user_people"},
	{name: "hatamoto_entity_versions"},
	{name: "hatamoto_terms", selfRef: "parent_id"},
	{name: "hatamoto_entity_terms"},
	{name: "hatamoto_menus"},
	{name: "hatamoto_menu_items", selfRef: "parent_id"},
	{name: "hatamoto_redirects"},
	{name: "hatamoto_forms"},
	{name: "hatamoto_form_submissions", pii: true},
	{name: "hatamoto_section_templates"},
	{name: "hatamoto_internal_link_rules"},
	{name: "hatamoto_agent_robots_policy", optional: true},
	{name: "hatamoto_plugin_grants", optional: true},
}

func tableByName(name string) (table, bool) {
	for _, t := range tables {
		if t.name == name {
			return t, true
		}
	}
	return table{}, false
}

// cleanMediaPath checks a media path from the archive or the database and
// returns it in slash form. It must stay inside the upload directory.
func cleanMediaPath(p string) (string, error) {
	p = strings.ReplaceAll(p, `\`, "/")
	c := path.Clean(p)
	if p == "" || path.IsAbs(c) || c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.ContainsRune(c, 0) {
		return "", fmt.Errorf("unsafe media path %q", p)
	}
	return c, nil
}
