package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
)

type fakeStanding struct {
	rule     StandingRule
	active   *StandingApproval
	waiting  []StandingWaiting
	paused   *StandingPaused
	digest   []StandingDigestRow
	approved []string // plugin/run of each Approve
}

func (f *fakeStanding) Rule(string) (StandingRule, bool) { return f.rule, f.rule.Run != "" }
func (f *fakeStanding) Active(context.Context, string, string) (*StandingApproval, error) {
	return f.active, nil
}
func (f *fakeStanding) Approve(_ context.Context, plugin, run, _, version, hash string) error {
	if version != f.rule.Version || hash != f.rule.Hash {
		return ErrStandingChanged
	}
	f.approved = append(f.approved, plugin+"/"+run)
	return nil
}
func (f *fakeStanding) Waiting(context.Context, string) ([]StandingWaiting, error) {
	return f.waiting, nil
}
func (f *fakeStanding) Digest(context.Context, time.Time) ([]StandingDigestRow, error) {
	return f.digest, nil
}
func (f *fakeStanding) Paused(context.Context, string, string) (*StandingPaused, error) {
	return f.paused, nil
}

var standingPlugin = PluginInfo{
	Name: "shop-sync", Version: "1.2.4", Loaded: true, Package: &PackageMeta{Enabled: true}, Standing: true,
	Runs: []RunDecl{{Name: "prices", Title: "Price sync"}},
}

var standingRule = StandingRule{Version: "1.2.4", Hash: "rulehash1", Run: "prices", Fields: []string{"price", "stock"}, Schemas: []string{"products"}, MaxPerHour: 200}

func newStandingRig(t *testing.T, fs *fakeStanding, plugins ...PluginInfo) *rig {
	t.Helper()
	if len(plugins) == 0 {
		plugins = []PluginInfo{standingPlugin}
	}
	return newRig(t, func(d *Deps) {
		d.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}, list: plugins}
		d.Growth.Standing = fs
	})
}

const standingPath = "/admin/plugins/shop-sync/automatic"

func TestStandingTab(t *testing.T) {
	t.Parallel()

	fs := &fakeStanding{rule: standingRule, waiting: []StandingWaiting{{RunID: uuid.New(), Run: "prices", ChangesetID: 44, Items: 15, At: time.Now()}}}
	g := newStandingRig(t, fs)
	rec := g.do("GET", standingPath, "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("tab: %d", rec.Code)
	}
	for _, want := range []string{
		"Option 1: no approval needed", "plugin_shop_sync_&lt;function&gt;", "Option 2: standing approval",
		`class="hm-permlist"`, "May change price and stock", "Only on products it created", "Up to 200 changes an hour", "Never text, links",
		`action="` + standingPath + `"`, `name="run" value="prices"`, `name="version" value="1.2.4"`, `name="rule" value="rulehash1"`, "Sign in again and allow", "Not now",
		"When a run steps outside the rule", "Price sync is waiting for you.", `href="/admin/plugins/shop-sync/changesets/44"`, ">Review</a>",
		`aria-current="page">Automatic updates`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tab lacks %q", want)
		}
	}
	if strings.Contains(body, `name="fields"`) || strings.Contains(body, `name="max_per_hour"`) {
		t.Error("the form must carry no rule values")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/plugins-automatic.html", normalizePage(body))

	// Allowed: the rule shows without a form.
	fs.active = &StandingApproval{By: "owner@example.com", At: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	fs.waiting = nil
	body = g.do("GET", standingPath, "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Automatic updates are on.", "Allowed by owner@example.com on 2 Nov 2026.", "No run is waiting for you."} {
		if !strings.Contains(body, want) {
			t.Errorf("allowed tab lacks %q", want)
		}
	}
	if strings.Contains(body, "Sign in again and allow") {
		t.Error("an allowed rule offers no second approval")
	}
}

func TestStandingTabVisibility(t *testing.T) {
	t.Parallel()

	fs := &fakeStanding{rule: standingRule}
	g := newStandingRig(t, fs)
	if body := g.do("GET", "/admin/plugins/shop-sync/settings", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Automatic updates") {
		t.Error("the owner sees the tab on a plugin that declares a rule")
	}
	if rec := g.do("GET", standingPath, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
	plain := PluginInfo{Name: "hello", Version: "1.0.0", Loaded: true, Package: &PackageMeta{Enabled: true}}
	g2 := newStandingRig(t, fs, plain)
	if body := g2.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil).Body.String(); strings.Contains(body, "Automatic updates") {
		t.Error("no tab for a plugin without a rule")
	}
	if rec := g2.do("GET", "/admin/plugins/hello/automatic", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("no rule: %d, want 404", rec.Code)
	}
	if rec := g.do("GET", "/admin/plugins/ghost/automatic", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown plugin: %d, want 404", rec.Code)
	}
	g3 := newRig(t, func(d *Deps) {
		d.Growth.Plugins = &fakePlugins{settings: map[string]map[string]any{}, list: []PluginInfo{standingPlugin}}
	})
	if body := g3.do("GET", "/admin/plugins/shop-sync/settings", "owner-token", nil, nil).Body.String(); strings.Contains(body, "Automatic updates") {
		t.Error("no tab when the service is not wired")
	}
	if rec := g3.do("GET", standingPath, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("not wired: %d, want 503", rec.Code)
	}
}

// TestStandingApprovalSaveNeedsSudo pins ADR 0004 invariant 2: allowing a rule
// is a POST behind the password page; without it nothing is saved and the
// request is not replayed.
func TestStandingApprovalSaveNeedsSudo(t *testing.T) {
	t.Parallel()

	fs := &fakeStanding{rule: standingRule}
	g := newStandingRig(t, fs)
	form := url.Values{"run": {"prices"}, "version": {"1.2.4"}, "rule": {"rulehash1"}}
	rec := g.do("POST", standingPath, "owner-token", form, map[string]string{"Referer": "http://example.com" + standingPath})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), g.a.sudoPath()) || len(fs.approved) != 0 {
		t.Fatalf("no sudo: %d %s, %d approvals", rec.Code, rec.Header().Get("Location"), len(fs.approved))
	}
	grantSudo(g)
	// The server reads the rule itself: a form naming another run saves nothing.
	if rec := g.do("POST", standingPath, "owner-token", url.Values{"run": {"other"}}, nil); rec.Code != http.StatusBadRequest || len(fs.approved) != 0 {
		t.Fatalf("wrong run: %d, %d approvals", rec.Code, len(fs.approved))
	}
	rec = g.do("POST", standingPath, "owner-token", form, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != standingPath || len(fs.approved) != 1 || fs.approved[0] != "shop-sync/prices" {
		t.Fatalf("sudo: %d %s %v", rec.Code, rec.Header().Get("Location"), fs.approved)
	}
	if rec := g.do("POST", standingPath, "editor-token", form, nil); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
}

