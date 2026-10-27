//go:build integration

package supervisor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// Real processes: a supervisor subprocess (testdata/sup) supervising copies of
// the stub child (testdata/stub), driven with real signals and the real Apply.
//
//	go test -tags=integration -count=1 -v ./internal/supervisor/...

const stubMarker = "\n#hmstub-behavior\n"

var (
	supOnce sync.Once
	supBin  string
	supErr  error
)

func buildSup(t *testing.T) string {
	t.Helper()
	supOnce.Do(func() {
		dir, err := os.MkdirTemp("", "supbin")
		if err != nil {
			supErr = err
			return
		}
		supBin = filepath.Join(dir, "sup")
		if out, err := exec.Command("go", "build", "-o", supBin, "./testdata/sup").CombinedOutput(); err != nil {
			supErr = &buildError{string(out), err}
		}
	})
	if supErr != nil {
		t.Fatalf("build sup: %v", supErr)
	}
	return supBin
}

// stubWithBehavior is the stub binary with behavior lines appended, so the
// bytes can be signed, staged by Apply and still behave as asked.
func stubWithBehavior(t *testing.T, behavior string) []byte {
	t.Helper()
	b, err := os.ReadFile(buildStub(t))
	if err != nil {
		t.Fatal(err)
	}
	return append(b, []byte(stubMarker+behavior+"\n")...)
}

type box struct {
	t        *testing.T
	dir      string
	data     string
	l        Layout
	port     string
	boot     string // bootstrap binary path
	version  string // bootstrap version stamp handed to the supervisor
	key      packages.Key
	opts     release.Options
	trustKey string // base64 public key, handed to the supervisor process
}

func newBox(t *testing.T) *box {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := k.PublicKey()
	ts := packages.TrustSet{}
	ts.Add(pub)
	dir := t.TempDir()
	addr, err := FreeLoopbackAddr()
	if err != nil {
		t.Fatal(err)
	}
	b := &box{t: t, dir: dir, data: filepath.Join(dir, "data"), port: addr[strings.LastIndex(addr, ":")+1:],
		version: "v1.0.0", key: k, opts: release.Options{Release: ts}, trustKey: k.Public}
	b.l = NewLayout(b.data)
	b.boot = filepath.Join(dir, "boot", "hatamoto") // /id answers "boot"
	b.writeBootstrap("")
	return b
}

