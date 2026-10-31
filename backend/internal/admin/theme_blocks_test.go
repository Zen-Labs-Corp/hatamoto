package admin

import (
	"hatamoto.dev/core/internal/entity"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func init() { areaPages["blocks"] = []string{"blocks/list"} }

func TestBlocksPageListsBlocksWithUsage(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	r.page.Attributes = []byte(`{"page_doc":` + refDoc(r.block.ID.String()) + `}`)
	for _, tok := range []string{"owner-token", "editor-token"} {
		rec := r.do("GET", "/admin/theme/blocks", tok, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", tok, rec.Code)
		}
		body := rec.Body.String()
		has(t, body, "<h1>Blocks</h1>", "Footer CTA", `href="/admin/entities/`+r.block.ID.String()+`/edit"`, "Used by",
			`<nav class="hm-subnav" aria-label="Theme">`, `<a class="hm-subnav__link" href="/admin/theme/blocks" aria-current="page">`,
			// New block is a POST to the draft route, never a link.
			`<form method="post" action="/admin/entities/drafts">`, `name="type" value="block"`, "New block")
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", tok, issues)
		}
	}
	body := r.do("GET", "/admin/theme/blocks", "owner-token", nil, nil).Body.String()
	row := body[strings.Index(body, "Footer CTA"):]
	if !regexp.MustCompile(`(?s)Footer CTA.*?<td[^>]*>\s*1\s*</td>`).MatchString(row) {
		t.Errorf("the used-by count of a block placed on one page is not 1: %.600s", row)
	}
	if body := r.do("GET", "/admin/theme/blocks?q=nothing-like-it", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "No blocks yet") || strings.Contains(body, "Footer CTA") {
		t.Errorf("search: %.300s", body)
	}
}

func TestBlocksPageOffWhenTheSlugIsASiteType(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	r.blockSc.IsBuiltin = false
	body := r.do("GET", "/admin/theme/blocks", "owner-token", nil, nil).Body.String()
	has(t, body, "Blocks are off", "Rename that type")
	lacks(t, body, "New block", "Footer CTA")
}

func TestBlockDeleteFromTheBlocksPageReturnsThere(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	u := "/admin/entities/" + r.block.ID.String() + "/delete"
	conf := r.do("GET", u+"?back="+url.QueryEscape("/admin/theme/blocks"), "owner-token", nil, nil).Body.String()
	has(t, conf, `name="back" value="/admin/theme/blocks"`)
	rec := r.do("POST", u, "owner-token", url.Values{"back": {"/admin/theme/blocks"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/theme/blocks" {
		t.Errorf("delete: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Any other back target is still limited to the entity list.
	if got := backTo(httptest.NewRequest("GET", "/x?back="+url.QueryEscape("https://evil.example/"), nil), "/admin/entities", "/admin/entities"); got != "/admin/entities" {
		t.Errorf("back %q", got)
	}
}

func TestBlockAndPartEditorsGoBackToTheirThemePage(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	body := r.get(t, "owner-token", r.url(r.block, "/edit"), "")
	has(t, body, `class="hm-editor-bar__back" href="/admin/theme/blocks"`)
	part := r.part(81, "header", `{"v":2,"nodes":[]}`)
	body = r.get(t, "owner-token", r.url(part, "/edit"), "")
	has(t, body, `class="hm-editor-bar__back" href="/admin/theme/parts"`)
}

// Blocks and layout parts are managed under Theme: the built-in types are
// absent from the Entities list, its counts, type tabs, Create menu, New chooser
// and parent picker, yet their edit URLs keep working.
func TestEntitiesLeaveOutTheBuiltInReuseTypes(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	part := r.part(81, "header", `{"v":2,"nodes":[]}`)
	list := r.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String()
	lacks(t, list, "Footer CTA", part.Name, "type=block", "type=theme_part", ">Block<", ">Theme part<")
	has(t, list, `<legend class="hm-visually-hidden">Status</legend>`)
	// The same filter by type slug does not bring them back.
	lacks(t, r.do("GET", "/admin/entities?type=block", "owner-token", nil, nil).Body.String(), "Footer CTA")
	if r.ents.lastList == nil || len(r.ents.lastList.ExcludeSchemaIDs) != 2 {
		t.Errorf("list filter excludes %v, want the block and theme part types", r.ents.lastList)
	}
	// New: no chooser entry, and ?type= falls back to the chooser.
	for _, p := range []string{"/admin/entities/new", "/admin/entities/new?type=block", "/admin/entities/new?type=theme_part"} {
		body := r.do("GET", p, "owner-token", nil, nil).Body.String()
		lacks(t, body, "Reusable blocks", "type=block", "type=theme_part", "Start a new block")
	}
	// The parent picker does not offer them.
	lacks(t, r.do("GET", "/admin/entities/parent-options?q=Footer", "owner-token", nil, nil).Body.String(), "Footer CTA")
	// Direct edit URLs still work.
	for _, e := range []*entity.Entity{r.block, part} {
		if rec := r.do("GET", r.url(e, "/edit"), "owner-token", nil, nil); rec.Code != http.StatusOK {
			t.Errorf("edit %s: %d", e.Name, rec.Code)
		}
	}
}

// A site's own type that took the slug "block" is an ordinary type: it stays in
// Entities, with its tab, entities and New entry.
func TestEntitiesKeepASiteOwnedBlockType(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	r.blockSc.IsBuiltin = false
	list := r.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String()
	has(t, list, "Footer CTA", "type=block")
	if r.ents.lastList == nil || len(r.ents.lastList.ExcludeSchemaIDs) != 1 {
		t.Errorf("list filter excludes %v, want only the built-in theme part type", r.ents.lastList)
	}
	has(t, r.do("GET", "/admin/entities?type=block", "owner-token", nil, nil).Body.String(), "Footer CTA")
	has(t, r.do("GET", "/admin/entities/new", "owner-token", nil, nil).Body.String(), "type=block")
}
