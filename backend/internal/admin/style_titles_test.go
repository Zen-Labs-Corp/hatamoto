package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func TestEveryStyleKeyHasTitle(t *testing.T) {
	t.Parallel()

	inTable := map[string]bool{}
	for _, e := range builder.StyleSpecTable() {
		inTable[e.Key] = true
		if styleKeyTitles[e.Key] == "" {
			t.Errorf("style key %s has no title", e.Key)
		}
	}
	for k := range styleKeyTitles {
		if !inTable[k] {
			t.Errorf("title for %s, which is not a style key", k)
		}
	}
	if len(styleKeyList) != len(styleKeyTitles) {
		t.Errorf("styleKeyList has %d entries for %d titles (duplicate key?)", len(styleKeyList), len(styleKeyTitles))
	}
}

func TestDiffLabelsNameEveryStyleKey(t *testing.T) {
	t.Parallel()

	for _, e := range builder.StyleSpecTable() {
		got := fieldDetails(builder.NodeChange{Styles: []builder.FieldChange{{Key: e.Key, BP: "sm", Kind: builder.DiffAdded, New: "x"}}})
		want := "style " + styleKeyTitles[e.Key] + " (Phone): set to “x”"
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: %q, want %q", e.Key, got, want)
		}
	}
	got := fieldDetails(builder.NodeChange{
		Styles: []builder.FieldChange{{Key: "ml", BP: "sm", Kind: builder.DiffAdded, New: "auto"}, {Key: "pt", BP: "base", Kind: builder.DiffChanged, Old: "space-1", New: "space-2"}, {Key: "gone", BP: "md", Kind: builder.DiffRemoved, Old: "1"}},
		Props:  []builder.FieldChange{{Key: "text", Kind: builder.DiffChanged, Old: "a", New: "b"}},
	})
	want := []string{"text: “a” → “b”", "style Margin left (Phone): set to “auto”", "style Padding top (Desktop): “space-1” → “space-2”", "style gone (Tablet): cleared (was “1”)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("details %q, want %q", got, want)
	}
}