func (b *box) writeBootstrap(behavior string) {
	b.t.Helper()
	if err := os.MkdirAll(filepath.Dir(b.boot), 0o755); err != nil {
		b.t.Fatal(err)
	}
	tmp := b.boot + ".new"
	if err := os.WriteFile(tmp, stubWithBehavior(b.t, behavior), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.Rename(tmp, b.boot); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) cfg() Config {
	return Config{Data: b.data, Bootstrap: b.boot, BootstrapVersion: b.version}
}

// artifact signs a stub with the behavior as version and returns Apply's options.
func (b *box) artifact(version, behavior string) ApplyOpts {
	b.t.Helper()
	body := stubWithBehavior(b.t, behavior)
	sum := sha256.Sum256(body)
	rec, err := release.MarshalRecord(release.Record{Version: version, Kind: "patch", SDK: "0.10.0",
		RollbackFloor: "v1.0.0", MinFrom: "v1.0.0",
		Artifacts: map[string]release.Artifact{testPlatform: {SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}}})
	if err != nil {
		b.t.Fatal(err)
	}
	sig, err := packages.SignBytes(b.key, rec)
	if err != nil {
		b.t.Fatal(err)
	}
	dir := b.t.TempDir()
	o := ApplyOpts{File: filepath.Join(dir, "hatamoto"), Record: filepath.Join(dir, "release.json"),
		Sig: filepath.Join(dir, "release.json.sig"), Release: b.opts, Platform: testPlatform}
	for p, c := range map[string][]byte{o.File: body, o.Record: rec, o.Sig: []byte(sig.Signer + ":" + sig.Signature + "\n")} {
		if err := os.WriteFile(p, c, 0o644); err != nil {
			b.t.Fatal(err)
		}
	}
	return o
}

// install stages a signed version under releases/ without telling anyone (a
// release that was applied earlier, or put there by hand).
func (b *box) install(version, behavior string) {
	b.t.Helper()
	o := b.artifact(version, behavior)
	dir := filepath.Join(b.l.Releases, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.t.Fatal(err)
	}
	for src, name := range map[string]string{o.File: "hatamoto", o.Record: "release.json", o.Sig: "release.json.sig"} {
		body, err := os.ReadFile(src)
		if err != nil {
			b.t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if name == "hatamoto" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, mode); err != nil {
			b.t.Fatal(err)
		}
	}
}

func (b *box) state() *State {
	b.t.Helper()
	st, err := LoadState(b.l.StatePath())
	if err != nil {
		b.t.Fatalf("state.json unreadable: %v", err)
	}
	return st
}

func (b *box) events() []string {
	raw, _ := os.ReadFile(b.l.EventsPath())
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (b *box) hasEvent(phase, to string) bool {
	for _, e := range b.events() {
		if strings.Contains(e, `"phase":"`+phase+`"`) && strings.Contains(e, `"to":"`+to+`"`) {
			return true
		}
	}
	return false
}

func (b *box) id() (string, bool) {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get("http://127.0.0.1:" + b.port + "/id")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode == 200
}

func (b *box) waitID(want string, within time.Duration) {
	b.t.Helper()
	deadline := time.Now().Add(within)
	got := ""
	for time.Now().Before(deadline) {
		if id, ok := b.id(); ok {
			if got = id; id == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	b.t.Fatalf("/id never reported %q (last %q)\n%s", want, got, b.t.Name())
}

func (b *box) waitFor(what string, within time.Duration, cond func() bool) {
	b.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	b.t.Fatalf("timed out waiting for %s", what)
}

func (b *box) childGone() bool {
	c := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get("http://127.0.0.1:" + b.port + "/id")
	if err == nil {
		resp.Body.Close()
		return false
	}
	return true
}

// ---- the supervisor subprocess

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type supProc struct {
	t    *testing.T
	cmd  *exec.Cmd
	logs *syncBuf
	done chan error
}

// startSup runs a real supervisor with short timings. extra overrides env.
func (b *box) startSup(extra ...string) *supProc {
	b.t.Helper()
	env := append(os.Environ(),
		"HATAMOTO_DATA="+b.data, "API_PORT="+b.port,
		"SUP_BOOTSTRAP="+b.boot, "SUP_VERSION="+b.version,
		"SUP_READY_URL=http://127.0.0.1:"+b.port+"/ready",
		"SUP_STOP_GRACE=3s", "SUP_READY_TIMEOUT=3s", "SUP_OBSERVE=1s", "SUP_SMOKE_POLL=200ms",
		"SUP_BOOT_WINDOW=10s", "SUP_BACKOFF=100ms", "SUP_TRUST_KEYS="+b.trustKey)
	env = append(env, extra...)
	p := &supProc{t: b.t, logs: &syncBuf{}, done: make(chan error, 1)}
	p.cmd = exec.Command(buildSup(b.t))
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.logs, p.logs
	if err := p.cmd.Start(); err != nil {
		b.t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	b.t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-p.done:
			case <-time.After(8 * time.Second):
				_ = p.cmd.Process.Kill()
			}
		}
		if b.t.Failed() {
			b.t.Logf("supervisor log:\n%s", p.logs.String())
		}
	})
	return p
}

// stop sends SIGTERM and returns the exit status; it must be 0 within grace.
func (p *supProc) stop(within time.Duration) error {
	p.t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.t.Fatal(err)
	}
	select {
	case err := <-p.done:
		p.done <- err // keep for Cleanup
		return err
	case <-time.After(within):
		p.t.Fatalf("supervisor did not exit within %v\n%s", within, p.logs.String())
	}
	return nil
}

func (p *supProc) alive() bool {
	select {
	case err := <-p.done:
		p.done <- err
		return false
	default:
		return true
	}
}

func (p *supProc) noAlarmWords() {
	p.t.Helper()
	low := strings.ToLower(p.logs.String())
	for _, w := range []string{"panic", "fatal"} {
		if strings.Contains(low, w) {
			p.t.Fatalf("supervisor log contains %q:\n%s", w, p.logs.String())
		}
	}
}

