package backup

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/portability"
)

// intRow scans one integer.
type intRow struct{ n int64 }

func (r intRow) Scan(dest ...any) error {
	switch d := dest[0].(type) {
	case *int64:
		*d = r.n
	case *int:
		*d = int(r.n)
	}
	return nil
}

// engineQuerier is fakeQuerier plus the two scalar queries the engine runs.
type engineQuerier struct {
	fakeQuerier
	dbSize int64
	major  int64
	// Cluster identity answers; sysErr makes pg_control_system unreadable.
	sysID, instance string
	sysErr          error
	notSuper        bool // rolsuper answers false
}

// strRow scans one string, one bool or fails.
type strRow struct {
	s   string
	b   bool
	err error
}

func (r strRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch d := dest[0].(type) {
	case *string:
		*d = r.s
	case *bool:
		*d = r.b
	}
	return nil
}

func (q *engineQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "pg_control_system"):
		return strRow{s: q.sysID, err: q.sysErr}
	case strings.Contains(sql, "inet_server_addr"):
		return strRow{s: q.instance}
	case strings.Contains(sql, "rolsuper"):
		return strRow{b: !q.notSuper}
	}
	if strings.Contains(sql, "pg_database_size") {
		return intRow{q.dbSize}
	}
	return intRow{q.major}
}

type rig struct {
	e    *Engine
	dump *fakePgDump
	q    *engineQuerier
	data string
	now  time.Time
}

func newRig(t *testing.T, plugins ...string) *rig {
	t.Helper()
	r := &rig{dump: &fakePgDump{version: "16.2"}, data: t.TempDir(), now: time.Date(2026, 10, 19, 9, 12, 0, 0, time.UTC)}
	r.q = &engineQuerier{major: 16, dbSize: 10 << 20}
	for _, p := range plugins {
		r.q.schemas = append(r.q.schemas, []string{"plugin_" + p, "hm_p_abcd1234_" + p})
	}
	r.e = New(Options{
		Data: r.data, MediaDir: filepath.Join(r.data, "uploads"), DatabaseURL: testURL,
		Run: r.dump.run, Version: "v1.2.3", KeepManual: 2, KeepPreUpdate: 1,
		Free: func(string) (uint64, error) { return 1 << 40, nil },
		Now:  func() time.Time { r.now = r.now.Add(time.Minute); return r.now },
		Log:  nil,
	})
	r.e.q = r.q
	r.e.exportFn = func(_ context.Context, w io.Writer, _ portability.ExportOptions) (*portability.Manifest, error) {
		_, err := w.Write([]byte("content"))
		return &portability.Manifest{Rows: map[string]int64{"users": 3}, MediaFiles: 2, MediaBytes: 9}, err
	}
	return r
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var names []string
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Mode != 0o600 {
			t.Errorf("%s mode = %o", h.Name, h.Mode)
		}
		names = append(names, h.Name)
	}
}

func TestCreateHappyPath(t *testing.T) {
	r := newRig(t, "webhooks", "foo_bar")
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	if info.State != StateOK || info.Percent != 100 || info.Size == 0 {
		t.Fatalf("info = %+v", info)
	}
	arch := filepath.Join(r.data, "backups", info.ID+".tar")
	want := []string{"core.dump", "plugin_webhooks.dump", "plugin_foo_bar.dump", "content.tar.gz", "manifest.json"}
	if got := tarNames(t, arch); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	rec, err := r.e.Get(info.ID)
	if err != nil || rec.State != StateOK || len(rec.Plugins) != 2 {
		t.Fatalf("sidecar = %+v, %v", rec, err)
	}
	fi, _ := os.Stat(arch)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("tar mode = %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Join(r.data, "backups"))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v", di.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(r.data, "backups", ".tmp-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	// The core dump excludes every plugin schema; each plugin dump uses its role.
	var core, plug string
	for _, c := range r.dump.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "core.dump") {
			core = j
		}
		if strings.Contains(j, "plugin_webhooks.dump") {
			plug = j
		}
	}
	if !strings.Contains(core, "--exclude-schema=plugin_webhooks") || !strings.Contains(plug, "--role=hm_p_abcd1234_webhooks") {
		t.Errorf("core = %s\nplugin = %s", core, plug)
	}
}

