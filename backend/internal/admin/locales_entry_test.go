package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/siteconfig"
)

// entryRig is the locale fixture with a validator that stores the value as is
// and refuses the path /bad.
func entryRig(t *testing.T) (*ownerRig, *fakeSite) {
	t.Helper()
	g, site := localeFixture(t)
	g.a.deps.ValidateVisitorEntry = func(_ context.Context, _ []siteconfig.LocaleEntry, _ string, _ bool, v siteconfig.VisitorEntry) (siteconfig.VisitorEntry, error) {
		for _, p := range v.Paths {
			if p == "/bad" {
				return siteconfig.VisitorEntry{}, fieldErr{"visitor_entry", `entry path "/bad" is a route of its own`}
			}
		}
		return v, nil
	}
	return g, site
}

func entryPost(g *ownerRig, token string, kv url.Values) (int, string) {
	rec := g.do("POST", "/admin/settings/locales/entry", token, kv, nil)
	return rec.Code, rec.Body.String()
}

func TestVisitorEntrySavesAndReloads(t *testing.T) {
	t.Parallel()
	g, site := entryRig(t)
	code, _ := entryPost(g, "owner-token", url.Values{"enabled": {"true"}, "entry_path": {"/contact-form", "", "/pricing"}, "legacy_cookie": {" old_loc "}})
	if code != 303 {
		t.Fatalf("code = %d", code)
	}
	v := site.cfg.VisitorEntry
	if !v.Enabled || strings.Join(v.Paths, ",") != "/contact-form,/pricing" || v.LegacyCookie != "old_loc" || g.flushed != 1 {
		t.Errorf("stored = %+v flushed=%d", v, g.flushed)
	}
	body := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`id="visitor-entry-form"`, `value="/contact-form"`, `value="/pricing"`, `value="old_loc"`, "Send visitors of / to their locale", "Switcher link:"} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	if strings.Contains(body, "Keep visitors in their locale") {
		t.Error("the panel shows a row that belongs to another part")
	}
	_, form, _ := strings.Cut(body, `id="visitor-entry-form"`)
	if _, afterInput, _ := strings.Cut(form, "<input"); !strings.HasPrefix(afterInput, ` type="hidden" name="_csrf"`) {
		t.Error("the csrf field is not the first field of the form")
	}
}

func TestVisitorEntryInvalidPathShowsAFieldErrorAndDoesNotSave(t *testing.T) {
	t.Parallel()
	g, site := entryRig(t)
	code, body := entryPost(g, "owner-token", url.Values{"enabled": {"true"}, "entry_path": {"/bad"}})
	if code != 422 || !strings.Contains(body, "is a route of its own") || !strings.Contains(body, `href="#entry-path-0"`) || !strings.Contains(body, `value="/bad"`) {
		t.Fatalf("code=%d", code)
	}
	if site.cfg.VisitorEntry.Enabled || len(site.cfg.VisitorEntry.Paths) != 0 || g.flushed != 0 {
		t.Errorf("a rejected save stored %+v", site.cfg.VisitorEntry)
	}
}

func TestVisitorEntryAddAndRemoveRowsWorkWithoutSaving(t *testing.T) {
	t.Parallel()
	g, site := entryRig(t)
	code, body := entryPost(g, "owner-token", url.Values{"enabled": {"true"}, "entry_path": {"/a", "/b", ""}, "add_entry_path": {"1"}})
	if code != 200 || strings.Count(body, `name="entry_path"`) != 4 || !strings.Contains(body, `value="/a"`) {
		t.Fatalf("add: code=%d rows=%d", code, strings.Count(body, `name="entry_path"`))
	}
	code, body = entryPost(g, "owner-token", url.Values{"enabled": {"true"}, "entry_path": {"/a", "/b", ""}, "remove_entry_path": {"0"}})
	if code != 200 || strings.Contains(body, `value="/a"`) || !strings.Contains(body, `value="/b"`) || strings.Count(body, `name="entry_path"`) != 2 {
		t.Fatalf("remove: code=%d", code)
	}
	if site.cfg.VisitorEntry.Enabled || g.flushed != 0 {
		t.Errorf("a row button saved: %+v", site.cfg.VisitorEntry)
	}
}

func TestVisitorEntryPanelAbsentOnASingleLocaleSite(t *testing.T) {
	t.Parallel()
	g, site := entryRig(t)
	site.cfg.Locales = nil
	body := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "visitor-entry-form") {
		t.Error("single-locale site shows the panel")
	}
	if code, _ := entryPost(g, "owner-token", url.Values{"enabled": {"true"}}); code != 422 || site.cfg.VisitorEntry.Enabled {
		t.Errorf("single-locale save: code=%d %+v", code, site.cfg.VisitorEntry)
	}
}

func TestVisitorEntryIsOwnerOnly(t *testing.T) {
	t.Parallel()
	g, site := entryRig(t)
	if code, _ := entryPost(g, "editor-token", url.Values{"enabled": {"true"}}); code != 403 || site.cfg.VisitorEntry.Enabled {
		t.Errorf("editor: code=%d %+v", code, site.cfg.VisitorEntry)
	}
}

func TestVisitorEntryPanelPassesA11y(t *testing.T) {
	t.Parallel()
	g, _ := entryRig(t)
	g.a.deps.Site.(*fakeSite).cfg.VisitorEntry = siteconfig.VisitorEntry{Enabled: true, Paths: []string{"/contact-form"}}
	rec := g.do("GET", "/admin/settings/locales", "owner-token", nil, nil)
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}
