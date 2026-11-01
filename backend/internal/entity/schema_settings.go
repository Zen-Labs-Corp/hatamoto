package entity

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/urlpattern"
)

// SchemaURLOverride swaps the URL pattern (and sitemap group) for entities
// whose attribute `Attribute` equals `Equals`.
type SchemaURLOverride struct {
	Attribute    string `json:"attribute"`
	Equals       string `json:"equals"`
	URLPattern   string `json:"url_pattern"`
	SitemapGroup string `json:"sitemap_group,omitempty"`
}

// SchemaSitemapSettings tunes sitemap output for a schema.
type SchemaSitemapSettings struct {
	Priority   *float64 `json:"priority,omitempty"`
	ChangeFreq string   `json:"changefreq,omitempty"`
	Exclude    bool     `json:"exclude,omitempty"`
}

// SchemaURLSettings is the typed view of EntitySchema.Settings. All keys are
// optional; unknown keys are ignored so a site may carry extra data.
type SchemaURLSettings struct {
	URLPattern   string                `json:"url_pattern,omitempty"`
	ArchivePath  string                `json:"archive_path,omitempty"`
	SitemapGroup string                `json:"sitemap_group,omitempty"`
	Aliases      []string              `json:"aliases,omitempty"`
	Overrides    []SchemaURLOverride   `json:"overrides,omitempty"`
	Sitemap      SchemaSitemapSettings `json:"sitemap"`
	SectionPages bool                  `json:"section_pages,omitempty"`
	// ArchiveTitle and ArchiveDescription override the archive page heading
	// and meta description (see MaxArchiveTitleLen / MaxArchiveDescriptionLen).
	ArchiveTitle       string `json:"archive_title,omitempty"`
	ArchiveDescription string `json:"archive_description,omitempty"`
	// ArchiveCrumbLabel is the section label in an entity's breadcrumb; it
	// falls back to ArchiveTitle, then the schema name.
	ArchiveCrumbLabel string `json:"archive_crumb_label,omitempty"`
	// SchemaOrgType is the JSON-LD @type for entities of schemas that have no
	// x-schema-org annotations and no plugin builder. Must be one of
	// AllowedSchemaOrgTypes (checked on write only). Types that require
	// fields the generic document cannot supply (FAQPage, Product, Event) are
	// excluded; use annotations or a plugin builder for those.
	SchemaOrgType string `json:"schema_org_type,omitempty"`
	// ArticleType is the @type of Article-family documents of this schema
	// (one of AllowedArticleTypes, checked on write only).
	ArticleType string `json:"article_type,omitempty"`
	// FAQFromContent turns FAQPage extraction from the entry body on or off;
	// absent means on.
	FAQFromContent *bool `json:"faq_from_content,omitempty"`
	// Editor selects the admin editing screen for the type: "" is the generic
	// form, EditorPost is the post (TipTap) editor used by blog posts.
	// Any other value, including a non-string, reads as "" (checked on write only).
	Editor EditorSetting `json:"editor,omitempty"`
}

// EditorSetting is settings.editor as stored. It decodes leniently: a value
// that is not a JSON string reads as "", so a stale or hand-edited setting can
// never make URLSettings fail and reset the schema's URL routing.
type EditorSetting string

// UnmarshalJSON implements json.Unmarshaler.
func (e *EditorSetting) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil {
		s = ""
	}
	*e = EditorSetting(s)
	return nil
}

// EditorPost is the settings.editor value that opts a type into the post editor.
const EditorPost = "post"

// AllowedSchemaOrgTypes is the allowlist for settings.schema_org_type.
var AllowedSchemaOrgTypes = []string{
	"Article", "BlogPosting", "NewsArticle", "DefinedTerm", "Person", "Service",
	"Course", "HowTo", "WebPage", "CreativeWork", "Organization", "LocalBusiness",
}

// IsAllowedSchemaOrgType reports whether t is in AllowedSchemaOrgTypes.
func IsAllowedSchemaOrgType(t string) bool {
	return slices.Contains(AllowedSchemaOrgTypes, t)
}

// AllowedArticleTypes is the allowlist for settings.article_type.
var AllowedArticleTypes = []string{"Article", "BlogPosting", "NewsArticle"}

// FAQEnabled reports whether FAQPage extraction from the body is on (the default).
func (st SchemaURLSettings) FAQEnabled() bool {
	return st.FAQFromContent == nil || *st.FAQFromContent
}

// Length limits (in characters, after trimming) for the archive settings.
const (
	MaxArchiveTitleLen       = 200
	MaxArchiveDescriptionLen = 500
)

// SettingsError is a schema settings value that is not acceptable. Field names
// the setting at fault ("url_pattern", "archive_path", "aliases", ...; empty
// when the settings as a whole are malformed), so callers need not parse the
// message.
type SettingsError struct{ Field, Msg string }

