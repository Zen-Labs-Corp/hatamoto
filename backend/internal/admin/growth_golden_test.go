package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
)

func fixedID(n int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + strings.Repeat("0", 11) + string(rune('0'+n)))
}

// TestGrowthPageGoldens pins the markup of the main screens of the site and
// growth area with fixed data, and runs the accessibility checker on each.
func TestGrowthPageGoldens(t *testing.T) {
	stamp := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	analyticsNow = func() time.Time { return stamp }
	pluginsNow = func() time.Time { return stamp }
	t.Cleanup(func() { analyticsNow, pluginsNow = time.Now, time.Now })
	fm := newFakeMenus()
	ename := "About us"
	eid := fixedID(2)
	m := fm.add("Main", "header",
		&menu.MenuItem{Label: "Home", LinkType: menu.LinkTypeCustom, URL: ptr("/"), OpenInNewTab: false},
		&menu.MenuItem{Label: "Company", LinkType: menu.LinkTypeEntity, EntityID: &eid, EntityName: &ename, Children: []*menu.MenuItem{
			{Label: "News", LinkType: menu.LinkTypeArchive, URL: ptr("/category/news")}}})
	delete(fm.menus, m.ID)
	m.ID = fixedID(1)
	fm.menus[m.ID] = m

	ff := newFakeForms()
	fo := ff.addForm("Contact", contactFields...)
	delete(ff.forms, fo.ID)
	fo.ID = fixedID(3)
	ff.forms[fo.ID] = fo
	sub := ff.addSub(fo, form.SubmissionStatusRead, stamp, map[string]any{"name": "Ada", "email": "ada@example.com", "message": "Hello"})
	delete(ff.subs, sub.ID)
	sub.ID = fixedID(4)
	ff.subs[sub.ID] = sub

	stats := &fakeAgentStats{series: []agents.SeriesPoint{{Day: "2026-01-01", Key: "OpenAI", Hits: 4}, {Day: "2026-01-02", Key: "OpenAI", Hits: 6}, {Day: "2026-01-02", Key: "Anthropic", Hits: 2}}}
	fl := newFakeLinking()
	target := fixedID(5)
	fl.names[target] = "Services"
	fp := &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{{Name: "hello", Version: "1.0.0", Loaded: true, Hooks: []string{"on_entity_published"}, Permissions: []string{"kv"}, Granted: []string{"kv"}}}}
	until := stamp.Add(24 * time.Hour)
	pk := &fakePackages{unsigned: true, unsignedEnd: &until, signers: []SignerInfo{{Name: "Example signer", Algorithm: "ed25519", Fingerprint: "abc123", Source: "Built in"}}, registry: RegistryView{
		Host: "registry.example", FetchedAt: stamp.Add(-3 * time.Hour),
		Entries: []RegistryEntry{
			{Kind: "plugin", Name: "hello", Title: "Hello", Version: "1.1.0", Description: "Says hello.", Permissions: []string{"kv"}, Installed: true, InstalledVersion: "1.0.0", Update: true, Tier: "first-party", Category: "Content"},
			{Kind: "plugin", Name: "gallery", Title: "Gallery", Version: "2.0.0", Description: "Photo galleries for pages.", Author: "Example Author", Tier: "community", Category: "Media", Featured: true,
				Permissions: []string{"kv", "media.read"}, About: "Adds a gallery section.\nWorks with the media library.", Features: []string{"Grid layout", "Lightbox"}, Size: 20480, Source: "https://example.com/gallery", Homepage: "https://example.com/gallery"},
			{Kind: "plugin", Name: "seo-helper", Version: "0.3.0", Description: "Checks titles.", Permissions: []string{}},
			{Kind: "theme", Name: "hanshi", Title: "Hanshi", Version: "1.0.0", Description: "The first-party blog theme.", Installed: true, InstalledVersion: "1.0.0", Active: true, Tier: "first-party", SiteType: "blog", Flags: []string{"dark"}, Featured: true,
				About: "A calm reading theme.", Features: []string{"Dark mode"}, Size: 40960},
			{Kind: "theme", Name: "ledger", Title: "Ledger", Version: "1.2.0", Description: "Documentation with a sidebar.", Author: "Example Author", Tier: "community", SiteType: "docs", Flags: []string{"nojs"},
				Screenshot: "https://registry.example/screenshots/ledger.jpg", Homepage: "https://example.com/ledger", About: "Docs theme.", Size: 30720},
			{Kind: "theme", Name: "kiln", Title: "Kiln", Version: "2.1.0", Description: "A landing page theme.", Author: "Example Author", Tier: "community", SiteType: "landing", Flags: []string{"builder"}, Installed: true, InstalledVersion: "2.0.0", Update: true},
			{Kind: "theme", Name: "plain", Version: "0.1.0", Description: "A theme with no screenshot or site type."},
		}}, themes: []ThemeInfo{{Name: "hanshi", Version: "1.0.0", Active: true, Signed: true, Signer: "Example signer"}}}
	g := newRig(t, func(d *Deps) {
		d.Growth.Packages = pk
		d.Growth.Menus, d.Growth.Forms, d.Growth.Agents, d.Growth.Linking, d.Growth.Plugins = fm, ff, stats, fl, fp
		d.Growth.Referrals = fakeReferrals{}
		d.Growth.Explain = fakeExplainer{answers: map[string]URLExplanation{"/old": {Redirects: &redirect.Answer{Path: "/old", Normalised: "/old", MatchedBy: "db_rules",
			Redirect: &redirect.Match{Target: "/new", Status: 301}, Stages: []redirect.Stage{{Provider: "db_rules", Priority: 100, Matched: true, Target: "/new", Status: 301, Variant: "exact"}}}}}}
	})
	pages := []struct{ name, path string }{
		{"menus-edit", "/admin/theme/menus/" + m.ID.String()},
		{"forms-edit", "/admin/settings/forms/" + fo.ID.String() + "/edit"},
		{"forms-submission", "/admin/settings/submissions/" + sub.ID.String()},
		{"redirects-explain", "/admin/settings/redirects/explain?path=%2Fold"},
		{"analytics-overview", "/admin/analytics?from=2026-01-01&to=2026-01-03"},
		{"analytics-crawlers", "/admin/analytics/crawlers?from=2026-01-01&to=2026-01-03"},
		{"analytics-google", "/admin/analytics/google?from=2026-01-01&to=2026-01-03"},
		{"analytics-content", "/admin/analytics/content?from=2026-01-01&to=2026-01-03"},
		{"analytics-referrals", "/admin/analytics/referrals?from=2026-01-01&to=2026-01-03"},
		{"analytics-health", "/admin/analytics/health?from=2026-01-01&to=2026-01-03"},
		{"analytics-agent", "/admin/analytics/agents/gptbot?from=2026-01-01&to=2026-01-03"},
		{"theme-browse", "/admin/theme/browse"},
		{"theme-browse-detail", "/admin/theme/themes/hanshi/about"},
		{"settings-theme", "/admin/theme/themes"},
		{"plugins", "/admin/plugins"},
		{"plugins-browse", "/admin/plugins/browse"},
		{"plugins-browse-detail", "/admin/plugins/gallery/about"},
		{"plugins-security", "/admin/plugins/security"},
		{"plugin", "/admin/plugins/hello/settings"},
		{"plugin-permissions", "/admin/plugins/hello/permissions"},
	}
	for _, p := range pages {
		rec := g.do("GET", p.path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", p.name, rec.Code)
		}
		body := normalizePage(rec.Body.String())
		compareGolden(t, "pages/growth-"+p.name+".html", body)
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", p.name, strings.Join(issues, "; "))
		}
	}
}