func (b *box) apply(o ApplyOpts) (Result, error) { return Apply(b.cfg(), o) }

func (b *box) mustApply(version, behavior string) Result {
	b.t.Helper()
	res, err := b.apply(b.artifact(version, behavior))
	if err != nil {
		b.t.Fatal(err)
	}
	return res
}

// applyHealthy applies a healthy version and waits until its gate has passed
// and the success event is recorded.
func (b *box) applyHealthy(version string) {
	b.t.Helper()
	if res := b.mustApply(version, ""); !res.Notified {
		b.t.Fatalf("supervisor was not notified: %+v", res)
	}
	b.waitID(version, 10*time.Second)
	b.waitFor("success event for "+version, 10*time.Second, func() bool { return b.hasEvent("success", version) })
}

// bootRunning starts a supervisor and waits until the bootstrap serves.
func (b *box) bootRunning(extra ...string) *supProc {
	b.t.Helper()
	p := b.startSup(extra...)
	b.waitID("boot", 10*time.Second)
	b.waitFor("supervisor.pid", 5*time.Second, func() bool { _, ok := supervisorPID(b.l); return ok })
	return p
}

// ---- scenarios

// ISC-1, ISC-11, ISC-A-6: a healthy apply switches, the event is recorded, SIGTERM exits 0.
func TestIntegrationSwitchSuccess(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	res := b.mustApply("v1.0.1", "")
	if !res.Notified {
		t.Fatalf("supervisor was not notified: %+v", res)
	}
	b.waitID("v1.0.1", 10*time.Second)
	b.waitFor("success event", 10*time.Second, func() bool { return b.hasEvent("success", "v1.0.1") })
	st := b.state()
	if b.l.Link("current") != "v1.0.1" || st.Pending != nil || st.Phase != "idle" || !st.PreviousIsBootstrap {
		t.Fatalf("state %+v current %q", st, b.l.Link("current"))
	}
	if err := p.stop(10 * time.Second); err != nil {
		t.Fatalf("SIGTERM exit: %v", err) // ISC-11: status 0
	}
	if !b.childGone() {
		t.Fatal("child still serving after the supervisor stopped")
	}
	if _, err := os.Stat(b.l.PIDPath()); !os.IsNotExist(err) {
		t.Fatal("pid file left behind")
	}
	p.noAlarmWords()
}

// ISC-2: /ready never answers within the timeout; the previous release comes back.
func TestIntegrationReadyTimeoutRollsBack(t *testing.T) {
	b := newBox(t)
	b.bootRunning()
	b.applyHealthy("v1.0.1")
	b.mustApply("v1.0.2", "ready=never")
	b.waitFor("rollback event", 15*time.Second, func() bool { return b.hasEvent("rolled_back", "v1.0.2") })
	b.waitID("v1.0.1", 10*time.Second)
	st := b.state()
	if b.l.Link("current") != "v1.0.1" || !st.IsBad("v1.0.2") || st.Pending != nil {
		t.Fatalf("state %+v current %q", st, b.l.Link("current"))
	}
}

// ISC-3: a smoke failure of either kind rolls back.
func TestIntegrationSmokeFailureRollsBack(t *testing.T) {
	for _, behavior := range []string{"smoke=500", "smoke=fail", "smoke=errors"} {
		t.Run(behavior, func(t *testing.T) {
			b := newBox(t)
			p := b.bootRunning()
			b.applyHealthy("v1.0.1")
			b.mustApply("v1.0.2", behavior)
			b.waitFor("rollback event", 15*time.Second, func() bool { return b.hasEvent("rolled_back", "v1.0.2") })
			b.waitID("v1.0.1", 10*time.Second)
			if !b.state().IsBad("v1.0.2") {
				t.Fatal("v1.0.2 not bad-listed")
			}
			p.noAlarmWords()
		})
	}
}

// waitBaseline waits until the running child's smoke result is on record, so
// the next switch is judged relative to it.
func (b *box) waitBaseline(root int) {
	b.t.Helper()
	b.waitFor("smoke baseline", 10*time.Second, func() bool { return b.state().SmokeBaseline["GET /"] == root })
}

