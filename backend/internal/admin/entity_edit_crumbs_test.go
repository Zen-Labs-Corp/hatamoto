package admin

import (
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func crumbPost(t *testing.T, vals url.Values) seoForm {
	t.Helper()
	r := httptest.NewRequest("POST", "/", strings.NewReader(vals.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	return seoFromForm(r)
}

func TestSEOFormBreadcrumbFields(t *testing.T) {
	t.Parallel()

	if slices.ContainsFunc(seoCrumbKeys, func(k string) bool { return slices.Contains(seoKeys, k) }) {
		t.Error("a crumb key is in seoKeys: the builder drawer would wipe it")
	}
	term := fid(7).String()
	f := crumbPost(t, url.Values{"_seo_crumb": {"1"}, "seo_breadcrumb_title": {"  Short "}, "seo_primary_term": {term}})
	m := map[string]any{}
	f.mergeCrumbs(m)
	if m["breadcrumb_title"] != "Short" || m["primary_term"] != term {
		t.Errorf("merge: %v", m)
	}
	// An emptied field removes the key.
	f = crumbPost(t, url.Values{"_seo_crumb": {"1"}})
	f.mergeCrumbs(m)
	if len(m) != 0 {
		t.Errorf("empty did not clear: %v", m)
	}
	// No marker: the stored values stay.
	stored := map[string]any{"breadcrumb_title": "Kept", "primary_term": term}
	r := httptest.NewRequest("POST", "/", strings.NewReader("seo_title=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	f = seoFromForm(r)
	f.keepUnpostedCrumbs(r, stored)
	next := map[string]any{}
	f.mergeCrumbs(next)
	if next["breadcrumb_title"] != "Kept" || next["primary_term"] != term {
		t.Errorf("unposted marker did not keep stored values: %v", next)
	}
	// The builder drawer's merge leaves the keys alone.
	b := map[string]any{"breadcrumb_title": "Kept"}
	seoForm{Title: "T"}.merge(b)
	if b["breadcrumb_title"] != "Kept" {
		t.Errorf("builder merge touched the key: %v", b)
	}
}

func TestSEOFormCrumbErrors(t *testing.T) {
	t.Parallel()

	a, b := fid(1).String(), fid(2).String()
	check := func(f seoForm, picked map[string]bool, termsSet bool) map[string]string {
		errs := map[string]string{}
		f.crumbErrors(errs, picked, termsSet)
		return errs
	}
	if e := check(seoForm{PrimaryTerm: a}, map[string]bool{a: true}, true); len(e) != 0 {
		t.Errorf("attached term refused: %v", e)
	}
	if e := check(seoForm{PrimaryTerm: b}, map[string]bool{a: true}, true); e["seo_primary_term"] == "" {
		t.Error("detached term accepted")
	}
	if e := check(seoForm{PrimaryTerm: b}, nil, false); len(e) != 0 {
		t.Errorf("a form without the term picker must not judge the term: %v", e)
	}
	if e := check(seoForm{PrimaryTerm: "nope"}, nil, false); e["seo_primary_term"] == "" {
		t.Error("non-uuid accepted")
	}
	if e := check(seoForm{BreadcrumbTitle: strings.Repeat("x", 121)}, nil, false); e["seo_breadcrumb_title"] == "" {
		t.Error("121-char title accepted")
	}
	if e := check(seoForm{BreadcrumbTitle: strings.Repeat("é", 120)}, nil, false); len(e) != 0 {
		t.Errorf("120-char title refused: %v", e)
	}
}

func TestPrimaryTermOptions(t *testing.T) {
	t.Parallel()

	groups := []termGroup{{Kind: "category", Terms: []termPick{{ID: "c1", Name: "News", Checked: true}, {ID: "c2", Name: "Old"}}}, {Kind: "tag", Terms: []termPick{{ID: "t1", Name: "Go", Checked: true}}}}
	opts := primaryTermOptions(groups, "t1")
	if len(opts) != 3 || opts[0].Value != "" || opts[0].Selected || !opts[2].Selected || opts[1].Value != "c1" {
		t.Errorf("options: %+v", opts)
	}
	if opts = primaryTermOptions(groups, "c2"); !opts[0].Selected {
		t.Errorf("a pick that is not ticked must fall back to Automatic: %+v", opts)
	}
}
