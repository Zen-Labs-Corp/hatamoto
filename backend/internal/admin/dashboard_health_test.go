package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/form"
)

func TestBackupCheckThresholds(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour
	for _, tc := range []struct {
		name  string
		b     BackupInfo
		age   time.Duration
		level string
	}{
		{"fresh", BackupInfo{State: BackupOK}, time.Hour, healthOK},
		{"two days", BackupInfo{State: BackupOK}, 2 * day, healthOK},
		{"just over two days", BackupInfo{State: BackupOK}, 2*day + time.Minute, healthWarn},
		{"seven days", BackupInfo{State: BackupOK}, 7 * day, healthWarn},
		{"just over seven days", BackupInfo{State: BackupOK}, 7*day + time.Minute, healthBad},
		{"fresh but failed", BackupInfo{State: BackupFailed}, time.Minute, healthBad},
	} {
		if got := backupCheck(&healthCheck{}, tc.b, tc.age); got.Level != tc.level {
			t.Errorf("%s: level %q, want %q", tc.name, got.Level, tc.level)
		}
	}
}

// healthOf runs the card and returns its items by label, with the summary.
func healthOf(t *testing.T, g *rig) (map[string]healthItem, string) {
	t.Helper()
	res, err := g.a.dashHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]healthItem{}
	if res.Sec == nil {
		return items, ""
	}
	for _, grp := range res.Sec.Health {
		for _, it := range grp.Items {
			items[it.Label] = it
		}
	}
	return items, res.Sec.Description
}

func TestDashboardHealthDiskAndBackup(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		free   uint64
		level  string
		kind   string
		linked bool
	}{
		{"plenty", 50 << 30, healthOK, "success", false},
		{"five GiB", 5 << 30, healthOK, "success", false},
		{"under five", 5<<30 - 1, healthWarn, "warning", true},
		{"one GiB", 1 << 30, healthWarn, "warning", true},
		{"under one", 1<<30 - 1, healthBad, "danger", true},
	} {
		fb := &fakeBackups{state: BackupsState{FreeBytes: tc.free, Rows: []BackupInfo{{State: BackupOK, Created: time.Now().Add(-time.Hour)}}}}
		g := newRig(t, func(d *Deps) { d.Backups = fb })
		items, _ := healthOf(t, g)
		it := items["Free disk"]
		if it.Badge.Kind != tc.kind || (it.Href != "") != tc.linked {
			t.Errorf("%s: %+v", tc.name, it)
		}
		if tc.linked && it.Href != "/admin/settings/backups" {
			t.Errorf("%s: href %q", tc.name, it.Href)
		}
	}
	// A failed latest backup is red even with an older good one; a running one is skipped.
	fb := &fakeBackups{state: BackupsState{Rows: []BackupInfo{
		{State: BackupRunning, Created: time.Now()},
		{State: BackupFailed, Created: time.Now().Add(-time.Hour)},
		{State: BackupOK, Created: time.Now().Add(-24 * time.Hour)},
	}}}
	items, summary := healthOf(t, newRig(t, func(d *Deps) { d.Backups = fb }))
	if it := items["Last backup"]; it.Badge.Kind != "danger" || it.Badge.Label != "Failed" || it.Href == "" {
		t.Errorf("last backup %+v", it)
	}
	if _, ok := items["Free disk"]; ok {
		t.Error("unknown free space (0) must leave the disk check out")
	}
	if summary != "0 of 1 passing" {
		t.Errorf("summary %q", summary)
	}
	items, _ = healthOf(t, newRig(t, func(d *Deps) { d.Backups = &fakeBackups{} }))
	if it := items["Last backup"]; it.Badge.Label != "No backups yet" || it.Badge.Kind != "warning" {
		t.Errorf("no backups: %+v", it)
	}
}

func TestDashboardHealthOmitsWhatIsNotWired(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if j := g.a.healthJob(); j != nil {
		t.Error("no store wired: no job")
	}
	res, err := g.a.dashHealth(context.Background())
	if err != nil || res.Sec != nil {
		t.Errorf("no checks: want no card, got %+v, %v", res.Sec, err)
	}
	g = newRig(t, func(d *Deps) { d.Webhooks, d.WebhookDeliveryOn = &fakeWebhooks{}, true })
	items, summary := healthOf(t, g)
	if len(items) != 1 || summary != "1 of 1 passing" {
		t.Errorf("only the webhook check is wired: %v %q", items, summary)
	}
}