// Smoke checks are judged against the last good run: a box with no home page
// applies a good release, and a release that changes "/" for the worse does not stay.
func TestIntegrationSmokeIsRelativeToLastGoodRun(t *testing.T) {
	t.Run("no home page applies a good release", func(t *testing.T) {
		b := newBox(t)
		b.writeBootstrap("root=404")
		b.bootRunning()
		b.waitBaseline(404)
		b.mustApply("v1.0.1", "root=404")
		b.waitID("v1.0.1", 10*time.Second)
		b.waitFor("success event", 10*time.Second, func() bool { return b.hasEvent("success", "v1.0.1") })
		if b.state().IsBad("v1.0.1") {
			t.Fatal("good release bad-listed")
		}
	})
	for name, root := range map[string]string{"200 to 500": "500", "200 to 404": "404"} {
		t.Run(name+" rolls back", func(t *testing.T) {
			b := newBox(t)
			b.bootRunning()
			b.waitBaseline(200)
			b.mustApply("v1.0.1", "root="+root)
			b.waitFor("rollback event", 15*time.Second, func() bool { return b.hasEvent("rolled_back", "v1.0.1") })
			b.waitID("boot", 10*time.Second)
			if !b.state().IsBad("v1.0.1") {
				t.Fatal("v1.0.1 not bad-listed")
			}
		})
	}
}

// ISC-4, ISC-5: three boot-window crashes roll back, and the bad version is never retried.
func TestIntegrationCrashLoopRollsBackAndBadListHolds(t *testing.T) {
	b := newBox(t)
	b.bootRunning()
	b.applyHealthy("v1.0.1")
	b.mustApply("v1.0.2", "crash_after_ms=100")
	b.waitFor("rollback event", 15*time.Second, func() bool { return b.hasEvent("rolled_back", "v1.0.2") })
	b.waitID("v1.0.1", 10*time.Second)
	if !b.state().IsBad("v1.0.2") {
		t.Fatal("v1.0.2 not bad-listed")
	}
	eventsBefore := len(b.events())
	_, err := b.apply(b.artifact("v1.0.2", "crash_after_ms=100"))
	if err == nil || !strings.Contains(err.Error(), "bad list") {
		t.Fatalf("apply of a bad version: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := len(b.events()); n != eventsBefore {
		t.Fatalf("a refused apply produced events: %v", b.events())
	}
	if id, _ := b.id(); id != "v1.0.1" {
		t.Fatalf("serving %q after refused apply", id)
	}
}

// ISC-6: SIGTERM in the middle of a switch leaves a consistent box, and a
// restarted supervisor finishes the job.
func TestIntegrationSIGTERMDuringApply(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	b.mustApply("v1.0.1", "ready_delay_ms=1500") // slow to become ready: the gate is open
	time.Sleep(300 * time.Millisecond)
	if err := p.stop(10 * time.Second); err != nil {
		t.Fatalf("exit after SIGTERM mid-switch: %v", err)
	}

	st := b.state() // must parse
	if cur := b.l.Link("current"); cur != "" && !b.l.Valid(cur) {
		t.Fatalf("current -> %q is not a valid release", cur)
	}
	if prev := b.l.Link("previous"); prev != "" && !b.l.Valid(prev) {
		t.Fatalf("previous -> %q is not a valid release", prev)
	}
	if st.Pending == nil && b.l.Link("current") != "v1.0.1" && !b.hasEvent("failed", "v1.0.1") {
		t.Fatalf("switch neither pending nor finished: %+v", st)
	}
	ents, _ := os.ReadDir(b.l.Releases)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("staging dir %s left behind", e.Name())
		}
	}

	b.startSup()
	b.waitID("v1.0.1", 15*time.Second)
	b.waitFor("success event", 15*time.Second, func() bool { return b.hasEvent("success", "v1.0.1") })
	if st := b.state(); st.Pending != nil || st.Phase != "idle" || b.l.Link("current") != "v1.0.1" {
		t.Fatalf("not consistent after restart: %+v current %q", st, b.l.Link("current"))
	}
}

