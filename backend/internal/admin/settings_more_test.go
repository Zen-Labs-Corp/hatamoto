package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/siteconfig"
)

// homeSite records the homepage a save chose.
type homeSite struct {
	*fakeSite
	saved *uuid.UUID
}

func (h *homeSite) Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	if in.HomepageEntityID != nil {
		h.saved = in.HomepageEntityID
		h.fakeSite.cfg.HomepageEntityID = *in.HomepageEntityID
	}
	return h.fakeSite.Update(ctx, in)
}

func TestHomepageSetting(t *testing.T) {
	t.Parallel()

	about, contact := uuid.New(), uuid.New()
	site := &homeSite{fakeSite: newFakeSite("Acme")}
	g := newRig(t, func(d *Deps) {
		d.Site = site
		d.Growth.Pickers = fakePickers{entities: []PickOption{{Value: about.String(), Label: "About us", Slug: "about"}, {Value: contact.String(), Label: "Contact", Slug: "contact"}}}
	})
	page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if page.Code != 200 {
		t.Fatalf("form: %d", page.Code)
	}
	if issues := a11y.Check(page.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{}, nil); rec.Code != 422 || site.saved != nil {
		t.Fatalf("nothing chosen: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{"homepage_q": {"about"}}, nil); rec.Code != 303 || site.saved == nil || *site.saved != about {
		t.Fatalf("typed name: %d %v", rec.Code, site.saved)
	}
	if rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{"homepage": {contact.String()}, "homepage_prev": {"Contact"}, "homepage_q": {"Contact"}}, nil); rec.Code != 303 || *site.saved != contact {
		t.Fatalf("picked: %d", rec.Code)
	}
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `value="Contact"`) {
		t.Error("the current homepage should be shown")
	}
	if rec := g.do("GET", "/admin/settings/general", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestOldSettingsPagesRedirectToTheirNewHome(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for from, to := range map[string]string{"branding": "/admin/theme/colours", "site": "/admin/settings/general", "homepage": "/admin/settings/general",
		"contact": "/admin/settings/general", "notifications": "/admin/settings/general", "api": "/admin/settings/api-keys"} {
		rec := g.do("GET", "/admin/settings/"+from, "owner-token", nil, nil)
		if rec.Code != 301 || rec.Header().Get("Location") != to {
			t.Errorf("%s: %d %s", from, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// statusPickers adds the status lookup the real pickers have.
type statusPickers struct {
	fakePickers
	status map[uuid.UUID]string
}

func (s statusPickers) EntityStatus(_ context.Context, id uuid.UUID) (string, bool) {
	st, ok := s.status[id]
	return st, ok
}

// fakeExplain answers "/" with the page the site serves today.
type fakeExplain struct{ id, name string }

func (f fakeExplain) ExplainURL(context.Context, string) (URLExplanation, error) {
	return URLExplanation{Page: PageAnswer{Kind: "home", Name: f.name, EntityID: f.id}}, nil
}

func TestHomepageNeedsAPublishedPageAndShowsTheCurrentOne(t *testing.T) {
	t.Parallel()

	live, draft, gone := uuid.New(), uuid.New(), uuid.New()
	site := &homeSite{fakeSite: newFakeSite("Acme")}
	g := newRig(t, func(d *Deps) {
		d.Site = site
		d.Growth.Pickers = statusPickers{fakePickers{entities: []PickOption{{Value: live.String(), Label: "Live", Slug: "live"}}},
			map[uuid.UUID]string{live: "published", draft: "draft"}}
		d.Growth.Explain = fakeExplain{id: live.String(), name: "Starter home"}
	})
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `value="Starter home"`) {
		t.Error("with nothing chosen the page served at / is shown")
	}
	for name, id := range map[string]uuid.UUID{"draft": draft, "missing": gone} {
		rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{"homepage": {id.String()}, "homepage_prev": {"x"}, "homepage_q": {"x"}}, nil)
		if rec.Code != 422 || site.saved != nil {
			t.Errorf("%s: %d, saved %v", name, rec.Code, site.saved)
		}
	}
	if rec := g.do("POST", "/admin/settings/general/homepage", "owner-token", url.Values{"homepage": {live.String()}, "homepage_prev": {"Live"}, "homepage_q": {"Live"}}, nil); rec.Code != 303 {
		t.Errorf("published: %d", rec.Code)
	}
}
