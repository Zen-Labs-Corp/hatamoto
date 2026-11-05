package admin

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
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

func TestRetentionCardShowsCounts(t *testing.T) {
	t.Parallel()
	body := newBackupsRig(t, baseBackups()).get(t)
	for _, w := range []string{`id="retention"`, `action="/admin/settings/backups/retention"`, "Backups to keep", `name="manual" value="10"`, `name="pre_update" value="5"`, `name="scheduled" value="7"`} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}

func TestRetentionSaveNoDeleteSavesDirect(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows = okBackups(BackupManual, 3)
	f.retDeleted = map[string]int{}
	g := newBackupsRig(t, f)
	code, _, loc := postRetention(g, retentionForm("12", "5", "7"))
	if code != 303 || loc != backupsPath+"#retention" {
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
	for _, w := range []string{`name="confirm" value="1"`, `name="manual" value="2"`, `name="scheduled" value="7"`} {
		if !strings.Contains(body, w) {
			t.Errorf("confirm lacks %q", w)
		}
	}
	compareGolden(t, "pages/settings-backups-retention-confirm.html", normalizePage(body))

	// The confirmed post saves and prunes; it recounts, so a stale count changes nothing it did not show.
	code, _, loc := postRetention(g, retentionForm("2", "5", "7", "confirm", "1"))
	if code != 303 || loc != backupsPath+"#retention" {
		t.Fatalf("confirmed save = %d %s", code, loc)
	}
	if len(f.retSaved) != 1 || f.retSaved[0][BackupManual] != 2 {
		t.Fatalf("saved = %v", f.retSaved)
	}
	if len(g.events) != 1 || g.events[0].Detail["deleted"] != 4 {
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
	f.ret.Scheduled = 9
	f.ret.Locked = map[string]bool{BackupScheduled: true}
	f.ret.EnvVar = map[string]string{BackupScheduled: "HATAMOTO_BACKUP_KEEP_SCHEDULED"}
	f.state.Rows = okBackups(BackupScheduled, 9)
	g := newBackupsRig(t, f)

	body := g.get(t)
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
	if strings.Contains(body, "Backups to keep") || strings.Contains(body, `id="retention"`) {
		t.Error("the form shows without a RetentionService")
	}
	if !strings.Contains(body, "Keeping the last 10 manual") {
		t.Error("the Keeping line is gone")
	}
	if code, _, _ := postRetention(g, retentionForm("10", "5", "7")); code == 303 {
		t.Error("a save succeeded without a service")
	}
}

func TestRetentionFormOutsideLive(t *testing.T) {
	t.Parallel()
	f := baseBackups()
	f.state.Rows, f.state.Running = backupRows(), true
	body := newBackupsRig(t, f).get(t)
	form, live := strings.Index(body, `id="retention"`), strings.Index(body, `id="backups-live"`)
	end := strings.Index(body[live:], `<p class="hm-muted">Dates are in`)
	if form < 0 || live < 0 || end < 0 {
		t.Fatalf("markers missing: %d %d %d", form, live, end)
	}
	if form > live && form < live+end {
		t.Error("the retention form is inside #backups-live, so polling would replace it")
	}
}

func TestRetentionOffsite422KeepsCard(t *testing.T) {
	t.Parallel()
	g := newOffsiteRig(t, &fakeOffsite{view: defaultOffsiteView()})
	rec := g.do("POST", backupsPath+"/offsite", "owner-token", offsiteForm(url.Values{"keep": {"0"}}), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `id="retention"`) {
		t.Errorf("offsite 422 = %d, retention card kept = %v", rec.Code, strings.Contains(rec.Body.String(), `id="retention"`))
	}
}
