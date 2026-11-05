package admin

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// okBackups is n finished backups of one kind, newest first.
func okBackups(kind string, n int) []BackupInfo {
	var rows []BackupInfo
	for i := 0; i < n; i++ {
		at := time.Date(2026, 10, 20-i, 3, 0, 0, 0, time.UTC)
		rows = append(rows, BackupInfo{ID: at.Format("20060102T150405Z") + "-" + kind, Kind: kind, State: BackupOK, Size: 1 << 20, Created: at})
	}
	return rows
}

func retentionForm(manual, pre, sched string, extra ...string) url.Values {
	f := url.Values{"manual": {manual}, "pre_update": {pre}, "scheduled": {sched}}
	for i := 0; i+1 < len(extra); i += 2 {
		f.Set(extra[i], extra[i+1])
	}
	return f
}

func postRetention(g *backupsRig, f url.Values) (code int, body string, loc string) {
	rec := g.do("POST", backupsPath+"/retention", "owner-token", f, nil)
	return rec.Code, rec.Body.String(), rec.Header().Get("Location")
}

func (g *backupsRig) getRetention(t *testing.T) string {
	t.Helper()
	rec := g.do("GET", backupsPath+"/retention", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET retention = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestRetentionCardShowsCounts(t *testing.T) {
	t.Parallel()
	g := newBackupsRig(t, baseBackups())
	body := g.getRetention(t)
	for _, w := range []string{`id="retention-form"`, `action="/admin/settings/backups/retention"`, "id=\"retention-title\">Backups to keep</h2>", `name="manual" value="10"`, `name="pre_update" value="5"`, `name="scheduled" value="7"`,
		`type="submit"`, `href="/admin/settings/backups"`, "Cancel", `aria-label="Breadcrumb"`} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	// the trail leads back to Settings > Backups & recovery > Backups and ends on this page
	if i, j := strings.Index(body, `>Backups</a>`), strings.LastIndex(body, "Backups to keep"); i < 0 || i > j {
		t.Errorf("breadcrumb does not lead through Backups (%d, %d)", i, j)
	}
	compareGolden(t, "pages/settings-backups-retention.html", normalizePage(body))

	list := g.get(t)
	for _, w := range []string{`href="/admin/settings/backups/retention"`, `<span class="hm-menu__hint">How many of each kind stay</span>`, "Backups to keep"} {
		if !strings.Contains(list, w) {
			t.Errorf("Backups page lacks %q", w)
		}
	}
	for _, x := range []string{`id="retention"`, `id="retention-form"`} {
		if strings.Contains(list, x) {
			t.Errorf("Backups page has %q", x)
		}
	}
	if !strings.Contains(list, "Keeping the last 10 manual, 5 pre-update and 7 scheduled backups.") {
		t.Error("the Keeping line changed")
	}
}

func TestRetentionPageOwnerOnly(t *testing.T) {
	t.Parallel()
	g := newBackupsRig(t, baseBackups())
	for _, m := range []string{"GET", "POST"} {
		if rec := g.do(m, backupsPath+"/retention", "editor-token", retentionForm("10", "5", "7"), nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if rec := g.do("GET", backupsPath+"/retention", "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous = %d, want 303", rec.Code)
	}
}

func TestRetentionSaveNoDeleteSavesDirect(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows = okBackups(BackupManual, 3)
	f.retDeleted = map[string]int{}
	g := newBackupsRig(t, f)
	code, _, loc := postRetention(g, retentionForm("12", "5", "7"))
	if code != 303 || loc != backupsPath {
		t.Fatalf("save = %d %s", code, loc)
	}
	if len(f.retSaved) != 1 || f.retSaved[0][BackupManual] != 12 || len(f.retSaved[0]) != 3 {
		t.Fatalf("saved = %v", f.retSaved)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.retention" || g.events[0].Detail["manual"] != 12 {
		t.Fatalf("audit = %+v", g.events)
	}
	if auditLabel("backups.retention") == "backups.retention" {
		t.Error("backups.retention has no label")
	}
}

func TestRetentionSaveConfirmFlow(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows = append(okBackups(BackupManual, 6), okBackups(BackupScheduled, 2)...)
	f.retDeleted = map[string]int{BackupManual: 4}
	g := newBackupsRig(t, f)

	code, body, _ := postRetention(g, retentionForm("2", "5", "7"))
	if code != 200 || !strings.Contains(body, "This deletes 4 manual backups (the oldest).") || !strings.Contains(body, "Delete and save") {
		t.Fatalf("confirm = %d\n%s", code, body)
	}
	if len(f.retSaved) != 0 || len(g.events) != 0 {
		t.Fatalf("nothing may be saved before the confirm: %v %v", f.retSaved, g.events)
	}
	for _, w := range []string{`name="confirm" value="1"`, `name="expect_manual" value="4"`, `name="manual" value="2"`, `name="scheduled" value="7"`} {
		if !strings.Contains(body, w) {
			t.Errorf("confirm lacks %q", w)
		}
	}
	if !strings.Contains(body, `href="/admin/settings/backups/retention"`) {
		t.Error("confirm Back does not lead to the retention page")
	}
	compareGolden(t, "pages/settings-backups-retention-confirm.html", normalizePage(body))

	// Rows change after the confirm page was shown: the confirmed post must not delete, it shows the new count.
	f.state.Rows = append(okBackups(BackupManual, 8), okBackups(BackupScheduled, 2)...)
	code, body, _ = postRetention(g, retentionForm("2", "5", "7", "confirm", "1", "expect_manual", "4"))
	if code != 200 || !strings.Contains(body, "This deletes 6 manual backups (the oldest).") || !strings.Contains(body, `name="expect_manual" value="6"`) {
		t.Fatalf("stale confirm = %d\n%s", code, body)
	}
	if len(f.retSaved) != 0 || len(g.events) != 0 {
		t.Fatalf("a stale confirm saved: %v %v", f.retSaved, g.events)
	}
	f.state.Rows = append(okBackups(BackupManual, 6), okBackups(BackupScheduled, 2)...)

	// The confirmed post, with the counts shown, saves and prunes.
	code, _, loc := postRetention(g, retentionForm("2", "5", "7", "confirm", "1", "expect_manual", "4"))
	if code != 303 || loc != backupsPath {
		t.Fatalf("confirmed save = %d %s", code, loc)
	}
	if len(f.retSaved) != 1 || f.retSaved[0][BackupManual] != 2 {
		t.Fatalf("saved = %v", f.retSaved)
	}
	if len(g.events) != 1 || g.events[0].Detail["deleted"] != 4 {
		t.Fatalf("audit = %+v", g.events)
	}
}

func TestRetentionPruneFailureAfterSaveIsAuditedAndFlashed(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows = okBackups(BackupManual, 3)
	f.retDeleted = map[string]int{BackupManual: 1}
	f.retErr = fmt.Errorf("%w: disk error", ErrRetentionPrune)
	g := newBackupsRig(t, f)
	code, _, loc := postRetention(g, retentionForm("2", "5", "7", "confirm", "1", "expect_manual", "1"))
	if code != 303 || loc != backupsPath {
		t.Fatalf("save = %d %s, want the redirect back with a flash", code, loc)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.retention" || g.events[0].Detail["error"] != "prune_failed" || g.events[0].Detail["deleted"] != 1 {
		t.Fatalf("audit = %+v", g.events)
	}
}

func TestRetentionDeferredWhileBackupRunsIsAuditedAndWarned(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows = okBackups(BackupManual, 3)
	f.retDeleted = map[string]int{}
	f.retErr = ErrRetentionDeferred
	g := newBackupsRig(t, f)
	code, _, loc := postRetention(g, retentionForm("2", "5", "7", "confirm", "1", "expect_manual", "1"))
	if code != 303 || loc != backupsPath {
		t.Fatalf("save = %d %s", code, loc)
	}
	if len(f.retSaved) != 1 {
		t.Fatalf("saved = %v", f.retSaved)
	}
	d := g.events[0].Detail
	if len(g.events) != 1 || d["deferred"] != true || d["deleted"] != 0 || d["error"] != nil {
		t.Fatalf("audit = %+v", g.events)
	}
}

func TestRetentionConfirmCountsEachKindAndIgnoresFailed(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	rows := append(okBackups(BackupManual, 3), okBackups(BackupPreUpdate, 2)...)
	rows = append([]BackupInfo{{ID: "20261021T000000Z-manual", Kind: BackupManual, State: BackupFailed}}, rows...)
	f.state.Rows = rows
	_, body, _ := postRetention(newBackupsRig(t, f), retentionForm("2", "1", "7"))
	if !strings.Contains(body, "This deletes 1 manual backup, 1 pre-update backup (the oldest).") {
		t.Errorf("body = %s", body)
	}
}

func TestRetentionValidation422(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"0", "51", "x", ""} {
		t.Run("value "+bad, func(t *testing.T) {
			f := baseBackups()
			g := newBackupsRig(t, f)
			code, body, _ := postRetention(g, retentionForm("10", bad, "7"))
			if code != 422 || !strings.Contains(body, "Choose a number from 1 to 50.") {
				t.Fatalf("code = %d", code)
			}
			if !strings.Contains(body, "id=\"retention-title\">Backups to keep</h2>") || !strings.Contains(body, "Cancel") {
				t.Error("the 422 is not the Backups to keep page")
			}
			if bad != "" && !strings.Contains(body, fmt.Sprintf(`name="pre_update" value=%q`, bad)) {
				t.Errorf("input not echoed")
			}
			if !strings.Contains(body, `name="manual" value="10"`) {
				t.Error("the other fields are lost")
			}
			if len(f.retSaved) != 0 {
				t.Error("saved invalid counts")
			}
		})
	}
}

func TestRetentionLockedIgnored(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.ret.Scheduled, f.state.KeepScheduled = 9, 9
	f.ret.Locked = map[string]bool{BackupScheduled: true}
	f.ret.EnvVar = map[string]string{BackupScheduled: "HATAMOTO_BACKUP_KEEP_SCHEDULED"}
	f.state.Rows = okBackups(BackupScheduled, 9)
	g := newBackupsRig(t, f)

	body := g.getRetention(t)
	for _, w := range []string{"Set by HATAMOTO_BACKUP_KEEP_SCHEDULED. Change it in the server config.", `readonly`, `name="scheduled" value="9"`} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
	compareGolden(t, "pages/settings-backups-retention-locked.html", normalizePage(body))

	// A crafted post cannot lower or corrupt the locked kind.
	code, _, _ := postRetention(g, retentionForm("11", "5", "1"))
	if code != 303 {
		t.Fatalf("save = %d", code)
	}
	if len(f.retSaved) != 1 {
		t.Fatalf("saved = %v", f.retSaved)
	}
	if _, ok := f.retSaved[0][BackupScheduled]; ok || f.retSaved[0][BackupManual] != 11 {
		t.Errorf("saved = %v", f.retSaved[0])
	}
	if code, _, _ := postRetention(g, retentionForm("11", "5", "junk")); code != 303 {
		t.Errorf("a locked kind is not validated: %d", code)
	}
}

func TestRetentionNonServiceHidesForm(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	g := newBackupsRig(t, f)
	g.a.deps.Backups = struct{ BackupsService }{f}
	body := g.get(t)
	if strings.Contains(body, "Backups to keep") || strings.Contains(body, `id="retention`) || strings.Contains(body, "hm-overflow") {
		t.Error("the retention form or menu item shows without a RetentionService")
	}
	if !strings.Contains(body, "Keeping the last 10 manual") {
		t.Error("the Keeping line is gone")
	}
	if rec := g.do("GET", backupsPath+"/retention", "owner-token", nil, nil); rec.Code != 503 || !strings.Contains(rec.Body.String(), "Backup retention is not available") {
		t.Errorf("GET retention = %d", rec.Code)
	}
	if code, _, _ := postRetention(g, retentionForm("10", "5", "7")); code == 303 {
		t.Error("a save succeeded without a service")
	}
}

func TestRetentionFormNotOnBackupsPage(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows, f.state.Running = backupRows(), true
	if body := newBackupsRig(t, f).get(t); strings.Contains(body, `id="retention`) {
		t.Error("the Backups page, whose live panel polls, has the retention form")
	}
	g := newOffsiteRig(t, &fakeOffsite{view: defaultOffsiteView()})
	rec := g.do("POST", backupsPath+"/offsite", "owner-token", offsiteForm(url.Values{"keep": {"0"}}), nil)
	if rec.Code != 422 || strings.Contains(rec.Body.String(), `id="retention`) {
		t.Errorf("offsite 422 = %d, has retention form = %v", rec.Code, strings.Contains(rec.Body.String(), `id="retention`))
	}
}

func TestRetentionSaveHiddenWhenAllLocked(t *testing.T) {
	t.Parallel()
	all := map[string]bool{BackupManual: true, BackupPreUpdate: true, BackupScheduled: true}
	env := map[string]string{BackupManual: "A", BackupPreUpdate: "B", BackupScheduled: "C"}
	d := retentionCard(RetentionView{Manual: 1, PreUpdate: 1, Scheduled: 1, Locked: all, EnvVar: env}, nil, nil)
	if !d.AllLocked {
		t.Error("AllLocked = false with every kind locked")
	}
	delete(all, BackupScheduled)
	if retentionCard(RetentionView{Locked: all, EnvVar: env}, nil, nil).AllLocked {
		t.Error("AllLocked = true with one kind open")
	}

	f := baseBackups()
	f.ret.Locked, f.ret.EnvVar = map[string]bool{BackupManual: true, BackupPreUpdate: true, BackupScheduled: true}, env
	g := newBackupsRig(t, f)
	if body := g.getRetention(t); strings.Contains(body, ">Save<") || !strings.Contains(body, "Cancel") {
		t.Errorf("all locked: Save should be gone, Cancel kept:\n%s", body)
	}
}
