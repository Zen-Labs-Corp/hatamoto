//go:build integration

package server

import (
	"net/url"
	"strings"
	"testing"
)

func textDoc(id, text string) map[string]any {
	return map[string]any{"v": 2, "nodes": []any{map[string]any{"id": id, "t": "text", "p": map[string]any{"text": text}}}}
}

// A4/ISC-18/21: an opted-in theme draws the assigned header and footer in place
// of its own, assigning purges the cached page, only the owner may assign, and
// clearing the assignment gives the theme's own output back byte for byte.
func TestReusePartAssignment_Integration(t *testing.T) {
	r := newReuseIT(t)
	if err := r.srv.renderEngine.SetTheme(themeFixture(t, "theme_parts.json")); err != nil {
		t.Fatal(err)
	}
	header := r.mk(r.partSc, "Site header", map[string]any{"slot": "header", "page_doc": textDoc("h", "B6-HEADER-PART")})
	footer := r.mk(r.partSc, "Site footer", map[string]any{"slot": "footer", "page_doc": textDoc("f", "B6-FOOTER-PART")})
	page := r.mk(r.pageSc, "Parts page", map[string]any{"page_doc": headingDoc("h", "PARTS-BODY")})
	r.publish(page)
	path := "/" + page.Slug

	assign := func(role string, form url.Values) int { return r.do(role, "POST", "/admin/theme/parts", form).Code }
	// Start from no assignment, whatever an earlier run left behind.
	if code := assign("owner", url.Values{}); code != 303 {
		t.Fatalf("clear: %d", code)
	}
	r.eventually("the page is cached", func() bool { r.get(path); return r.cached(path) })
	plain := r.get(path).Body.String()
	if strings.Contains(plain, "B6-HEADER-PART") || !strings.Contains(plain, "PARTS-BODY") {
		t.Fatalf("unassigned page:\n%.500s", plain)
	}

	if code := assign("editor", url.Values{"part_header": {header.ID.String()}}); code != 403 {
		t.Errorf("an editor assigned a part: %d", code)
	}
	if code := assign("owner", url.Values{"part_header": {footer.ID.String()}}); code != 422 {
		t.Errorf("a footer part in the header slot: %d", code)
	}
	if !strings.Contains(r.get(path).Body.String(), "PARTS-BODY") || strings.Contains(r.get(path).Body.String(), "B6-HEADER-PART") {
		t.Fatal("a refused assignment changed the page")
	}

	if code := assign("owner", url.Values{"part_header": {header.ID.String()}, "part_footer": {footer.ID.String()}}); code != 303 {
		t.Fatalf("assign: %d", code)
	}
	r.eventually("assigning a part purges the cached page", func() bool { return !r.cached(path) })
	body := r.get(path).Body.String()
	for _, want := range []string{"B6-HEADER-PART", "B6-FOOTER-PART", "PARTS-BODY"} {
		if !strings.Contains(body, want) {
			t.Fatalf("assigned page lacks %s:\n%.800s", want, body)
		}
	}
	if strings.Contains(body, `class="hm-header"`) {
		t.Error("the theme's own header is still drawn")
	}

	// Saving the part reaches the page too (the block path, for a part).
	res := r.do("owner", "POST", "/admin/entities/"+header.ID.String()+"/builder/save", map[string]any{"version": 1, "doc": textDoc("h", "B6-HEADER-V2"), "op": "save"})
	if res.Code != 200 {
		t.Fatalf("save part: %d %s", res.Code, res.Body.String())
	}
	r.eventually("a part save purges the page", func() bool { return !r.cached(path) })
	if body := r.get(path).Body.String(); !strings.Contains(body, "B6-HEADER-V2") || strings.Contains(body, "B6-HEADER-PART") {
		t.Fatalf("the part save did not reach the page")
	}

	// Clearing gives the theme's own header back, byte for byte.
	if code := assign("owner", url.Values{}); code != 303 {
		t.Fatalf("clear: %d", code)
	}
	r.eventually("clearing purges the page", func() bool { return !r.cached(path) })
	if back := r.get(path).Body.String(); back != plain {
		t.Fatalf("clearing the parts did not restore the page (%d vs %d bytes)", len(back), len(plain))
	}
}

// A4/ISC-17: a theme that did not opt in renders byte-identically with parts
// assigned (and keeps its own header and footer).
func TestReusePartsIgnoredWithoutOptIn_Integration(t *testing.T) {
	r := newReuseIT(t)
	if err := r.srv.renderEngine.SetTheme(themeFixture(t, "theme_plain.json")); err != nil {
		t.Fatal(err)
	}
	header := r.mk(r.partSc, "Plain header", map[string]any{"slot": "header", "page_doc": textDoc("h", "B6-IGNORED-HEADER")})
	footer := r.mk(r.partSc, "Plain footer", map[string]any{"slot": "footer", "page_doc": textDoc("f", "B6-IGNORED-FOOTER")})
	page := r.mk(r.pageSc, "No opt in", map[string]any{"page_doc": headingDoc("h", "NO-OPT-IN")})
	r.publish(page)
	path := "/" + page.Slug

	if code := r.do("owner", "POST", "/admin/theme/parts", url.Values{}).Code; code != 303 {
		t.Fatalf("clear: %d", code)
	}
	r.eventually("the page is cached", func() bool { r.get(path); return r.cached(path) })
	before := r.get(path).Body.String()

	if code := r.do("owner", "POST", "/admin/theme/parts", url.Values{"part_header": {header.ID.String()}, "part_footer": {footer.ID.String()}}).Code; code != 303 {
		t.Fatalf("assign: %d", code)
	}
	r.eventually("assigning purges the page", func() bool { return !r.cached(path) })
	after := r.get(path).Body.String()
	if after != before {
		t.Fatalf("a theme without the opt-in changed (%d vs %d bytes)", len(after), len(before))
	}
	if strings.Contains(after, "B6-IGNORED") || !strings.Contains(after, `class="hm-header"`) {
		t.Error("the theme's own header must stay")
	}
	// The Layout parts page says so.
	page2 := r.do("owner", "GET", "/admin/theme/parts", nil).Body.String()
	if strings.Count(page2, "Your theme does not use") != 1 || !strings.Contains(page2, "header, footer, single page layout, archive layout") {
		t.Errorf("the Layout parts page must say no slot is supported:\n%.300s", page2)
	}
	r.do("owner", "POST", "/admin/theme/parts", url.Values{}) // leave the site as found
}
