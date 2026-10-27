package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakeBackups is a BackupsService with a fixed state and call records.
type fakeBackups struct {
	state   BackupsState
	listErr error

	startID   string
	startErr  error
	starts    []string // the actor of each Start
	archive   string
	opened    []string
	deleted   []string
	deleteErr error
}

type nopSeekCloser struct{ io.ReadSeeker }

func (nopSeekCloser) Close() error { return nil }

func (f *fakeBackups) List(context.Context) (BackupsState, error) { return f.state, f.listErr }
func (f *fakeBackups) Start(_ context.Context, actor string) (string, error) {
	f.starts = append(f.starts, actor)
	return f.startID, f.startErr
}
func (f *fakeBackups) Open(_ context.Context, id string) (io.ReadSeekCloser, BackupInfo, error) {
	f.opened = append(f.opened, id)
	for _, b := range f.state.Rows {
		if b.ID == id && b.State == BackupOK {
			return nopSeekCloser{strings.NewReader(f.archive)}, b, nil
		}
	}
	return nil, BackupInfo{}, ErrBackupNotFound
}
func (f *fakeBackups) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func baseBackups() *fakeBackups {
	return &fakeBackups{state: BackupsState{KeepManual: 5, KeepPreUpdate: 2, FreeBytes: 50 << 30}, startID: "20261019T091200Z-manual", archive: "TARDATA"}
}

var (
	backupContent = []string{"core.dump", "plugin_webhooks.dump", "content.tar.gz", "manifest.json"}
	backupPlugins = []string{"searchconsole", "webhooks"}
)

func backupRows() []BackupInfo {
	return []BackupInfo{
		{ID: "20261019T091200Z-manual", Kind: BackupManual, State: BackupRunning, Percent: 62, Created: time.Date(2026, 10, 18, 22, 12, 0, 0, time.UTC)},
		{ID: "20261018T141900Z-pre_update", Kind: BackupPreUpdate, Target: "v0.26.99", State: BackupOK, Size: 30 << 20,
			Created: time.Date(2026, 10, 18, 3, 19, 0, 0, time.UTC), Parts: backupContent, Plugins: backupPlugins},
		{ID: "20261017T080000Z-manual", Kind: BackupManual, State: BackupFailed, Error: "not enough free disk space", Created: time.Date(2026, 10, 16, 21, 0, 0, 0, time.UTC)},
	}
}

type backupsRig struct {
	*ownerRig
	svc    *fakeBackups
	events []AuditEvent
}

func newBackupsRig(t *testing.T, svc *fakeBackups) *backupsRig {
	t.Helper()
	g := newOwnerRig(t, true)
	g.a.deps.Site.(*fakeSite).cfg.Timezone = "Australia/Sydney"
	if svc != nil {
		g.a.deps.Backups = svc
	}
	b := &backupsRig{ownerRig: g, svc: svc}
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { b.events = append(b.events, e) }
	return b
}

