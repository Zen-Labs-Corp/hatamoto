package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

type partsRig struct {
	*reuseRig
	site    themeSite
	flushed *int
	header  *entity.Entity
	footer  *entity.Entity
}

func init() { areaPages["layoutparts"] = []string{"settings/layoutparts"} }

func newPartsRig(t *testing.T, supported ...string) *partsRig {
	r := &partsRig{reuseRig: newReuseRig(t), flushed: new(int)}
	r.site = themeSite{newFakeSite("Acme Site")}
	r.site.cfg.ThemeExtra = json.RawMessage(`{"colors":{"accent":"#778899"},"builder":{"tokens":{"space":{"4":"1.25rem"}},"presets":{"card":{"label":"Card","s":{"base":{"pt":"space-4"}}}}}}`)
	r.a.deps.Site = r.site
	r.a.deps.Invalidate = func(context.Context) { *r.flushed++ }
	r.a.deps.BuilderParts = func() []string { return supported }
	r.header = r.part(90, "header", `{"v":2,"nodes":[]}`)
	r.footer = r.part(91, "footer", `{"v":2,"nodes":[]}`)
	return r
}

func (r *partsRig) saved(t *testing.T) builder.Design {
	t.Helper()
	d, err := builder.ParseDesign(r.site.cfg.ThemeExtra)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLayoutPartsAssignAndClear(t *testing.T) {
	t.Parallel()

	r := newPartsRig(t, "header", "footer")
	rec := r.do("GET", "/admin/theme/parts", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("form: %d", rec.Code)
	}
	a11yOK(t, body)
	has(t, body, "Create header part", "Create archive part", "Part header", "None: use the theme&#39;s own", `name="part_single"`)
	if strings.Count(body, "Your theme does not use") != 1 || !strings.Contains(body, "Your theme does not use these parts: single page layout, archive layout.") {
		t.Errorf("one notice above the cards must name the two unsupported slots only")
	}
	compareGolden(t, "pages/settings-layout-parts.html", normalizePage(body))

	form := url.Values{"part_header": {r.header.ID.String()}, "part_footer": {r.footer.ID.String()}}
	if rec := r.do("POST", "/admin/theme/parts", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if *r.flushed != 1 {
		t.Errorf("rendered cache purged %d times, want 1", *r.flushed)
	}
	d := r.saved(t)
	if d.Parts["header"] != r.header.ID.String() || d.Parts["footer"] != r.footer.ID.String() || len(d.Parts) != 2 {
		t.Fatalf("parts: %v", d.Parts)
	}
	if d.Tokens.Space["4"] != "1.25rem" || d.Presets["card"].Label != "Card" || !strings.Contains(string(r.site.cfg.ThemeExtra), "#778899") {
		t.Errorf("other keys lost: %s", r.site.cfg.ThemeExtra)
	}
	body = r.do("GET", "/admin/theme/parts", "owner-token", nil, nil).Body.String()
	has(t, body, `value="`+r.header.ID.String()+`" selected`, "/admin/entities/"+r.header.ID.String()+"/edit")

	// Clearing one keeps the other.
	if rec := r.do("POST", "/admin/theme/parts", "owner-token", url.Values{"part_footer": {r.footer.ID.String()}}, nil); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if d := r.saved(t); d.Parts["header"] != "" || d.Parts["footer"] == "" || *r.flushed != 2 {
		t.Errorf("after clear: %v purges=%d", d.Parts, *r.flushed)
	}
}

func TestLayoutPartsRejectsForeignIDsAndSlots(t *testing.T) {
	t.Parallel()

	r := newPartsRig(t, "header")
	before := string(r.site.cfg.ThemeExtra)
	for name, form := range map[string]url.Values{
		"wrong slot":    {"part_header": {r.footer.ID.String()}},
		"a block":       {"part_header": {r.block.ID.String()}},
		"a page":        {"part_header": {r.page.ID.String()}},
		"not an id":     {"part_header": {"nope"}},
		"missing":       {"part_header": {"00000000-0000-0000-0000-00000000dead"}},
		"unknown slot":  {"part_sidebar": {r.header.ID.String()}},
		"mixed, 1 good": {"part_footer": {r.footer.ID.String()}, "part_header": {r.footer.ID.String()}},
	} {
		rec := r.do("POST", "/admin/theme/parts", "owner-token", form, nil)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
			continue
		}
		a11yOK(t, rec.Body.String())
	}
	if string(r.site.cfg.ThemeExtra) != before || *r.flushed != 0 {
		t.Errorf("a refused post stored or purged: %s purges=%d", r.site.cfg.ThemeExtra, *r.flushed)
	}
}

func TestLayoutPartsOwnerOnlyAndCSRF(t *testing.T) {
	t.Parallel()

	r := newPartsRig(t, "header")
	for _, method := range []string{"GET", "POST"} {
		if rec := r.do(method, "/admin/theme/parts", "editor-token", url.Values{"part_header": {""}}, nil); rec.Code != 403 {
			t.Errorf("editor %s: %d", method, rec.Code)
		}
	}
	if rec := r.do("POST", "/admin/theme/parts", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("csrf: %d", rec.Code)
	}
	if *r.flushed != 0 {
		t.Error("purged without an allowed write")
	}
}

func TestLayoutPartsNavTab(t *testing.T) {
	t.Parallel()

	r := newPartsRig(t)
	body := r.do("GET", "/admin/theme/design-system", "owner-token", nil, nil).Body.String()
	has(t, body, `href="/admin/theme/parts"`, "Layout parts")
}

// The entity list serves at most 100 rows per request and turns a larger limit
// into 20, which would hide most parts: the page must never ask for more.
func TestLayoutPartsListStaysWithinThePageLimit(t *testing.T) {
	t.Parallel()

	r := newPartsRig(t, "header", "footer")
	if rec := r.do("GET", "/admin/theme/parts", "owner-token", nil, nil); rec.Code != 200 {
		t.Fatalf("form: %d", rec.Code)
	}
	if r.ents.maxLimit == 0 || r.ents.maxLimit > 100 {
		t.Errorf("the parts list asked for %d rows per page, the entity list serves at most 100", r.ents.maxLimit)
	}
}