// ISC-7: a bootstrap binary that changed on disk wins over current.
func TestIntegrationChangedBootstrapWins(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	b.applyHealthy("v1.0.1")
	if err := p.stop(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	b.writeBootstrap("ready_delay_ms=1") // a different binary: a deploy
	b.startSup()
	b.waitID("boot", 10*time.Second)
}

// ISC-8: a semver-newer bootstrap wins; a non-semver stamp never counts as newer.
func TestIntegrationBootstrapVersionRules(t *testing.T) {
	for _, tc := range []struct{ name, version, want string }{
		{"semver newer", "v2.0.0", "boot"},
		{"semver older", "v1.0.0", "v1.0.1"},
		{"non-semver", "dev-abc123", "v1.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBox(t)
			p := b.bootRunning()
			b.applyHealthy("v1.0.1")
			if err := p.stop(10 * time.Second); err != nil {
				t.Fatal(err)
			}
			b.version = tc.version
			b.startSup()
			b.waitID(tc.want, 10*time.Second)
		})
	}
}

// ISC-9: current flipped by hand per RUNBOOK §8 is honoured at the next start.
func TestIntegrationHandFlippedCurrent(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	if err := p.stop(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	b.install("v1.0.7", "")
	flip := `cd "$1" && ln -sfn v1.0.7 current.new && mv -T current.new current`
	if out, err := exec.Command("sh", "-c", flip, "sh", b.l.Releases).CombinedOutput(); err != nil {
		t.Fatalf("flip: %v %s", err, out)
	}
	b.startSup()
	b.waitID("v1.0.7", 10*time.Second)
}

// ISC-10: an unwritable releases dir runs the bootstrap, says why and stays up.
func TestIntegrationUnwritableReleasesRunsBootstrap(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	b := newBox(t)
	if err := os.MkdirAll(b.l.Releases, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(b.l.Releases, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(b.l.Releases, 0o755) })
	p := b.startSup()
	b.waitID("boot", 10*time.Second)
	time.Sleep(500 * time.Millisecond)
	if !p.alive() {
		t.Fatalf("supervisor exited\n%s", p.logs.String())
	}
	if !strings.Contains(p.logs.String(), "not writable") {
		t.Fatalf("log does not say why:\n%s", p.logs.String())
	}
	ents, _ := os.ReadDir(b.l.Releases)
	if len(ents) != 0 {
		t.Fatalf("wrote into an unwritable releases dir: %v", ents)
	}
}

// R4: SIGHUP with nothing pending changes nothing; neither process dies.
func TestIntegrationSIGHUPIdleIsHarmless(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	if err := p.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if !p.alive() {
		t.Fatalf("supervisor died on SIGHUP\n%s", p.logs.String())
	}
	if id, ok := b.id(); !ok || id != "boot" {
		t.Fatalf("child gone after SIGHUP: %q %v", id, ok)
	}
	if !strings.Contains(p.logs.String(), "nothing pending") {
		t.Fatalf("hup not handled:\n%s", p.logs.String())
	}
}

// A stop request is not a crash: the child is not restarted and no rollback runs.
func TestIntegrationStopIsNotACrash(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	b.applyHealthy("v1.0.1")
	if err := p.stop(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.logs.String(), "exited unexpectedly") {
		t.Fatalf("stop counted as a crash:\n%s", p.logs.String())
	}
	if len(b.state().Bad) != 0 {
		t.Fatal("bad list not empty after a clean stop")
	}
}

// A binary swapped into releases/<v>/ after staging is never run: the
// supervisor re-verifies against the signed record, bad-lists it and runs the
// bootstrap.
func TestIntegrationTamperedStagedBinaryIsNotRun(t *testing.T) {
	b := newBox(t)
	p := b.bootRunning()
	if err := p.stop(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	b.install("v1.0.7", "")
	if err := os.WriteFile(b.l.Binary("v1.0.7"), stubWithBehavior(t, "id evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	flip := `cd "$1" && ln -sfn v1.0.7 current.new && mv -T current.new current`
	if out, err := exec.Command("sh", "-c", flip, "sh", b.l.Releases).CombinedOutput(); err != nil {
		t.Fatalf("flip: %v %s", err, out)
	}
	b.startSup()
	b.waitID("boot", 10*time.Second)
	b.waitFor("bad list", 5*time.Second, func() bool { return b.state().IsBad("v1.0.7") })
}