func TestPresetsSaveKeepsKeysThePageDidNotList(t *testing.T) {
	t.Parallel()

	g, site, _ := presetsRig(t)
	// card sets pt, bg: add a v2 key through the add row, then save a form that does not mention it.
	add := url.Values{"label.card": {"Card"}, "add.card.key": {"ml"}, "add.card.base": {"auto"}, "add.card.sm": {"-1rem"}}
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", add, nil); rec.Code != 303 {
		t.Fatalf("add row: %d %s", rec.Code, rec.Body.String())
	}
	p := design(t, site).Presets["card"]
	if p.S["base"]["ml"] != "auto" || p.S["sm"]["ml"] != "-1rem" || p.S["base"]["pt"] != "space-4" {
		t.Fatalf("after add row: %+v", p)
	}
	rec := g.do("GET", "/admin/theme/presets", "owner-token", nil, nil)
	for _, want := range []string{`name="s.card.base.ml" value="auto"`, `name="s.card.sm.ml" value="-1rem"`, `name="s.card.md.ml"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(rec.Body.String(), `name="s.card.base.shadow"`) {
		t.Error("an unset key is listed")
	}
	// A save that never mentions ml (an older form, or a client posting a few cells) keeps it.
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", url.Values{"label.card": {"Card"}, "s.card.base.pt": {"space-5"}}, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	p = design(t, site).Presets["card"]
	if p.S["base"]["ml"] != "auto" || p.S["sm"]["ml"] != "-1rem" || p.S["base"]["pt"] != "space-5" || p.S["base"]["bg"] != "surface" {
		t.Fatalf("save dropped a key: %+v", p)
	}
	// An empty posted cell clears just that cell.
	if rec := g.do("POST", "/admin/theme/presets", "owner-token", url.Values{"label.card": {"Card"}, "s.card.sm.ml": {""}}, nil); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	p = design(t, site).Presets["card"]
	if p.S["sm"]["ml"] != "" || p.S["base"]["ml"] != "auto" {
		t.Fatalf("clear: %+v", p)
	}
}

func TestPresetsAddRowValidates(t *testing.T) {
	t.Parallel()

	g, site, flushed := presetsRig(t)
	before := string(site.cfg.ThemeExtra)
	for what, f := range map[string]url.Values{
		"bad value":    {"label.card": {"Card"}, "add.card.key": {"bgimg"}, "add.card.base": {"/media/../x.jpg"}},
		"bad pos":      {"label.card": {"Card"}, "add.card.key": {"pos"}, "add.card.sm": {"fixed"}},
		"unknown key":  {"label.card": {"Card"}, "add.card.key": {"nope"}, "add.card.base": {"1"}},
		"no value":     {"label.card": {"Card"}, "add.card.key": {"ml"}},
		"unsigned neg": {"label.card": {"Card"}, "add.card.key": {"pt"}, "add.card.base": {"-1rem"}},
	} {
		rec := g.do("POST", "/admin/theme/presets", "owner-token", f, nil)
		if rec.Code != 422 || strings.Count(rec.Body.String(), "hm-field__error") != 1 {
			t.Errorf("%s: %d errors=%d", what, rec.Code, strings.Count(rec.Body.String(), "hm-field__error"))
		}
		a11yOK(t, rec.Body.String())
	}
	if string(site.cfg.ThemeExtra) != before || *flushed != 0 {
		t.Error("a refused add row changed state")
	}
}

func TestPresetsPageSizeListsOnlySetKeys(t *testing.T) {
	t.Parallel()

	g, site, _ := presetsRig(t)
	rec := g.do("GET", "/admin/theme/presets", "owner-token", nil, nil)
	body := rec.Body.String()
	// The shell and the add-preset form are the page's floor: measure it with no preset.
	site.cfg.ThemeExtra = []byte(`{}`)
	floor := len(g.do("GET", "/admin/theme/presets", "owner-token", nil, nil).Body.String())
	t.Logf("presets page: %d bytes, %d without presets", len(body), floor)
	if grew := len(body) - floor; grew > 12<<10 {
		t.Errorf("a preset with two keys adds %d bytes; listing every key would add over 100 KiB", grew)
	}
	if n := strings.Count(body, `name="s.card.`); n != 6 {
		t.Errorf("%d value fields for 2 keys x 3 breakpoints", n)
	}
	if !strings.Contains(body, `name="add.card.key"`) || !strings.Contains(body, `<option value="ml">Margin left</option>`) {
		t.Error("add row missing or does not offer unset keys")
	}
	if strings.Contains(body, `name="add.card.key"`) {
		sel := body[strings.Index(body, `name="add.card.key"`):]
		sel = sel[:strings.Index(sel, "</select>")]
		if strings.Contains(sel, `value="pt"`) || strings.Contains(sel, `value="bg"`) {
			t.Error("add row offers a key that is already set")
		}
	}
}

func TestBreakpointTitleNamesStates(t *testing.T) {
	t.Parallel()

	for k, want := range map[string]string{
		"base": "Desktop", "md": "Tablet", "sm": "Phone",
		"base:hover": "Hover, Desktop", "md:focus": "Focus, Tablet", "sm:active": "Pressed, Phone", "sm:parent-hover": "Parent hover, Phone",
		"xl": "xl", "base:visited": "base:visited",
	} {
		if got := breakpointTitle(k); got != want {
			t.Errorf("breakpointTitle(%q) = %q, want %q", k, got, want)
		}
	}
	got := fieldDetails(builder.NodeChange{Styles: []builder.FieldChange{{Key: "shadow", BP: "md:hover", Kind: builder.DiffAdded, New: "lg"}}})
	if len(got) != 1 || got[0] != "style Shadow (Hover, Tablet): set to “lg”" {
		t.Errorf("details = %q", got)
	}
}