func (b *backupsRig) get(t *testing.T) string {
	t.Helper()
	rec := b.do("GET", backupsPath, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestBackupsPageStates(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		mut     func(f *fakeBackups)
		wants   []string
		absents []string
	}{
		{"empty", func(f *fakeBackups) {}, []string{
			"No backups yet.", "A backup holds secrets: password hashes, API keys and plugin credentials.",
			"Keeping the last 5 manual and 2 pre-update backups.", "Free disk: 50 GB.", "Australia/Sydney",
		}, []string{"/download\"", "Delete backup"}},
		{"list", func(f *fakeBackups) { f.state.Rows = backupRows()[1:] }, []string{
			"18 Oct 2026 14:19", "17 Oct 2026 08:00", "Manual", "Before update to v0.26.99",
			"Database, Pages, Media", "2 plugins", "30 MB", ">OK<", ">Failed<", "Back up now", `action="/admin/settings/backups"`,
			"not enough free disk space", `href="/admin/settings/backups/20261018T141900Z-pre_update/download"`,
		}, []string{"/20261017T080000Z-manual/download", "hx-trigger", "Running"}},
		{"running", func(f *fakeBackups) { f.state.Rows, f.state.Running = backupRows(), true }, []string{
			"19 Oct 2026 09:12", "Running 62%", "A backup is running.", `Backup progress</span><span class="hm-meter__value">62%</span>`, `width="62%"`, `hx-get="/admin/settings/backups?poll=running"`,
			`hx-trigger="every 3s"`, `<noscript><meta http-equiv="refresh" content="3"></noscript>`, ">OK<", ">Failed<",
		}, []string{"Back up now", `id="backup-form"`, "/20261019T091200Z-manual/delete"}},
		{"unknown_disk", func(f *fakeBackups) { f.state.FreeBytes = 0 }, []string{"Keeping the last"}, []string{"Free disk"}},
		{"escapes_plugins", func(f *fakeBackups) {
			f.state.Rows = []BackupInfo{{ID: "20261018T141900Z-manual", Kind: BackupManual, State: BackupOK, Size: 1 << 20,
				Created: time.Date(2026, 10, 18, 3, 19, 0, 0, time.UTC), Parts: backupContent, Plugins: []string{"<b>x</b>"}}}
		}, []string{"1 plugin"}, []string{"<b>x</b>", "x</b>"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := baseBackups()
			c.mut(f)
			body := newBackupsRig(t, f).get(t)
			for _, w := range c.wants {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, x := range c.absents {
				if strings.Contains(body, x) {
					t.Errorf("page has %q", x)
				}
			}
			if c.name != "escapes_plugins" {
				compareGolden(t, "pages/settings-backups-"+c.name+".html", normalizePage(body))
			}
		})
	}
}

func TestBackupsPageUnavailableAndErrors(t *testing.T) {
	t.Parallel()

	g := newBackupsRig(t, nil)
	if rec := g.do("GET", backupsPath, "owner-token", nil, nil); rec.Code != 503 || !strings.Contains(rec.Body.String(), "Backups is not available") {
		t.Errorf("nil service: %d", rec.Code)
	}
	f := baseBackups()
	f.listErr = io.ErrUnexpectedEOF
	g = newBackupsRig(t, f)
	if rec := g.do("GET", backupsPath, "owner-token", nil, nil); rec.Code != 500 {
		t.Errorf("list error: %d", rec.Code)
	}
}

func TestBackupsPageOwnerOnly(t *testing.T) {
	t.Parallel()

	g := newBackupsRig(t, baseBackups())
	if rec := g.do("GET", backupsPath, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor = %d, want 403", rec.Code)
	}
	if rec := g.do("GET", backupsPath, "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous = %d, want 303", rec.Code)
	}
}

func TestBackupsPollMovesThePageOn(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	f.state.Rows, f.state.Running = backupRows(), true
	g := newBackupsRig(t, f)
	htmx := map[string]string{"HX-Request": "true"}
	if rec := g.do("GET", backupsPath+"?poll=running", "owner-token", nil, htmx); rec.Code != 200 {
		t.Errorf("poll while still running = %d", rec.Code)
	}
	f.state.Running = false
	rec := g.do("GET", backupsPath+"?poll=running", "owner-token", nil, htmx)
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != backupsPath {
		t.Errorf("poll after finish = %d, HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	// A plain browser request with ?poll just renders the page.
	if rec := g.do("GET", backupsPath+"?poll=running", "owner-token", nil, nil); rec.Code != 200 {
		t.Errorf("plain GET with poll = %d", rec.Code)
	}
}

func TestBackupsStart(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	g := newBackupsRig(t, f)
	rec := g.do("POST", backupsPath, "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != backupsPath {
		t.Fatalf("start = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.starts) != 1 || f.starts[0] == "" {
		t.Errorf("Start calls: %v", f.starts)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.create" || g.events[0].Target != "20261019T091200Z-manual" {
		t.Errorf("audit: %+v", g.events)
	}
	if body := followFlashTo(g.ownerRig, backupsPath, rec); !strings.Contains(body, "Backup started") {
		t.Error("no started flash")
	}

	// A second start while one runs is refused with a warning and no audit.
	f.startErr = ErrBackupRunning
	rec = g.do("POST", backupsPath, "owner-token", url.Values{}, nil)
	if rec.Code != 303 {
		t.Fatalf("second start = %d", rec.Code)
	}
	if body := followFlashTo(g.ownerRig, backupsPath, rec); !strings.Contains(body, "A backup is already running.") {
		t.Error("no already-running flash")
	}
	if len(g.events) != 1 {
		t.Errorf("a refused start was audited: %+v", g.events)
	}
}

// followFlashTo loads path with the flash cookie a redirect set.
func followFlashTo(g *ownerRig, path string, rec interface{ Result() *http.Response }) string {
	req := httptestReq("GET", path, "owner-token")
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return serve(g.h, req, nil).Body.String()
}

func TestBackupsDownload(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	f.state.Rows = backupRows()
	g := newBackupsRig(t, f)
	const id = "20261018T141900Z-pre_update"
	rec := g.do("GET", backupsPath+"/"+id+"/download", "owner-token", nil, nil)
	if rec.Code != 200 || rec.Body.String() != "TARDATA" {
		t.Fatalf("download = %d %q", rec.Code, rec.Body.String())
	}
	for k, want := range map[string]string{
		"Content-Type": "application/x-tar", "Content-Disposition": `attachment; filename="` + id + `.tar"`,
		"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.download" || g.events[0].Target != id {
		t.Errorf("audit: %+v", g.events)
	}
	f.opened = nil
	for _, bad := range []string{"..%2F..%2Fetc%2Fpasswd", "../etc/passwd", "20261018T141900Z-pre_update.tar", "x", "20261018T141900Z-weekly", "20261001T000000Z-manual"} {
		rec := g.do("GET", backupsPath+"/"+bad+"/download", "owner-token", nil, nil)
		// The mux cleans a raw "../" path with a redirect before any handler runs.
		if rec.Code != 404 && !(rec.Code == 307 && strings.Contains(bad, "../")) {
			t.Errorf("download %q = %d, want 404", bad, rec.Code)
		}
	}
	if len(f.opened) != 1 || f.opened[0] != "20261001T000000Z-manual" {
		t.Errorf("only the well-formed unknown id may reach the service: %v", f.opened)
	}
	if len(g.events) != 1 {
		t.Errorf("a refused download was audited: %+v", g.events)
	}
	if rec := g.do("GET", backupsPath+"/"+id+"/download", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor download = %d, want 403", rec.Code)
	}
}

func TestBackupsDelete(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	f.state.Rows = backupRows()
	g := newBackupsRig(t, f)
	const id = "20261018T141900Z-pre_update"

	rec := g.do("GET", backupsPath+"/"+id+"/delete", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("confirm = %d", rec.Code)
	}
	for _, want := range []string{"Delete this backup?", "Before update to v0.26.99 from 18 Oct 2026 14:19", `action="/admin/settings/backups/` + id + `/delete"`, "Delete backup", "Cancel"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm page lacks %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/settings-backups-delete-confirm.html", normalizePage(body))
	if len(f.deleted) != 0 {
		t.Error("GET deleted something")
	}
	if rec := g.do("GET", backupsPath+"/20261019T091200Z-manual/delete", "owner-token", nil, nil); rec.Code != 303 {
		t.Errorf("confirm for a running backup = %d, want a redirect", rec.Code)
	}
	if rec := g.do("GET", backupsPath+"/20261001T000000Z-manual/delete", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("confirm for an unknown backup = %d", rec.Code)
	}

	rec = g.do("POST", backupsPath+"/"+id+"/delete", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || len(f.deleted) != 1 || f.deleted[0] != id {
		t.Fatalf("delete = %d %v", rec.Code, f.deleted)
	}
	if len(g.events) != 1 || g.events[0].Action != "backups.delete" || g.events[0].Target != id {
		t.Errorf("audit: %+v", g.events)
	}

	// A running backup cannot be deleted: a warning and no audit.
	f.deleteErr = ErrBackupRunning
	rec = g.do("POST", backupsPath+"/20261019T091200Z-manual/delete", "owner-token", url.Values{}, nil)
	if body := followFlashTo(g.ownerRig, backupsPath, rec); rec.Code != 303 || !strings.Contains(body, "A running backup cannot be deleted") {
		t.Errorf("delete running = %d", rec.Code)
	}
	f.deleteErr = ErrBackupNotFound
	if rec := g.do("POST", backupsPath+"/20261001T000000Z-manual/delete", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("delete unknown = %d", rec.Code)
	}
	if rec := g.do("POST", backupsPath+"/../x/delete", "owner-token", url.Values{}, nil); rec.Code == 303 {
		t.Errorf("delete bad id redirected as success")
	}
	if len(g.events) != 1 {
		t.Errorf("refused deletes were audited: %+v", g.events)
	}
}

func TestBackupsPostsAreOwnerOnlyAndNeedCSRF(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	f.state.Rows = backupRows()
	g := newBackupsRig(t, f)
	for _, target := range []string{backupsPath, backupsPath + "/20261018T141900Z-pre_update/delete"} {
		if rec := g.do("POST", target, "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", target, rec.Code)
		}
		if rec := g.do("POST", target, "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d, want 403", target, rec.Code)
		}
		if rec := g.do("POST", target, "", url.Values{}, nil); rec.Code == 303 && !strings.Contains(rec.Header().Get("Location"), "login") {
			t.Errorf("anonymous POST %s = %d %s", target, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, p := range []string{backupsPath + "/20261018T141900Z-pre_update/delete", backupsPath + "/20261018T141900Z-pre_update/download"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d, want 403", p, rec.Code)
		}
	}
	if len(f.starts)+len(f.deleted)+len(f.opened) != 0 {
		t.Errorf("a refused request reached the service: %v %v %v", f.starts, f.deleted, f.opened)
	}
	if len(g.events) != 0 {
		t.Errorf("audit: %+v", g.events)
	}
}

// deadlineWriter records SetWriteDeadline calls, as the net/http server's
// response writer would honour them.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (d *deadlineWriter) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

func TestBackupsDownloadClearsTheWriteDeadline(t *testing.T) {
	t.Parallel()

	f := baseBackups()
	f.state.Rows = backupRows()
	g := newBackupsRig(t, f)
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	g.h.ServeHTTP(w, httptestReq("GET", backupsPath+"/20261018T141900Z-pre_update/download", "owner-token"))
	if w.Code != 200 {
		t.Fatalf("download = %d", w.Code)
	}
	if len(w.deadlines) == 0 || !w.deadlines[len(w.deadlines)-1].IsZero() {
		t.Fatalf("write deadline not cleared: %v", w.deadlines)
	}
}
