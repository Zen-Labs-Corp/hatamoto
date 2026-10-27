package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/supervisor"
	"hatamoto.dev/core/internal/worker"
)

const (
	testPlatform = "linux-amd64"
	runNow       = "2026-11-01T12:00:00Z"
)

// runBox is an updater over a temp data dir, a fake channel server with
// throwaway keys, and fake supervisor entry points.
type runBox struct {
	t        *testing.T
	data     string
	srv      *recorder
	files    map[string][]byte
	rel, man packages.Key
	ropts    release.Options
	u        *Updater

	staged, applied []supervisor.ApplyOpts
	appliedSeen     [][]byte // artifact bytes seen by applyFn / stageFn
	rolledBack      int
	supervisedNow   bool
	imports         fakeImports
	hold            chan struct{} // when set, artifact requests signal here and hang until cancelled
}

type fakeImports struct{ running bool }

func (f *fakeImports) ImportRunning(context.Context) (bool, error) { return f.running, nil }

func newRunBox(t *testing.T, running string) *runBox {
	t.Helper()
	b := &runBox{t: t, data: filepath.Join(t.TempDir(), "data"), files: map[string][]byte{}}
	if err := os.MkdirAll(filepath.Join(b.data, "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	var rts, mts = packages.TrustSet{}, packages.TrustSet{}
	var err error
	if b.rel, err = packages.GenerateKey(); err != nil {
		t.Fatal(err)
	}
	if b.man, err = packages.GenerateKey(); err != nil {
		t.Fatal(err)
	}
	rp, _ := b.rel.PublicKey()
	mp, _ := b.man.PublicKey()
	rts.Add(rp)
	mts.Add(mp)
	b.ropts = release.Options{Release: rts, Manifest: mts}
	b.srv = newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		if b.hold != nil && strings.Contains(r.URL.Path, "/hatamoto-") {
			b.hold <- struct{}{}
			<-r.Context().Done()
			return
		}
		body, ok := b.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	})
	now, _ := time.Parse(time.RFC3339, runNow)
	u, err := New(Env{
		Data: b.data, Version: running, Platform: testPlatform,
		Supervisor: supervisor.Config{Data: b.data, Bootstrap: filepath.Join(b.data, "boot"), BootstrapVersion: running, Release: b.ropts},
		Imports:    &b.imports, Backups: &fakeBackups{}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	u.fetcher = testFetcher(t, b.srv.URL)
	rec := func(into *[]supervisor.ApplyOpts) switchFn {
		return func(_ supervisor.Config, o supervisor.ApplyOpts) (supervisor.Result, error) {
			*into = append(*into, o)
			body, err := os.ReadFile(o.File)
			if err != nil {
				return supervisor.Result{}, err
			}
			b.appliedSeen = append(b.appliedSeen, body)
			for _, f := range []string{o.Record, o.Sig} {
				if _, err := os.Stat(f); err != nil {
					return supervisor.Result{}, err
				}
			}
			return supervisor.Result{Message: "fake switch"}, nil
		}
	}
	u.applyFn, u.stageFn = rec(&b.applied), rec(&b.staged)
	u.rollbackFn = func(supervisor.Config) (supervisor.Result, error) {
		b.rolledBack++
		return supervisor.Result{Message: "rolled"}, nil
	}
	u.supervised = func(supervisor.Config) bool { return b.supervisedNow }
	u.free = func(string) (uint64, error) { return 1 << 40, nil }
	b.u = u
	return b
}

// offer publishes version with a 100% ramp and a build for the test platform.
func (b *runBox) offer(version string, mod func(*release.Record)) {
	b.t.Helper()
	art := []byte("binary " + version)
	sum := sha256.Sum256(art)
	rec := release.Record{Version: version, Kind: "patch", SDK: "0.10.0", RollbackFloor: "v1.0.0", MinFrom: "v1.0.0",
		Artifacts: map[string]release.Artifact{testPlatform: {SHA256: hex.EncodeToString(sum[:]), Size: int64(len(art))}}}
	if mod != nil {
		mod(&rec)
	}
	rb, err := release.MarshalRecord(rec)
	if err != nil {
		b.t.Fatal(err)
	}
	s, err := packages.SignBytes(b.rel, rb)
	if err != nil {
		b.t.Fatal(err)
	}
	b.files["/releases/"+version+"/release.json"] = rb
	b.files["/releases/"+version+"/release.json.sig"] = []byte(s.Signer + ":" + s.Signature + "\n")
	b.files["/releases/"+version+"/hatamoto-"+testPlatform] = art
}

func (b *runBox) publish(entries []release.Entry, yanked []release.Yank) {
	b.t.Helper()
	now, _ := time.Parse(time.RFC3339, runNow)
	if yanked == nil {
		yanked = []release.Yank{}
	}
	m := release.ChannelManifest{Channel: "stable", IssuedAt: now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Releases: entries, Yanked: yanked}
	for i, e := range m.Releases { // like the real manifest: each entry names its record's signature
		if sig, ok := b.files["/releases/"+e.Version+"/release.json.sig"]; ok && e.RecordSig == "" {
			m.Releases[i].RecordSig = strings.TrimSpace(string(sig))
		}
	}
	body, _ := json.Marshal(m)
	doc, err := packages.SignIndex(body, b.man)
	if err != nil {
		b.t.Fatal(err)
	}
	b.files["/releases/channels/stable.json"] = doc
}

func open100(v string) release.Entry {
	return release.Entry{Version: v, Ramp: []release.RampStep{{At: "2026-10-01T00:00:00Z", Pct: 100}}}
}

func (b *runBox) hits(sub string) int {
	n := 0
	for _, r := range b.srv.requests() {
		if strings.Contains(r.URL.Path, sub) {
			n++
		}
	}
	return n
}

func (b *runBox) tmpDirs() []string {
	m, _ := filepath.Glob(filepath.Join(b.data, "releases", ".tmp-dl-*"))
	return m
}

func TestRunOffMakesNoRequest(t *testing.T) {
	t.Setenv(EnvMode, "off")
	b := newRunBox(t, "v1.0.0")
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindOff || st.LastResult != ResultSkipped {
		t.Fatalf("%+v %v", st, err)
	}
	if n := len(b.srv.requests()); n != 0 {
		t.Fatalf("%d requests with updates off", n)
	}
}

func TestRunDevBuildMakesNoRequest(t *testing.T) {
	b := newRunBox(t, "dev")
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindDev {
		t.Fatalf("%+v %v", st, err)
	}
	if n := len(b.srv.requests()); n != 0 {
		t.Fatalf("%d requests from a dev build", n)
	}
}

func TestRunNotifyDecidesButNeverDownloads(t *testing.T) {
	t.Setenv(EnvMode, "notify")
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindNotify || st.Target != "v1.0.1" {
		t.Fatalf("%+v %v", st, err)
	}
	if b.hits("hatamoto-") != 0 || len(b.applied)+len(b.staged) != 0 {
		t.Fatal("notify downloaded or switched")
	}
}

func TestRunBusyMakesNoRequest(t *testing.T) {
	cases := map[string]func(*runBox){
		"pending": func(b *runBox) {
			st := &supervisor.State{Pending: &supervisor.Pending{Kind: "apply", Version: "v1.0.1"}}
			if err := supervisor.SaveState(supervisor.NewLayout(b.data).StatePath(), st); err != nil {
				b.t.Fatal(err)
			}
		},
		"probation": func(b *runBox) {
			if err := supervisor.SaveState(supervisor.NewLayout(b.data).StatePath(), &supervisor.State{Phase: "probation"}); err != nil {
				b.t.Fatal(err)
			}
		},
		"unproven": func(b *runBox) {
			if err := supervisor.SaveState(supervisor.NewLayout(b.data).StatePath(), &supervisor.State{Unproven: "v1.0.0"}); err != nil {
				b.t.Fatal(err)
			}
		},
		"import": func(b *runBox) { b.imports.running = true },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			b := newRunBox(t, "v1.0.0")
			b.publish([]release.Entry{open100("v1.0.1")}, nil)
			setup(b)
			st, err := b.u.Run(t.Context(), RunOpts{})
			if err != nil || st.Decision != KindBusy || st.LastResult != ResultSkipped {
				t.Fatalf("%+v %v", st, err)
			}
			if n := len(b.srv.requests()); n != 0 {
				t.Fatalf("%d requests while busy", n)
			}
		})
	}
}