// The owner approves what the tab showed: a form read from an older release
// (the plugin updated in between) allows nothing.
func TestStandingApproveRefusesWhatWasNotRead(t *testing.T) {
	t.Parallel()

	fs := &fakeStanding{rule: standingRule}
	g := newStandingRig(t, fs)
	grantSudo(g)
	for name, form := range map[string]url.Values{
		"old version": {"run": {"prices"}, "version": {"1.2.3"}, "rule": {"rulehash1"}},
		"old rule":    {"run": {"prices"}, "version": {"1.2.4"}, "rule": {"oldhash"}},
		"no fields":   {"run": {"prices"}},
	} {
		rec := g.do("POST", standingPath, "owner-token", form, nil)
		if rec.Code != http.StatusSeeOther || len(fs.approved) != 0 {
			t.Fatalf("%s: %d, %d approvals", name, rec.Code, len(fs.approved))
		}
		req := httptestReq("GET", standingPath, "owner-token")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		if body := serve(g.h, req, nil).Body.String(); !strings.Contains(body, "The plugin changed; review the rule again.") {
			t.Fatalf("%s: the owner is not told to review again", name)
		}
	}
}

func TestStandingTabPausedState(t *testing.T) {
	stamp := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	pluginsNow = func() time.Time { return stamp }
	t.Cleanup(func() { pluginsNow = time.Now })
	fs := &fakeStanding{rule: standingRule, paused: &StandingPaused{At: stamp, Reason: "plugin updated"},
		digest: []StandingDigestRow{{Plugin: "shop-sync", Changes: 300}, {Plugin: "shop-sync", Changes: 11}, {Plugin: "event-feed", Changes: 12}}}
	g := newStandingRig(t, fs)
	body := g.do("GET", standingPath, "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		"Updated", "shop-sync was updated to 1.2.4. Its automatic updates are paused.", "Review and allow again", `href="#opt2"`,
		"Keep paused", `href="/admin/plugins/shop-sync/settings"`, "Sign in again and allow",
		"Daily summary, 2 Nov 2026", "Automatic runs", "<td", ">311<", ">12<", "See runs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("paused tab lacks %q", want)
		}
	}
	if strings.Count(body, `>See runs<`) != 2 {
		t.Errorf("want one See runs per plugin, got %d", strings.Count(body, `>See runs<`))
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/plugins-automatic-paused.html", normalizePage(body))

	fs.paused.Reason = "schema changed"
	if body := g.do("GET", standingPath, "owner-token", nil, nil).Body.String(); !strings.Contains(body, "A content type shop-sync updates was changed.") {
		t.Error("a schema change is worded as one")
	}
	// Allowed again: the paused callout and badge are gone.
	fs.active = &StandingApproval{By: "owner@example.com", At: stamp}
	body = g.do("GET", standingPath, "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "Review and allow again") || strings.Contains(body, "paused") {
		t.Error("an active approval shows no paused notice")
	}
	// Never approved and nothing published: no notice, no summary.
	g2 := newStandingRig(t, &fakeStanding{rule: standingRule})
	body = g2.do("GET", standingPath, "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "Review and allow again") || strings.Contains(body, "Daily summary") {
		t.Error("the fresh state shows neither the paused notice nor the summary")
	}
}