func TestCreateFailingPluginDump(t *testing.T) {
	r := newRig(t, "webhooks")
	calls := 0
	r.e.run = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if j := strings.Join(args, " "); strings.Contains(j, "--role=") {
			calls++
			return []byte("pg_dump: error: could not set role\nLOCK TABLE x\n"), errors.New("exit status 1")
		}
		return r.dump.run(ctx, env, name, args...)
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err == nil || !strings.Contains(err.Error(), "pg_dump: error: could not set role") {
		t.Fatalf("err = %v", err)
	}
	rec, _ := r.e.Get(info.ID)
	if rec.State != StateFailed || !strings.Contains(rec.Error, "could not set role") {
		t.Fatalf("sidecar = %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(r.data, "backups", info.ID+".tar")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("archive exists: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(r.data, "backups", ".tmp-*")); len(left) != 0 {
		t.Errorf("temp left: %v", left)
	}
	if calls != 1 {
		t.Errorf("plugin dump calls = %d", calls)
	}
}

func TestCreateLowSpaceFailsBeforeAnyExec(t *testing.T) {
	r := newRig(t, "webhooks")
	r.e.opts.Free = func(string) (uint64, error) { return 1 << 20, nil }
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err == nil || !strings.Contains(err.Error(), "not enough free disk space: need 84 MB, have 1 MB in ") {
		t.Fatalf("err = %v", err)
	}
	if len(r.dump.calls) != 0 {
		t.Fatalf("exec ran: %v", r.dump.calls)
	}
	if rec, _ := r.e.Get(info.ID); rec.State != StateFailed || rec.Error == "" {
		t.Fatalf("sidecar = %+v", rec)
	}
}

// The parts and the finished tar coexist in the backups directory, so 1.5x
// (which used to pass) is not enough there.
func TestCreateRefusesWhenPartsAndTarWouldNotBothFit(t *testing.T) {
	r := newRig(t)
	r.e.opts.Free = func(d string) (uint64, error) {
		if d == os.TempDir() {
			return 1 << 40, nil
		}
		return 16 << 20, nil // above the old 15 MB need, below 2x plus margin
	}
	_, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err == nil || !strings.Contains(err.Error(), "not enough free disk space: need 84 MB, have 16 MB in ") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateChecksTempDirToo(t *testing.T) {
	r := newRig(t)
	var seen []string
	r.e.opts.Free = func(d string) (uint64, error) { seen = append(seen, d); return 1 << 40, nil }
	if _, err := r.e.Create(t.Context(), Request{Kind: KindManual}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[1] != os.TempDir() || seen[0] != filepath.Join(r.data, "backups") {
		t.Fatalf("checked %v", seen)
	}
}

func TestCreateNoDatabase(t *testing.T) {
	r := newRig(t)
	r.e.q = nil
	_, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err == nil || err.Error() != "database not reachable" {
		t.Fatalf("err = %v", err)
	}
}

func TestManualWhileLockedIsBusy(t *testing.T) {
	r := newRig(t)
	_, work, err := r.e.Begin(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.e.Begin(t.Context(), Request{Kind: KindManual}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Begin = %v, want ErrBusy", err)
	}
	if list, _ := r.e.List(); len(list) != 1 || list[0].State != StateRunning {
		t.Fatalf("list = %+v", list)
	}
	if _, err := work(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.e.Create(t.Context(), Request{Kind: KindManual}); err != nil {
		t.Fatalf("after finish: %v", err)
	}
}

func TestPreUpdateWaitsThenTimesOut(t *testing.T) {
	r := newRig(t)
	_, work, err := r.e.Begin(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = r.e.Create(t.Context(), Request{Kind: KindPreUpdate, Target: "v2", Wait: 400 * time.Millisecond})
	if !errors.Is(err, ErrBusy) || err.Error() != "another backup is running" {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatalf("did not wait: %v", time.Since(start))
	}
	_, _ = work(t.Context())
}

func TestPreUpdateWaitsThenRuns(t *testing.T) {
	r := newRig(t)
	_, work, err := r.e.Begin(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = work(context.Background())
	}()
	info, err := r.e.Create(t.Context(), Request{Kind: KindPreUpdate, Target: "v2", Wait: 5 * time.Second})
	if err != nil || info.Target != "v2" || info.Kind != KindPreUpdate {
		t.Fatalf("info = %+v, %v", info, err)
	}
}

func TestProgressMonotonicAndComplete(t *testing.T) {
	r := newRig(t, "a", "b")
	var last atomic.Int64
	var bad atomic.Bool
	orig := r.dump.run
	r.e.run = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if len(args) > 1 {
			recs, _ := r.e.List()
			if len(recs) == 1 {
				p := int64(recs[0].Percent)
				if p < last.Load() {
					bad.Store(true)
				}
				last.Store(p)
			}
		}
		return orig(ctx, env, name, args...)
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	if bad.Load() {
		t.Error("percent went backwards")
	}
	if last.Load() == 0 || last.Load() >= 100 {
		t.Errorf("percent during run = %d, want 0 < p < 100", last.Load())
	}
	if info.Percent != 100 {
		t.Errorf("final percent = %d", info.Percent)
	}
}

func TestRetentionAfterSuccess(t *testing.T) {
	r := newRig(t)
	var ids []string
	for range 4 {
		info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, info.ID)
	}
	list, _ := r.e.List()
	if len(list) != 2 || list[0].ID != ids[3] || list[1].ID != ids[2] {
		t.Fatalf("list = %+v", list)
	}
}

func TestSameSecondIDsDoNotCollide(t *testing.T) {
	r := newRig(t)
	r.e.opts.Now = func() time.Time { return r.now }
	a, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || a.ID == b.ID {
		t.Fatalf("a=%s b=%s err=%v", a.ID, b.ID, err)
	}
}

func TestCancelledContextFails(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	r.e.exportFn = func(context.Context, io.Writer, portability.ExportOptions) (*portability.Manifest, error) {
		cancel()
		return nil, ctx.Err()
	}
	info, err := r.e.Create(ctx, Request{Kind: KindManual})
	if err == nil {
		t.Fatal("want error")
	}
	if rec, _ := r.e.Get(info.ID); rec.State != StateFailed {
		t.Fatalf("sidecar = %+v", rec)
	}
}

func TestKeepFromEnv(t *testing.T) {
	m, p := KeepFromEnv()
	if m != 5 || p != 2 {
		t.Fatalf("defaults = %d, %d", m, p)
	}
	t.Setenv(EnvKeep, "9")
	t.Setenv(EnvKeepPreUpdate, "0")
	if m, p = KeepFromEnv(); m != 9 || p != 2 {
		t.Fatalf("env = %d, %d", m, p)
	}
}

func TestFreeDisk(t *testing.T) {
	r := newRig(t)
	if got := r.e.FreeDisk(); got != 1<<40 {
		t.Fatalf("FreeDisk = %d", got)
	}
}

func TestCreateFailsWhenMediaDirMissing(t *testing.T) {
	r := newRig(t)
	r.e.exportFn = func(_ context.Context, w io.Writer, _ portability.ExportOptions) (*portability.Manifest, error) {
		_, err := w.Write([]byte("content"))
		return &portability.Manifest{Rows: map[string]int64{"media": 1}, MediaMissing: 2}, err
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err == nil || !strings.Contains(err.Error(), "media directory") || !strings.Contains(err.Error(), "MEDIA_UPLOAD_DIR") {
		t.Fatalf("err = %v", err)
	}
	if rec, _ := r.e.Get(info.ID); rec.State != StateFailed {
		t.Fatalf("sidecar = %+v", rec)
	}
}

func TestCreateFailsWhenEveryReferencedFileMissing(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(r.e.mediaDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	r.e.exportFn = func(_ context.Context, w io.Writer, _ portability.ExportOptions) (*portability.Manifest, error) {
		_, err := w.Write([]byte("content"))
		return &portability.Manifest{Rows: map[string]int64{"media": 2}, MediaMissing: 2}, err
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err == nil || !strings.Contains(err.Error(), "none of the 2 media files") || !strings.Contains(err.Error(), r.e.MediaDirAbs()) {
		t.Fatalf("err = %v", err)
	}
	if rec, _ := r.e.Get(info.ID); rec.State != StateFailed {
		t.Fatalf("sidecar = %+v", rec)
	}
}

func TestCreateOKWhenSomeReferencedFilesMissing(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(r.e.mediaDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	r.e.exportFn = func(_ context.Context, w io.Writer, o portability.ExportOptions) (*portability.Manifest, error) {
		o.OnMissing("2026/a.png")
		_, err := w.Write([]byte("content"))
		return &portability.Manifest{Rows: map[string]int64{"hatamoto_media": 2}, MediaFiles: 1, MediaMissing: 1}, err
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || info.State != StateOK || info.MediaMissing != 1 {
		t.Fatalf("info = %+v err = %v", info, err)
	}
	rec, err := r.e.Get(info.ID)
	if err != nil || rec.State != StateOK || rec.MediaMissing != 1 {
		t.Fatalf("sidecar = %+v, %v", rec, err)
	}
}

func TestCreateOKWithoutMediaRowsAndNoDir(t *testing.T) {
	r := newRig(t)
	r.e.exportFn = func(_ context.Context, w io.Writer, _ portability.ExportOptions) (*portability.Manifest, error) {
		_, err := w.Write([]byte("content"))
		return &portability.Manifest{Rows: map[string]int64{"users": 1}}, err
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil || info.State != StateOK {
		t.Fatalf("info = %+v err = %v", info, err)
	}
}