func TestRunUpToDateIsOneGet(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.publish(nil, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindUpToDate || st.LastCheckAt == nil {
		t.Fatalf("%+v %v", st, err)
	}
	if reqs := b.srv.requests(); len(reqs) != 1 || reqs[0].URL.Path != "/releases/channels/stable.json" {
		t.Fatalf("requests: %v", reqs)
	}
}

func TestRunHappyApplySupervised(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindApplying || st.Target != "v1.0.1" {
		t.Fatalf("%+v %v", st, err)
	}
	if len(b.applied) != 1 || len(b.staged) != 0 {
		t.Fatalf("applied=%d staged=%d", len(b.applied), len(b.staged))
	}
	if string(b.appliedSeen[0]) != "binary v1.0.1" {
		t.Fatalf("artifact: %q", b.appliedSeen[0])
	}
	if d := b.tmpDirs(); len(d) != 0 {
		t.Fatalf("download dir left behind: %v", d)
	}
	if b.hits("release.json") != 2 || b.hits("hatamoto-") != 1 { // record + sig, artifact
		t.Fatalf("requests: %v", b.srv.requests())
	}
}

func TestRunUnsupervisedOnlyStages(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindStaged {
		t.Fatalf("%+v %v", st, err)
	}
	if len(b.staged) != 1 || len(b.applied) != 0 {
		t.Fatalf("applied=%d staged=%d", len(b.applied), len(b.staged))
	}
	if d := b.tmpDirs(); len(d) != 0 {
		t.Fatalf("download dir left behind: %v", d)
	}
}

func TestRunDryRunInstallsNothing(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{DryRun: true})
	if err != nil || st.Decision != KindApplying || !strings.Contains(st.Reason, "dry run") {
		t.Fatalf("%+v %v", st, err)
	}
	if b.hits("hatamoto-") != 0 || len(b.applied)+len(b.staged) != 0 {
		t.Fatal("dry run installed")
	}
}