func TestDashboardHealthOneFailingCheckIsLeftOut(t *testing.T) {
	t.Parallel()

	fb := &fakeBackups{listErr: errors.New("disk gone")}
	fw := &fakeWebhooks{failedSince: 0}
	g := newRig(t, func(d *Deps) { d.Backups, d.Webhooks, d.WebhookDeliveryOn = fb, fw, true })
	items, summary := healthOf(t, g)
	if _, ok := items["Last backup"]; ok {
		t.Error("a failing check must be omitted")
	}
	if _, ok := items["Free disk"]; ok {
		t.Error("a failing check must be omitted")
	}
	if it := items["Webhooks"]; it.Badge.Kind != "success" || summary != "1 of 1 passing" {
		t.Errorf("webhooks %+v, summary %q", it, summary)
	}
	// All of them failing: no card, and no error card.
	fw.failedErr = errors.New("db")
	if res, err := g.a.dashHealth(context.Background()); err != nil || res.Sec != nil {
		t.Errorf("all failing: %+v, %v", res.Sec, err)
	}
}

func TestDashboardHealthWebhooks(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		failed  int
		on      bool
		value   string
		kind    string
		hrefEnd string
	}{
		{"none", 0, true, "None failed this week", "success", ""},
		{"failed", 3, true, "3 failed this week", "warning", "/settings/webhooks/deliveries"},
		{"delivery off", 0, false, "Delivery is off", "warning", "/settings/webhooks"},
	} {
		fw := &fakeWebhooks{failedSince: tc.failed}
		g := newRig(t, func(d *Deps) { d.Webhooks, d.WebhookDeliveryOn = fw, tc.on })
		items, _ := healthOf(t, g)
		it := items["Webhooks"]
		if it.Badge.Label != tc.value || it.Badge.Kind != tc.kind || !strings.HasSuffix(it.Href, tc.hrefEnd) || (tc.hrefEnd != "") != (it.Href != "") {
			t.Errorf("%s: %+v", tc.name, it)
		}
		if d := time.Since(fw.failedAsked); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
			t.Errorf("%s: asked for failures since %v ago, want 7 days", tc.name, d)
		}
	}
}

func TestDashboardHealthSpamPiggybacksOnTheSubmissionsScan(t *testing.T) {
	t.Parallel()

	ff := newFakeForms()
	contact := ff.addForm("Contact", contactFields...)
	now := time.Now()
	for i := range 4 {
		ff.addSub(contact, form.SubmissionStatusSpam, now.Add(-time.Duration(i+1)*time.Hour), map[string]any{"name": "Bot"})
	}
	ff.addSub(contact, form.SubmissionStatusSpam, now.Add(-10*24*time.Hour), map[string]any{"name": "Old bot"}) // before this week
	ff.addSub(contact, form.SubmissionStatusNew, now.Add(-time.Hour), map[string]any{"name": "Real"})
	g := newRig(t, func(d *Deps) { d.Growth.Forms = ff })
	ff.listCalls = 0
	v := g.a.buildDash(context.Background(), []dashJob{*g.a.healthJob(), *g.a.submissionsJob()})
	if ff.listCalls != 1 {
		t.Errorf("the forms were read %d times, want one shared scan", ff.listCalls)
	}
	var health *dashSection
	for _, s := range v.Sections {
		if s.ID == "dash-health" {
			health = s
		}
	}
	if health == nil || health.Health[0].Items[0].Badge.Label != "4 caught this week" {
		t.Fatalf("health card: %+v", health)
	}
	if v.Stats[0].Value != "1" {
		t.Errorf("submissions value %q", v.Stats[0].Value)
	}
}

// errOverview is agent stats whose verified-fetch read fails.
type errOverview struct{ *fakeAgentStats }

func (errOverview) Overview(context.Context, agents.Range) (agents.OverviewTotals, error) {
	return agents.OverviewTotals{}, errors.New("rollup down")
}