func (e *SettingsError) Error() string { return ErrInvalidSchema.Error() + ": " + e.Msg }
func (e *SettingsError) Unwrap() error { return ErrInvalidSchema }

func settingsErr(field, format string, args ...any) error {
	return &SettingsError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// URLSettings decodes Settings. An empty or null value yields zero settings.
func (s *EntitySchema) URLSettings() (SchemaURLSettings, error) {
	var out SchemaURLSettings
	if s == nil || len(s.Settings) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(s.Settings, &out); err != nil {
		return SchemaURLSettings{}, fmt.Errorf("%w: settings: %v", ErrInvalidSchema, err)
	}
	return out, nil
}

// ValidateSchemaSettings checks the structural rules for schema settings:
// the value must be a JSON object, patterns must start with "/" and contain
// {slug} (see urlpattern.Parse), and archive_path must start with "/".
func ValidateSchemaSettings(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return settingsErr("", "settings must be a JSON object")
	}
	var st SchemaURLSettings
	if err := json.Unmarshal(raw, &st); err != nil {
		return settingsErr("", "settings: %v", err)
	}
	if st.URLPattern != "" {
		if err := validateURLPatternString(st.URLPattern); err != nil {
			return settingsErr("url_pattern", "settings.url_pattern: %v", err)
		}
	}
	for i, o := range st.Overrides {
		if o.Attribute == "" {
			return settingsErr("overrides", "settings.overrides[%d].attribute is required", i)
		}
		if err := validateURLPatternString(o.URLPattern); err != nil {
			return settingsErr("overrides", "settings.overrides[%d].url_pattern: %v", i, err)
		}
	}
	if st.ArchivePath != "" && !strings.HasPrefix(st.ArchivePath, "/") {
		return settingsErr("archive_path", "settings.archive_path must start with '/'")
	}
	for i, a := range st.Aliases {
		if strings.TrimSpace(a) == "" {
			return settingsErr("aliases", "settings.aliases[%d] is empty", i)
		}
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(st.ArchiveTitle)); n > MaxArchiveTitleLen {
		return settingsErr("archive_title", "settings.archive_title must be at most %d characters", MaxArchiveTitleLen)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(st.ArchiveCrumbLabel)); n > MaxArchiveTitleLen {
		return settingsErr("archive_crumb_label", "settings.archive_crumb_label must be at most %d characters", MaxArchiveTitleLen)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(st.ArchiveDescription)); n > MaxArchiveDescriptionLen {
		return settingsErr("archive_description", "settings.archive_description must be at most %d characters", MaxArchiveDescriptionLen)
	}
	return nil
}

// ValidateSchemaSettingsForWrite is ValidateSchemaSettings plus the checks
// that apply only when settings are saved. schema_org_type is validated here
// and not on load: a stale or hand-edited value must only disable JSON-LD
// typing (the generator re-checks the allowlist), never reset the schema's
// URL routing. The same holds for editor, which is also checked only here.
func ValidateSchemaSettingsForWrite(raw json.RawMessage) error {
	if err := ValidateSchemaSettings(raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var st SchemaURLSettings
	if err := json.Unmarshal(raw, &st); err != nil {
		return settingsErr("", "settings: %v", err)
	}
	if st.SchemaOrgType != "" && !IsAllowedSchemaOrgType(st.SchemaOrgType) {
		return settingsErr("schema_org_type", "settings.schema_org_type %q is not one of %s", st.SchemaOrgType, strings.Join(AllowedSchemaOrgTypes, ", "))
	}
	if st.ArticleType != "" && !slices.Contains(AllowedArticleTypes, st.ArticleType) {
		return settingsErr("article_type", "settings.article_type %q is not one of %s", st.ArticleType, strings.Join(AllowedArticleTypes, ", "))
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	if v, ok := obj["editor"]; ok {
		var ed string
		if json.Unmarshal(v, &ed) != nil || (ed != "" && ed != EditorPost) {
			return settingsErr("editor", "settings.editor must be empty or %q", EditorPost)
		}
	}
	return nil
}

// ValidateEditorSetting is the write-time check of settings.editor against the
// schema it applies to: the post editor writes attributes.content as a string,
// so the schema must declare one, and the person schema has its own form.
func ValidateEditorSetting(slug string, schema, settings json.RawMessage) error {
	var st SchemaURLSettings
	if len(settings) == 0 || json.Unmarshal(settings, &st) != nil || st.Editor != EditorPost {
		return nil
	}
	if slug == PersonSchemaSlug {
		return settingsErr("editor", "settings.editor %q cannot be used for the person type, which has its own form", EditorPost)
	}
	var decl struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &decl)
	if decl.Properties["content"].Type != "string" {
		return settingsErr("editor", "settings.editor %q needs the schema to declare a string property named content", EditorPost)
	}
	return nil
}

func validateURLPatternString(p string) error {
	_, err := urlpattern.Parse(p)
	return err
}