func TestRunYankBackRollsBackOnlyUnderASupervisor(t *testing.T) {
	setup := func(supervised bool) *runBox {
		b := newRunBox(t, "v1.0.1")
		b.supervisedNow = supervised
		l := supervisor.NewLayout(b.data)
		if err := os.MkdirAll(filepath.Join(l.Releases, "v1.0.0"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := l.SwapSymlink("previous", "v1.0.0"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(l.Releases, "v1.0.1"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := l.SwapSymlink("current", "v1.0.1"); err != nil {
			t.Fatal(err)
		}
		// The running release's stored record carries the rollback floor.
		b.offer("v1.0.1", nil)
		_ = os.WriteFile(filepath.Join(l.Releases, "v1.0.1", "release.json"), b.files["/releases/v1.0.1/release.json"], 0o644)
		_ = os.WriteFile(filepath.Join(l.Releases, "v1.0.1", "release.json.sig"), b.files["/releases/v1.0.1/release.json.sig"], 0o644)
		if err := supervisor.SaveState(l.StatePath(), &supervisor.State{Run: supervisor.RunRelease, CurrentSeen: "v1.0.1"}); err != nil {
			t.Fatal(err)
		}
		b.publish([]release.Entry{open100("v1.0.1")}, []release.Yank{{Version: "v1.0.1", Reason: "bad build"}})
		return b
	}
	b := setup(true)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindRolledBackYank || b.rolledBack != 1 {
		t.Fatalf("%+v %v rolledBack=%d", st, err, b.rolledBack)
	}
	b = setup(false)
	st, err = b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindBlocked || b.rolledBack != 0 || !strings.Contains(st.Reason, "no supervisor") {
		t.Fatalf("%+v %v rolledBack=%d", st, err, b.rolledBack)
	}
}

func TestRunRejectsARecordForAnotherVersion(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.2", nil)
	for _, f := range []string{"release.json", "release.json.sig"} { // served under the wrong version
		b.files["/releases/v1.0.1/"+f] = b.files["/releases/v1.0.2/"+f]
	}
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err == nil || st.LastResult != ResultError {
		t.Fatalf("a record naming another version must fail: %+v %v", st, err)
	}
	if len(b.applied)+len(b.staged) != 0 || b.hits("hatamoto-") != 0 {
		t.Fatal("installed from a mismatched record")
	}
}

func TestRunCleansTempDirOnDownloadError(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	delete(b.files, "/releases/v1.0.1/hatamoto-"+testPlatform)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err == nil || st.LastResult != ResultError {
		t.Fatalf("%+v %v", st, err)
	}
	if d := b.tmpDirs(); len(d) != 0 {
		t.Fatalf("download dir left behind: %v", d)
	}
}

func TestRunBlocksOnPluginRange(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", func(r *release.Record) { r.SDK = "0.11.0" })
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	b.u.env.Plugins = fakePlugins{{Kind: "plugin", Name: "hello", Range: "^0.10"}}
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindBlocked || !strings.Contains(st.Reason, "hello") || !strings.Contains(st.Reason, "^0.10") {
		t.Fatalf("%+v %v", st, err)
	}
	if b.hits("hatamoto-") != 0 {
		t.Fatal("downloaded a blocked release")
	}
	// --force skips the plugin check.
	b.supervisedNow = true
	if st, err = b.u.Run(t.Context(), RunOpts{Force: true}); err != nil || st.Decision != KindApplying {
		t.Fatalf("forced: %+v %v", st, err)
	}
}

type fakePlugins []NamedRange

func (f fakePlugins) EnabledSDKRanges(context.Context) ([]NamedRange, error) { return f, nil }

func TestRunBlocksOnDisk(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	b.u.free = func(string) (uint64, error) { return 1, nil }
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindBlocked || !strings.Contains(st.Reason, "free") {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestRunNamedApplyAllowsMajorsAndRefusesYanked(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v2.0.0", func(r *release.Record) { r.Kind = "major" })
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{{Version: "v2.0.0"}, open100("v1.0.1")}, []release.Yank{{Version: "v1.0.1", Reason: "x"}})
	st, err := b.u.Run(t.Context(), RunOpts{Apply: "v2.0.0"})
	if err != nil || st.Decision != KindApplying || len(b.applied) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1"}); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("yanked: %v", err)
	}
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v3.0.0"}); err == nil {
		t.Fatal("a version not on the channel")
	}
}

func TestRunIsSingleFlight(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.publish(nil, nil)
	b.u.mu.Lock()
	st, err := b.u.Run(t.Context(), RunOpts{})
	b.u.mu.Unlock()
	if err != nil || st.Decision != KindBusy {
		t.Fatalf("%+v %v", st, err)
	}
	if n := len(b.srv.requests()); n != 0 {
		t.Fatalf("%d requests while another run held the lock", n)
	}
}

// onRelease makes the box run release v on top of a bootstrap build that is
// older, the state every box is in after its first self-update.
func (b *runBox) onRelease(v string) {
	b.t.Helper()
	l := supervisor.NewLayout(b.data)
	if err := os.MkdirAll(filepath.Join(l.Releases, v), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := l.SwapSymlink("current", v); err != nil {
		b.t.Fatal(err)
	}
	if err := supervisor.SaveState(l.StatePath(), &supervisor.State{Run: supervisor.RunRelease, CurrentSeen: v, PreviousIsBootstrap: true}); err != nil {
		b.t.Fatal(err)
	}
}

func TestRunUsesTheReleaseTheSupervisorRuns(t *testing.T) {
	// Bootstrap v1.0.0 self-updated to v1.0.2. The process is the bootstrap
	// binary (Env.Version v1.0.0) but the box runs v1.0.2.
	b := newRunBox(t, "v1.0.0")
	b.onRelease("v1.0.2")
	b.offer("v1.0.1", nil)
	b.offer("v1.0.2", nil)

	// Up to date: the running release is the newest; nothing is downloaded.
	b.publish([]release.Entry{open100("v1.0.1"), open100("v1.0.2")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindUpToDate {
		t.Fatalf("%+v %v", st, err)
	}
	if b.hits("hatamoto-") != 0 || len(b.applied)+len(b.staged) != 0 {
		t.Fatal("reinstalled the running version")
	}

	// The running v1.0.2 is halted: v1.0.1 is older and is never picked.
	h := open100("v1.0.2")
	h.Halted = true
	b.publish([]release.Entry{open100("v1.0.1"), h}, nil)
	b.supervisedNow = true
	st, err = b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindUpToDate {
		t.Fatalf("%+v %v", st, err)
	}
	if b.hits("hatamoto-") != 0 || len(b.applied)+len(b.staged) != 0 {
		t.Fatal("downgraded to v1.0.1")
	}

	// Naming the running version is refused before anything is downloaded.
	b.publish([]release.Entry{open100("v1.0.1"), open100("v1.0.2")}, nil)
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.2"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("apply of the running version: %v", err)
	}
	if b.hits("hatamoto-") != 0 {
		t.Fatal("downloaded the running version")
	}
}

// yankedOnBootstrapHistory is a box whose first self-update (from the
// bootstrap v1.0.0) went to v1.0.1, which the channel then withdrew.
func yankedOnBootstrapHistory(t *testing.T, mode string) *runBox {
	t.Helper()
	b := newRunBox(t, "v1.0.0")
	b.supervisedNow = true
	b.onRelease("v1.0.1")
	l := supervisor.NewLayout(b.data)
	st, _ := supervisor.LoadState(l.StatePath())
	st.BootstrapVersion = "v1.0.0"
	_ = supervisor.SaveState(l.StatePath(), st)
	b.offer("v1.0.1", nil)
	for _, f := range []string{"release.json", "release.json.sig"} {
		_ = os.WriteFile(filepath.Join(l.Releases, "v1.0.1", f), b.files["/releases/v1.0.1/"+f], 0o644)
	}
	b.publish([]release.Entry{open100("v1.0.1")}, []release.Yank{{Version: "v1.0.1", Reason: "bad build"}})
	if mode != "" {
		t.Setenv("HATAMOTO_UPDATES", mode)
	}
	return b
}

func TestRunYankBackReachesTheBootstrap(t *testing.T) {
	b := yankedOnBootstrapHistory(t, "")
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindRolledBackYank || b.rolledBack != 1 {
		t.Fatalf("%+v %v rolledBack=%d", st, err, b.rolledBack)
	}
}

func TestRunYankBackRefusesAnUnknownBootstrapVersion(t *testing.T) {
	// A bootstrap that predates state.bootstrap_version: the child (a release)
	// must not offer its own version in its place.
	b := yankedOnBootstrapHistory(t, "")
	l := supervisor.NewLayout(b.data)
	st, _ := supervisor.LoadState(l.StatePath())
	st.BootstrapVersion = ""
	_ = supervisor.SaveState(l.StatePath(), st)
	cfg := b.u.env.Supervisor
	cfg.BootstrapVersion = "v1.0.1" // the child's own stamp
	b.u.env.Supervisor = cfg
	got, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || got.Decision != KindBlocked || b.rolledBack != 0 || !strings.Contains(got.Reason, "rollback floor") {
		t.Fatalf("%+v %v rolledBack=%d", got, err, b.rolledBack)
	}
}

func TestRunYankedRunningWithABlockedFixRollsBack(t *testing.T) {
	b := yankedOnBootstrapHistory(t, "")
	b.offer("v1.0.2", nil)
	b.publish([]release.Entry{open100("v1.0.1"), open100("v1.0.2")}, []release.Yank{{Version: "v1.0.1", Reason: "bad build"}})
	b.u.free = func(string) (uint64, error) { return 1, nil } // the fix does not fit
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindRolledBackYank || b.rolledBack != 1 || len(b.applied) != 0 ||
		!strings.Contains(st.Reason, "v1.0.2 is blocked") {
		t.Fatalf("%+v %v rolledBack=%d applied=%d", st, err, b.rolledBack, len(b.applied))
	}
}

func TestRunNotifyNeverRollsBack(t *testing.T) {
	b := yankedOnBootstrapHistory(t, "notify")
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindNotify || b.rolledBack != 0 || !strings.Contains(st.Reason, "rolling back") {
		t.Fatalf("%+v %v rolledBack=%d", st, err, b.rolledBack)
	}
}

func TestShutdownCancelsAnUpdateInFlight(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.hold = make(chan struct{}, 1)
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)

	// The scheduler hands the job a context under its 10 minute cap, derived
	// from the server's lifecycle context.
	life, shutdown := context.WithCancel(context.Background())
	jobCtx, capCancel := context.WithTimeout(life, 10*time.Minute)
	defer capCancel()
	done := make(chan error, 1)
	go func() { done <- b.u.job(jobCtx) }()
	select {
	case <-b.hold:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	if len(b.tmpDirs()) != 1 {
		t.Fatalf("expected the download dir while downloading: %v", b.tmpDirs())
	}
	shutdown()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled run must not report success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the in-flight update")
	}
	if d := b.tmpDirs(); len(d) != 0 {
		t.Fatalf("download dir left behind: %v", d)
	}
}

func TestShutdownAfterTheJobCapStillCancels(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.hold = make(chan struct{}, 1)
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)

	// The job cap (a tiny one here) fires first, then the lifecycle is cancelled.
	life, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	jobCtx, capCancel := context.WithTimeout(worker.WithJobParent(life, life), 50*time.Millisecond)
	defer capCancel()
	done := make(chan error, 1)
	go func() { done <- b.u.job(jobCtx) }()
	select {
	case <-b.hold:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	<-jobCtx.Done() // the cap has fired
	select {
	case err := <-done:
		t.Fatalf("the cap must not stop the run: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	shutdown()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled run must not report success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown after the job cap did not cancel the run")
	}
}

func TestJobCapDoesNotCutTheDownload(t *testing.T) {
	// The scheduler's cap expiring (a deadline, not a cancel) must not stop the run.
	b := newRunBox(t, "v1.0.0")
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	if err := b.u.job(ctx); err != nil || len(b.applied) != 1 {
		t.Fatalf("err=%v applied=%d", err, len(b.applied))
	}
}

func TestRunNotifyFetchesNoRecords(t *testing.T) {
	t.Setenv("HATAMOTO_UPDATES", "notify")
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindNotify || st.Target != "v1.0.1" {
		t.Fatalf("%+v %v", st, err)
	}
	if n := len(b.srv.requests()); n != 1 || b.hits("channels/stable.json") != 1 {
		t.Fatalf("a notify check is one manifest GET, got %d requests", n)
	}
}

func TestRunReSignedRecordReplacesTheCachedOne(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	t.Setenv("HATAMOTO_UPDATES", "auto")
	b.u.free = func(string) (uint64, error) { return 0, nil } // blocked, record cached
	if _, err := b.u.Run(t.Context(), RunOpts{}); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(b.data, "releases", ".records", "v1.0.1", "release.json")
	old, err := os.ReadFile(cached)
	if err != nil {
		t.Fatal(err)
	}
	// The record is corrected and re-signed while the version stays listed.
	b.offer("v1.0.1", func(r *release.Record) { r.SDK = "0.11.0" })
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	if _, err := b.u.Run(t.Context(), RunOpts{}); err != nil {
		t.Fatal(err)
	}
	now, err := os.ReadFile(cached)
	if err != nil || bytes.Equal(now, old) || !bytes.Equal(now, b.files["/releases/v1.0.1/release.json"]) {
		t.Fatalf("the re-signed record did not replace the cached one (err %v)", err)
	}
}

func TestRunWaitingBoxReadsEachRecordOnce(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	t.Setenv("HATAMOTO_UPDATES", "auto")
	// A box blocked on the disk: it waits with a record in hand.
	b.u.free = func(string) (uint64, error) { return 0, nil }
	for i := 0; i < 3; i++ {
		st, err := b.u.Run(t.Context(), RunOpts{})
		if err != nil || st.Decision != KindBlocked {
			t.Fatalf("run %d: %+v %v", i, st, err)
		}
	}
	if n := b.hits("release.json"); n != 2 { // record and its signature, once
		t.Fatalf("record fetches over three checks: %d (want release.json + .sig = 2)", n)
	}
	// A tampered cache entry is not trusted: it is fetched again.
	cached := filepath.Join(b.data, "releases", ".records", "v1.0.1", "release.json")
	if err := os.WriteFile(cached, []byte(`{"version":"v1.0.1","kind":"security"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.u.Run(t.Context(), RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := b.hits("release.json"); n != 4 {
		t.Fatalf("a tampered cache must be refetched, fetches %d", n)
	}
	// The channel dropping a version drops its cache entry.
	b.publish(nil, nil)
	if _, err := b.u.Run(t.Context(), RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(cached)); err == nil {
		t.Fatal("cache entry of an unlisted version was kept")
	}
}

func TestRunAfterAHealthGateRollbackSaysSo(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	l := supervisor.NewLayout(b.data)
	st := &supervisor.State{Run: supervisor.RunBootstrap}
	st.AddBad("v1.0.1", "health gate failed: GET / returned 500")
	if err := supervisor.SaveState(l.StatePath(), st); err != nil {
		t.Fatal(err)
	}
	got, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || got.Decision != KindRolledBack || got.Target != "v1.0.1" || !strings.Contains(got.Reason, "GET / returned 500") {
		t.Fatalf("%+v %v", got, err)
	}
	if b.hits("hatamoto-") != 0 {
		t.Fatal("retried a rolled-back release")
	}
}

func TestRunUnsupervisedStagesAVersionOnce(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	want := "staged v1.0.1; run under `hatamoto supervise` to apply"
	if err != nil || st.Decision != KindStaged || !strings.HasPrefix(st.Reason, want) {
		t.Fatalf("%+v %v", st, err)
	}
	// What the real stage leaves behind.
	dir := filepath.Join(b.data, "releases", "v1.0.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"hatamoto": b.files["/releases/v1.0.1/hatamoto-"+testPlatform],
		"release.json": b.files["/releases/v1.0.1/release.json"], "release.json.sig": b.files["/releases/v1.0.1/release.json.sig"]} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	artifactGets := b.hits("/hatamoto-")
	for i := 0; i < 3; i++ {
		st, err = b.u.Run(t.Context(), RunOpts{})
		if err != nil || st.Decision != KindStaged || st.Reason != want {
			t.Fatalf("repeat %d: %+v %v", i, st, err)
		}
	}
	if b.hits("/hatamoto-") != artifactGets || len(b.staged) != 1 {
		t.Fatalf("restaged: artifact GETs %d->%d, stage calls %d", artifactGets, b.hits("/hatamoto-"), len(b.staged))
	}
}
