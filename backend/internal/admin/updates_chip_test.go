package admin

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func chipKinds(c *UpdateChip) string {
	var k []string
	for _, b := range c.Blocks {
		k = append(k, b.Kind)
	}
	return strings.Join(k, ",")
}

func TestBuildUpdateChip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)
	ok := UpdatesHistoryRow{When: now.Add(-48 * time.Hour), From: "v1.4.1", To: "v1.4.2", Phase: "success"}
	rolled := UpdatesHistoryRow{When: now.Add(-24 * time.Hour), From: "v1.4.2", To: "v1.4.3", Phase: "rolled_back"}
	var many []UpdatesHistoryRow
	for i := 0; i < 9; i++ {
		many = append(many, ok)
	}

	for _, c := range []struct {
		name   string
		g      UpdatesGlance
		title  string
		blocks string
		rows   int
		note   string
		ver    string
	}{
		{"up to date", UpdatesGlance{Running: "1.4.2", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "up_to_date"}, History: []UpdatesHistoryRow{rolled, ok}},
			"Up to date", "success,danger", 2, "", "v1.4.2"},
		{"available", UpdatesGlance{Running: "v1.4.2", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "scheduled", Target: "1.4.3"}, History: []UpdatesHistoryRow{ok}},
			"v1.4.3 is ready", "success,info", 1, "", "v1.4.2"},
		{"major", UpdatesGlance{Running: "v1.4.2", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}},
			"Major update v2.0.0 is ready", "info", 0, "Review it in Update settings", "v1.4.2"},
		{"stale major equal to the running release", UpdatesGlance{Running: "v2.0.0", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "available_major", AvailableMajor: "v2.0.0"}},
			"Up to date", "", 0, "", "v2.0.0"},
		{"rolled back", UpdatesGlance{Running: "v1.4.2", Mode: "auto", Channel: "edge", Status: UpdatesStatus{Decision: "rolled_back", Target: "v1.4.3"}, History: []UpdatesHistoryRow{rolled, ok}},
			"v1.4.3 rolled back", "success,danger", 2, "", "v1.4.2"},
		{"dev", UpdatesGlance{Running: "dev", Dev: true, History: []UpdatesHistoryRow{ok}},
			"Development build", "", 0, "disabled in development builds", "dev build"},
		{"off for 90 days", UpdatesGlance{Running: "v1.4.2", Mode: "off", Channel: "stable", OffSince: &old},
			"Updates are off", "", 0, "off for 90+ days", "v1.4.2"},
		{"off for a while", UpdatesGlance{Running: "v1.4.2", Mode: "off", Channel: "stable", OffSince: &now},
			"Updates are off", "", 0, "", "v1.4.2"},
		{"applying", UpdatesGlance{Running: "v1.4.2", Mode: "auto", Channel: "stable", Applying: true, Status: UpdatesStatus{Decision: "scheduled", Target: "v1.4.3"}, History: []UpdatesHistoryRow{ok}},
			"Updating to v1.4.3", "success,info", 1, "", "v1.4.2"},
		{"nine releases keep six", UpdatesGlance{Running: "v1.4.2", Mode: "auto", Status: UpdatesStatus{Decision: "up_to_date"}, History: many},
			"Up to date", "success,success,success,success,success,success", 6, "", "v1.4.2"},
		{"ready beats the oldest block", UpdatesGlance{Running: "v1.4.2", Mode: "auto", Status: UpdatesStatus{Decision: "scheduled", Target: "v1.4.3"}, History: many},
			"v1.4.3 is ready", "success,success,success,success,success,info", 6, "", "v1.4.2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ch := buildUpdateChip(c.g, "/admin", time.UTC, now)
			if ch.Title != c.title || chipKinds(ch) != c.blocks || len(ch.Rows) != c.rows || ch.Version != c.ver {
				t.Errorf("chip = %q blocks %q rows %d version %q", ch.Title, chipKinds(ch), len(ch.Rows), ch.Version)
			}
			if !strings.Contains(ch.Note, c.note) {
				t.Errorf("note %q lacks %q", ch.Note, c.note)
			}
			if !strings.HasPrefix(ch.Name, ch.Version+",") {
				t.Errorf("accessible name %q must start with the visible text %q", ch.Name, ch.Version)
			}
			if ch.Href != "/admin/settings/updates" {
				t.Errorf("href %q", ch.Href)
			}
		})
	}
}

