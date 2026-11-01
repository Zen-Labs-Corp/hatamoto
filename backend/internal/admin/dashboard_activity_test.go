package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMergeActivityOrdersCutsAndKeepsTies(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	audit := []activityEvent{{At: at, Who: "audit-tie"}, {At: at.Add(-time.Hour), Who: "audit-old"}}
	upd := []activityEvent{{At: at, Who: "update-tie"}, {At: at.Add(time.Hour), Who: "update-new"}}
	got := mergeActivity(3, audit, upd)
	var who []string
	for _, e := range got {
		who = append(who, e.Who)
	}
	if strings.Join(who, ",") != "update-new,audit-tie,update-tie" {
		t.Errorf("order %v: newest first, equal times in source order, cut at 3", who)
	}
	long := make([]activityEvent, 10)
	if n := len(mergeActivity(dashActivityN, long)); n != dashActivityN {
		t.Errorf("%d events, want %d", n, dashActivityN)
	}
}

func TestDashboardActivityMergesBothSources(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	fa := &fakeAuditLog{rows: []AuditRow{
		{ID: 2, At: at, Actor: "owner@example.com", Action: "entity.publish", Target: "id-1", Detail: map[string]any{"name": "Pricing"}},
		{ID: 1, At: at.Add(-48 * time.Hour), Actor: "owner@example.com", Action: "package.install", Target: "plugin:x"},
	}}
	fu := &fakeUpdates{state: UpdatesState{History: []UpdatesHistoryRow{
		{When: at.Add(-time.Hour), To: "1.4.3", Phase: "rolled_back"},
		{When: at.Add(-72 * time.Hour), To: "1.4.2", Phase: "manual_rollback"},
	}}}
	g := newRig(t, func(d *Deps) { d.AuditLog, d.Updates = fa, fu })
	res, err := g.a.dashActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fa.limit != dashActivityN || fa.offset != 0 {
		t.Errorf("audit window %d/%d", fa.limit, fa.offset)
	}
	if fa.recentCalls != 0 {
		t.Error("the Activity card counted the whole audit table")
	}
	rows := res.Sec.Activity
	if len(rows) != 4 {
		t.Fatalf("%d rows", len(rows))
	}
	want := [][3]string{
		{"owner@example.com", "Published", "Pricing"},
		{"Auto update", "Rolled back", "v1.4.3"},
		{"owner@example.com", "package.install", "plugin:x"},
		{"Auto update", "Rolled back by hand", "v1.4.2"},
	}
	for i, w := range want {
		r := rows[i]
		if r.Who != w[0] || r.Badge.Label != w[1] || r.Target != w[2] {
			t.Errorf("row %d = %q %q %q, want %v", i, r.Who, r.Badge.Label, r.Target, w)
		}
	}
	if rows[3].Badge.Kind != "neutral" || rows[1].Badge.Kind != "warning" {
		t.Error("history badges follow the Updates page: rollback warning, by hand neutral")
	}
	if res.Sec.More == nil || res.Sec.More.Href != "/admin/audit" {
		t.Errorf("more link %+v", res.Sec.More)
	}
}

func TestDashboardActivityOneSourceFailing(t *testing.T) {
	t.Parallel()

	at := time.Now()
	fa := &fakeAuditLog{rows: []AuditRow{{ID: 1, At: at, Actor: "o@example.com", Action: "entity.delete", Detail: map[string]any{"name": "Old"}}}}
	fu := &fakeUpdates{state: UpdatesState{History: []UpdatesHistoryRow{{When: at, To: "1.4.3", Phase: "success"}}}}

	g := newRig(t, func(d *Deps) { d.AuditLog, d.Updates = &fakeAuditLog{err: errors.New("db")}, fu })
	res, err := g.a.dashActivity(context.Background())
	if err != nil || len(res.Sec.Activity) != 1 || res.Sec.Activity[0].Who != "Auto update" {
		t.Errorf("audit failing: history must show: %+v %v", res.Sec, err)
	}
	fu.viewErr = errors.New("down")
	g = newRig(t, func(d *Deps) { d.AuditLog, d.Updates = fa, fu })
	res, err = g.a.dashActivity(context.Background())
	if err != nil || len(res.Sec.Activity) != 1 || res.Sec.Activity[0].Target != "Old" {
		t.Errorf("updates failing: audit must show: %+v %v", res.Sec, err)
	}
	g = newRig(t, func(d *Deps) { d.AuditLog, d.Updates = &fakeAuditLog{err: errors.New("db")}, fu })
	if _, err := g.a.dashActivity(context.Background()); err == nil {
		t.Error("both failing must be an error (the card shows its error state)")
	}
	g = newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{err: errors.New("db")} })
	if _, err := g.a.dashActivity(context.Background()); err == nil {
		t.Error("the only source failing must be an error")
	}
}

func TestDashboardActivityWiring(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	if g.a.activityJob() != nil {
		t.Error("no source wired: no card")
	}
	g = newRig(t, func(d *Deps) { d.Updates = &fakeUpdates{} })
	res, err := g.a.dashActivity(context.Background())
	if err != nil || res.Sec.State != dashEmpty || res.Sec.More != nil {
		t.Errorf("updates only, no history: want an empty card without the Audit log link: %+v %v", res.Sec, err)
	}
	for _, j := range g.a.dashJobs("editor") {
		if j.Name == "activity" {
			t.Error("editors must not get the activity card")
		}
	}
	owner := false
	for _, j := range g.a.dashJobs("owner") {
		owner = owner || j.Name == "activity"
	}
	if !owner {
		t.Error("owners get the activity card")
	}
}

func TestDashboardActivityLinksUseTheAdminPath(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{rows: auditRows(2)} })
	g.a.adminPath = "/cms"
	res, err := g.a.dashActivity(context.Background())
	if err != nil || res.Sec.More.Href != "/cms/audit" {
		t.Errorf("%+v %v", res.Sec.More, err)
	}
}
