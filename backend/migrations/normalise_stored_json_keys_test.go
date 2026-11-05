package migrations

import (
	"slices"
	"sort"
	"testing"

	"hatamoto.dev/core/internal/jsonx/keynorm"
)

// shapePaths flattens a shape into sorted key paths: "/a" is a key, "[]" an
// array element, "{}" the values of a free-form object.
func shapePaths(prefix string, s *keynorm.Shape) []string {
	if s == nil {
		return nil
	}
	var out []string
	if s.Open {
		out = append(out, prefix+"{}")
	}
	for k, child := range s.Fields {
		out = append(out, prefix+"/"+k)
		out = append(out, shapePaths(prefix+"/"+k, child)...)
	}
	if s.Elem != nil && !s.Open {
		out = append(out, prefix+"[]")
		out = append(out, shapePaths(prefix+"[]", s.Elem)...)
	}
	sort.Strings(out)
	return out
}

// The migration is unshipped-then-applied history: what it normalises is the
// key layout of 2026-11-05, frozen here as literals. The test compares against
// that snapshot, not against the live types, so adding a field to a type later
// never fails it (new keys are written canonically by the v2 encoders).
func TestNormaliseStoredJSONKeysShapesAreFrozen(t *testing.T) {
	want := map[string][]string{
		"hatamoto_site_config.theme_extra": {
			"/borderRadius", "/borderRadius/lg", "/borderRadius/md", "/borderRadius/sm",
			"/builder", "/colors", "/colors/accent", "/colors/background", "/colors/primary",
			"/colors/secondary", "/colors/text", "/headerCta", "/headerCta/text", "/headerCta/url",
			"/media_formats", "/media_formats/avif", "/media_formats/webp", "/media_rebuild",
			"/socialLinks", "/socialLinks{}", "/typography", "/typography/fontFamily", "/typography/headingFamily",
		},
		"hatamoto_entity_schemas.settings": {
			"/aliases", "/archive_crumb_label", "/archive_description", "/archive_path", "/archive_title",
			"/article_type", "/editor", "/faq_from_content", "/locale_neutral", "/overrides",
			"/overrides[]", "/overrides[]/attribute", "/overrides[]/equals", "/overrides[]/sitemap_group",
			"/overrides[]/url_pattern", "/schema_org_type", "/section_pages", "/sitemap",
			"/sitemap/changefreq", "/sitemap/exclude", "/sitemap/priority", "/sitemap_group", "/url_pattern",
		},
		"hatamoto_forms.fields": {
			"[]", "[]/attributes", "[]/attributes{}", "[]/default_value", "[]/help_text", "[]/id", "[]/label",
			"[]/name", "[]/options", "[]/options[]", "[]/options[]/label", "[]/options[]/value",
			"[]/placeholder", "[]/position", "[]/required", "[]/type", "[]/validation",
			"[]/validation/max", "[]/validation/max_length", "[]/validation/message",
			"[]/validation/min", "[]/validation/min_length", "[]/validation/pattern", "[]/width",
		},
		"hatamoto_forms.settings": {
			"/confirmation_email", "/confirmation_message", "/enable_captcha", "/notify_email",
			"/store_submissions", "/submit_button_text", "/success_message", "/success_redirect_url",
		},
	}
	got := map[string][]string{}
	for _, c := range storedKeyColumns() {
		got[c.table+"."+c.column] = shapePaths("", c.shape)
	}
	for key, w := range want {
		slices.Sort(w)
		if !slices.Equal(got[key], w) {
			t.Errorf("%s key layout changed (an applied migration must not be edited):\n got %v\nwant %v", key, got[key], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("migration covers %d columns, snapshot has %d", len(got), len(want))
	}
}

// The migration touches the four typed columns and nothing else.
func TestNormaliseStoredJSONKeysColumns(t *testing.T) {
	var got []string
	for _, c := range storedKeyColumns() {
		got = append(got, c.table+"."+c.column)
	}
	slices.Sort(got)
	want := []string{"hatamoto_entity_schemas.settings", "hatamoto_forms.fields", "hatamoto_forms.settings", "hatamoto_site_config.theme_extra"}
	if !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
}