func TestDashboardHealthCrawlerChecks(t *testing.T) {
	t.Parallel()

	fs := &fakeAgentStats{}
	g := newRig(t, func(d *Deps) { d.Growth.Agents = fs })
	items, _ := healthOf(t, g)
	// Overview 30 of 40 decided; health 150 hits, 11 failed (7.3%); chatgpt-user ignores robots.txt.
	if it := items["Verified fetches"]; it.Badge.Label != "75%" || it.Href != "" {
		t.Errorf("verified %+v", it)
	}
	if it := items["Errors served"]; it.Badge.Label != "7.3%" || it.Badge.Kind != "warning" || it.Href != "/admin/analytics/health#w-h-fail" {
		t.Errorf("errors %+v", it)
	}
	if it := items["robots.txt"]; it.Badge.Label != "1 agent ignores it" || it.Badge.Kind != "warning" || it.Href == "" {
		t.Errorf("robots %+v", it)
	}
	if it := items["Published URLs"]; it.Badge.Label != "120" {
		t.Errorf("published %+v", it)
	}
	fs.health = []agents.HealthDay{{Day: day(0), Hits: 100, S2xx: 80, S5xx: 20}}
	if it, _ := healthOf(t, g); it["Errors served"].Badge.Kind != "danger" {
		t.Errorf("20%% errors must be red: %+v", it["Errors served"])
	}
	fs.health = []agents.HealthDay{{Day: day(0), Hits: 100, S2xx: 99, S4xx: 1}}
	if it, _ := healthOf(t, g); it["Errors served"].Badge.Kind != "success" {
		t.Errorf("1%% errors is fine: %+v", it["Errors served"])
	}
	fs.health = []agents.HealthDay{}
	if it, _ := healthOf(t, g); it["Errors served"].Badge.Label != "" {
		t.Errorf("no fetches: no errors check: %+v", it["Errors served"])
	}
	g = newRig(t, func(d *Deps) { d.Growth.Agents = errOverview{&fakeAgentStats{}} })
	items, _ = healthOf(t, g)
	if _, ok := items["Verified fetches"]; ok {
		t.Error("a failing read must leave the check out")
	}
	if _, ok := items["Errors served"]; !ok {
		t.Error("the other crawler checks must survive")
	}
}

func TestDashboardHealthPluginsAndUpdates(t *testing.T) {
	t.Parallel()

	fp := &fakePlugins{list: []PluginInfo{{Name: "a", Loaded: true}, {Name: "b", Loaded: true}, {Name: "c", Error: "boom"}}}
	fu := &fakeUpdates{state: UpdatesState{Running: "1.4.2", Status: UpdatesStatus{Decision: updUpToDate}}}
	g := newRig(t, func(d *Deps) { d.Growth.Plugins, d.Updates = fp, fu })
	items, _ := healthOf(t, g)
	if it := items["Plugins"]; it.Badge.Label != "2 active, 1 in error" || it.Badge.Kind != "warning" || it.Href != "/admin/plugins" {
		t.Errorf("plugins %+v", it)
	}
	if it := items["Update check"]; it.Badge.Label != "Up to date" || it.Badge.Kind != "success" {
		t.Errorf("update check %+v", it)
	}
	fu.state.Status = UpdatesStatus{Decision: updBlocked, Reason: "no space"}
	if it, _ := healthOf(t, g); it["Update check"].Href != "/admin/settings/updates" || it["Update check"].Badge.Kind == "success" {
		t.Errorf("a blocked update check must link to the settings: %+v", it["Update check"])
	}
	fu.state.Status = UpdatesStatus{}
	if it, _ := healthOf(t, g); it["Update check"].Badge.Label != "" {
		t.Error("before the first check there is nothing to report")
	}
	fu.state.Dev = true
	if it, _ := healthOf(t, g); it["Update check"].Badge.Label != "" {
		t.Error("a dev build has no update check")
	}
	fp.list = nil
	if it, _ := healthOf(t, g); it["Plugins"].Badge.Label != "" {
		t.Error("no plugins installed: no check")
	}
}

func TestDashboardHealthIsOwnerOnly(t *testing.T) {
	t.Parallel()

	fb := &fakeBackups{state: BackupsState{FreeBytes: 9 << 30}}
	g := newRig(t, func(d *Deps) { d.Backups = fb })
	for _, j := range g.a.dashJobs("editor") {
		if j.Name == "site health" {
			t.Error("editors must not get the health card")
		}
	}
	owner := false
	for _, j := range g.a.dashJobs("owner") {
		owner = owner || j.Name == "site health"
	}
	if !owner {
		t.Error("owners get the health card")
	}
}
