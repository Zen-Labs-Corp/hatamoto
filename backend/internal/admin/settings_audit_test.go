package admin

import (
	"context"
	"net/url"
	"slices"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/siteconfig"
)

// settingsAuditExpect checks the n-th captured event (1-based) and that the
// events so far carry no unsafe detail.
func settingsAuditExpect(t *testing.T, read func() []AuditEvent, n int, target string, detail map[string]any, secrets ...string) AuditEvent {
	t.Helper()
	evs := read()
	assertAuditSafe(t, evs, secrets...)
	if len(evs) != n {
		t.Fatalf("events = %+v, want %d", evs, n)
	}
	ev := evs[n-1]
	if ev.Action != AuditSettingsUpdated || ev.Target != target || ev.Actor != "owner@example.com" {
		t.Fatalf("row = %+v, want %s on %s by the owner", ev, AuditSettingsUpdated, target)
	}
	for k, want := range detail {
		got := ev.Detail[k]
		if ws, ok := want.([]string); ok {
			gs, _ := got.([]string)
			if !slices.Equal(ws, gs) {
				t.Errorf("%s: detail[%s] = %v, want %v", target, k, got, want)
			}
		} else if got != want {
			t.Errorf("%s: detail[%s] = %v, want %v", target, k, got, want)
		}
	}
	return ev
}

func TestSettingsAuditGeneral(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	read := auditCapture(g.a)

	bad := url.Values{"name": {""}, "url": {"ftp://x"}, "language": {"!!"}, "timezone": {"Nowhere/Land"}}
	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", bad, nil); rec.Code != 422 {
		t.Fatalf("invalid identity: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/general/email", "owner-token", url.Values{"notify": {"bad"}}, nil); rec.Code != 422 {
		t.Fatalf("invalid notify: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed saves audited: %+v", evs)
	}

	site := g.a.deps.Site.(*fakeSite)
	site.cfg.Language, site.cfg.Timezone = "en", "UTC"
	form := url.Values{"name": {"Acme"}, "url": {"https://acme.example/"}, "language": {"en"}, "timezone": {"UTC"}, "tagline": {"Fresh"}}
	if rec := g.do("POST", "/admin/settings/general/identity", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("identity: %d", rec.Code)
	}
	// Only the fields that differ from the stored values are named.
	settingsAuditExpect(t, read, 1, "identity", map[string]any{"changed": []string{"name", "url", "tagline"}}, "Fresh", "acme.example")

	if rec := g.do("POST", "/admin/settings/general/email", "owner-token", url.Values{"notify": {"boss@example.com"}}, nil); rec.Code != 303 {
		t.Fatalf("notify: %d", rec.Code)
	}
	settingsAuditExpect(t, read, 2, "notifications", map[string]any{"changed": []string{"notify"}})
}

func TestSettingsAuditHomepage(t *testing.T) {
	t.Parallel()

	about := uuid.New()
	site := &homeSite{fakeSite: newFakeSite("Acme")}
	g := newRig(t, func(d *Deps) {
		d.Site = site
		d.Growth.Pickers = fakePickers{entities: []PickOption{{Value: about.String(), Label: "About us", Slug: "about"}}}
	})
	read := auditCapture(g.a)
	if rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{}, nil); rec.Code != 422 {
		t.Fatalf("nothing chosen: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	if rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{"homepage": {about.String()}, "homepage_prev": {"About us"}}, nil); rec.Code != 303 {
		t.Fatalf("homepage: %d", rec.Code)
	}
	settingsAuditExpect(t, read, 1, "homepage", map[string]any{"changed": []string{"homepage"}})
}

func TestSettingsAuditSetupSite(t *testing.T) {
	t.Parallel()

	g := newSetupRig(t)
	g.count.n = 1
	g.a.sudo.store.Grant(g.sessions.byToken["owner-token"].ID)
	read := auditCapture(g.a)
	if rec := g.do("POST", "/admin/setup/site", "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if evs := read(); len(evs) != 0 {
		t.Fatalf("failed save audited: %+v", evs)
	}
	form := url.Values{"name": {"Acme"}, "url": {"https://acme.example"}, "language": {"en"}, "timezone": {"UTC"}}
	if rec := g.do("POST", "/admin/setup/site", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("setup site: %d", rec.Code)
	}
	settingsAuditExpect(t, read, 1, "site", nil)
}

func TestSettingsAuditLocales(t *testing.T) {
	t.Parallel()

	g, site := localeFixture(t)
	site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: "nl-NL", Segment: "nl-nl"})
	g.a.deps.ValidateVisitorEntry = func(_ context.Context, _ []siteconfig.LocaleEntry, _ string, _ bool, v siteconfig.VisitorEntry) (siteconfig.VisitorEntry, error) {
		return v, nil
	}
	read := auditCapture(g.a)

	// Refused saves write nothing.
	g.do("POST", "/admin/settings/locales/order", "owner-token", url.Values{"order": {"fr,en"}}, nil)
	g.do("POST", "/admin/settings/locales/update/en", "owner-token", url.Values{"segment": {"en"}, "public": {"false"}, "name": {"x"}}, nil)
	g.do("POST", "/admin/settings/locales/organisation", "owner-token", url.Values{"org_address": {"always"}}, nil)
	g.do("POST", "/admin/settings/locales/addresses", "owner-token", url.Values{"sitemap_naming": {"nope"}}, nil)
	g.do("POST", "/admin/settings/locales/remove/en", "owner-token", url.Values{}, nil)
	if evs := read(); len(evs) != 0 {
		t.Fatalf("refused saves audited: %+v", evs)
	}

	steps := []struct {
		path   string
		form   url.Values
		target string
		detail map[string]any
	}{
		{"/admin/settings/locales/entry", url.Values{"enabled": {"true"}, "entry_path": {"/x"}}, "visitor-entry", map[string]any{"changed": []string{"enabled", "entry_path"}}},
		{"/admin/settings/locales/order", url.Values{"order": {"en,nl-NL,fr"}, "moved": {"nl-NL"}, "dir": {"up"}}, "locales", map[string]any{"op": "order"}},
		{"/admin/settings/locales/business/en", url.Values{"loc_en.phone": {"555 0100"}}, "business:en", map[string]any{"changed": []string{"loc_en.phone"}}},
		{"/admin/settings/locales/organisation", url.Values{"org_address": {""}}, "organisation", map[string]any{"changed": []string{"org_address"}}},
		{"/admin/settings/locales/addresses", url.Values{"xdefault": {"nl-NL"}, "sitemap_naming": {"group"}}, "addresses", map[string]any{"changed": []string{"xdefault"}}},
		{"/admin/settings/locales/update/nl-NL", url.Values{"name": {"Dutch"}, "segment": {"nl"}}, "locale:nl-NL", map[string]any{"op": "update", "changed": []string{"name", "segment"}}},
		{"/admin/settings/locales/remove/fr", url.Values{}, "locale:fr", map[string]any{"op": "remove"}},
	}
	for i, s := range steps {
		if rec := g.do("POST", s.path, "owner-token", s.form, nil); rec.Code != 303 {
			t.Fatalf("%s: %d\n%s", s.path, rec.Code, rec.Body.String())
		}
		settingsAuditExpect(t, read, i+1, s.target, s.detail, "555 0100")
	}
}