func TestPartialUpdateChip(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	ok := UpdatesHistoryRow{When: now.Add(-48 * time.Hour), From: "v1.4.1", To: "v1.4.2", Phase: "success"}
	rolled := UpdatesHistoryRow{When: now.Add(-24 * time.Hour), From: "v1.4.2", To: "v1.4.3", Phase: "rolled_back"}
	for name, g := range map[string]UpdatesGlance{
		"uptodate":   {Running: "v1.4.2", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "up_to_date"}, History: []UpdatesHistoryRow{ok}},
		"available":  {Running: "v1.4.2", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "scheduled", Target: "v1.4.3"}, History: []UpdatesHistoryRow{ok}},
		"applying":   {Running: "v1.4.2", Mode: "auto", Channel: "stable", Applying: true, Status: UpdatesStatus{Decision: "scheduled", Target: "v1.4.3"}, History: []UpdatesHistoryRow{ok}},
		"rolledback": {Running: "v1.4.2", Mode: "auto", Channel: "stable", Status: UpdatesStatus{Decision: "rolled_back", Target: "v1.4.3"}, History: []UpdatesHistoryRow{rolled, ok}},
		"dev":        {Running: "dev", Dev: true},
	} {
		checkPartial(t, a, "hm/updatechip", name, buildUpdateChip(g, "/admin", time.UTC, now))
	}
}

func TestUpdateChipOnPages(t *testing.T) {
	t.Parallel()

	const marker = "hm-topbar__chip"
	t.Run("owner sees it after the crumbs", func(t *testing.T) {
		g := newUpdatesRig(t, baseUpdates())
		body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
		i, j := strings.Index(body, "</nav>"), strings.Index(body, marker)
		if j < 0 || i < 0 || j < i {
			t.Fatalf("chip missing or before the crumbs (nav %d, chip %d)", i, j)
		}
		for _, w := range []string{`popovertarget="hm-update-chip"`, `aria-expanded="false"`, `href="/admin/settings/updates"`, "v1.4.1, Up to date. Update history", `<span aria-hidden="true">&rsaquo;</span></a></footer>`, ">v1.4.1<"} {
			if !strings.Contains(body, w) {
				t.Errorf("page lacks %q", w)
			}
		}
	})
	t.Run("also on the Updates page", func(t *testing.T) {
		g := newUpdatesRig(t, baseUpdates())
		if body := g.do("GET", "/admin/settings/updates", "owner-token", nil, nil).Body.String(); !strings.Contains(body, marker) {
			t.Error("no chip on the Updates page")
		}
	})
	t.Run("editor never", func(t *testing.T) {
		g := newUpdatesRig(t, baseUpdates())
		if body := g.do("GET", "/admin/settings/general", "editor-token", nil, nil).Body.String(); strings.Contains(body, marker) {
			t.Error("editor sees the chip")
		}
		if g.svc.glanceGet != 0 {
			t.Errorf("service asked %d times for an editor", g.svc.glanceGet)
		}
	})
	t.Run("no service, no chip", func(t *testing.T) {
		g := newOwnerRig(t, true)
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, marker) {
			t.Error("chip without an update service")
		}
	})
	t.Run("service error, no chip", func(t *testing.T) {
		f := baseUpdates()
		f.viewErr = fmt.Errorf("down")
		g := newUpdatesRig(t, f)
		rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), marker) {
			t.Errorf("code %d or chip shown on error", rec.Code)
		}
	})
	t.Run("not on a POST render", func(t *testing.T) {
		g := newUpdatesRig(t, baseUpdates())
		g.do("POST", "/admin/settings/updates", "owner-token", goodUpdatesForm(), nil)
		if g.svc.glanceGet != 0 {
			t.Errorf("glance read on a non-GET (%d)", g.svc.glanceGet)
		}
	})
	t.Run("dev build", func(t *testing.T) {
		f := baseUpdates()
		f.state.Dev, f.state.Running = true, "dev"
		g := newUpdatesRig(t, f)
		body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, ">dev build<") || strings.Contains(body, "hm-updchip__blocks") {
			t.Error("dev chip should say dev build and show no blocks")
		}
	})
}

// The chip links under the instance prefix, not /admin.
func TestAdminPathUpdateChip(t *testing.T) {
	t.Parallel()

	g := newPathRig(t)
	g.a.deps.Updates = baseUpdates()
	body := g.do("GET", "/x-test/settings/general", "owner-token", nil, nil).Body.String()
	i := strings.Index(body, "hm-topbar__chip")
	if i < 0 || !strings.Contains(body[i:], `href="/x-test/settings/updates"`) || strings.Contains(body[i:], `href="/admin/`) {
		t.Errorf("chip link is not under /x-test")
	}
}

// The QA script shoots only banners that still exist (the major and
// off-for-90-days banners moved into the chip).
func TestUpdatesQAScriptsDropRemovedBanners(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"updates-shots.mjs", "updates-shots.sh"} {
		b, err := os.ReadFile("../../../scripts/admin-qa/" + f)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{"banner_major", "banner_off_90d"} {
			if strings.Contains(string(b), gone) {
				t.Errorf("%s still shoots the removed %s banner", f, gone)
			}
		}
	}
}
