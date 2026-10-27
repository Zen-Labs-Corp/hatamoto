package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

const testPlatform = "linux-amd64"

// applyBox is a data dir plus a signer holding throwaway keys.
type applyBox struct {
	t    *testing.T
	dir  string
	l    Layout
	key  packages.Key
	opts release.Options
	cfg  Config
}

func newApplyBox(t *testing.T, bootstrapVersion string) *applyBox {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := k.PublicKey()
	ts := packages.TrustSet{}
	ts.Add(pub)
	dir := t.TempDir()
	boot := filepath.Join(dir, "bootstrap-bin")
	if err := os.WriteFile(boot, []byte("bootstrap"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &applyBox{t: t, dir: dir, l: NewLayout(filepath.Join(dir, "data")), key: k,
		opts: release.Options{Release: ts},
		cfg:  Config{Data: filepath.Join(dir, "data"), Bootstrap: boot, BootstrapVersion: bootstrapVersion}}
}

// markRan records that the current release ran and passed here (as a
// supervisor start would), so the next apply sees it as a real previous.
func (b *applyBox) markRan() {
	b.t.Helper()
	err := WithLock(b.l.LockPath(), func() error {
		st := b.state()
		st.Run, st.CurrentSeen, st.Unproven, st.Ungated = RunRelease, b.l.Link("current"), "", nil
		return SaveState(b.l.StatePath(), st)
	})
	if err != nil {
		b.t.Fatal(err)
	}
}

// artifact writes a binary, its record and signature and returns ApplyOpts.
func (b *applyBox) artifact(version, body string, mod func(*release.Record)) ApplyOpts {
	b.t.Helper()
	sub := filepath.Join(b.dir, "art-"+version+"-"+strconv.Itoa(len(body)))
	_ = os.MkdirAll(sub, 0o755)
	rec := release.Record{Version: version, Kind: "patch", SDK: "0.10.0", RollbackFloor: "v1.0.0", MinFrom: "v1.0.0",
		Artifacts: map[string]release.Artifact{testPlatform: {SHA256: shaOf(body), Size: int64(len(body))}}}
	if mod != nil {
		mod(&rec)
	}
	recBytes, err := release.MarshalRecord(rec)
	if err != nil {
		b.t.Fatal(err)
	}
	sig, err := packages.SignBytes(b.key, recBytes)
	if err != nil {
		b.t.Fatal(err)
	}
	o := ApplyOpts{File: filepath.Join(sub, "hatamoto"), Record: filepath.Join(sub, "release.json"),
		Sig: filepath.Join(sub, "release.json.sig"), Release: b.opts, Platform: testPlatform}
	for p, c := range map[string]string{o.File: body, o.Record: string(recBytes), o.Sig: sig.Signer + ":" + sig.Signature + "\n"} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			b.t.Fatal(err)
		}
	}
	return o
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (b *applyBox) state() *State {
	b.t.Helper()
	st, err := LoadState(b.l.StatePath())
	if err != nil {
		b.t.Fatal(err)
	}
	return st
}

func TestApplyOfflineSwitch(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	res, err := Apply(b.cfg, b.artifact("v1.1.0", "binary-1.1.0", nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Notified || !strings.Contains(res.Message, "staged and switched to v1.1.0") || !strings.Contains(res.Message, "restart hatamoto") {
		t.Fatalf("message: %+v", res)
	}
	if b.l.Link("current") != "v1.1.0" || !b.l.Valid("v1.1.0") {
		t.Fatalf("current=%q", b.l.Link("current"))
	}
	for _, f := range []string{"hatamoto", "release.json", "release.json.sig"} {
		if _, err := os.Stat(filepath.Join(b.l.Releases, "v1.1.0", f)); err != nil {
			t.Fatal(err)
		}
	}
	st := b.state()
	if st.Pending != nil || st.Run != RunRelease || st.CurrentSeen != "v1.1.0" || !st.PreviousIsBootstrap || st.BootstrapSHA256 == "" {
		t.Fatalf("state: %+v", st)
	}
	// A first supervisor start keeps the applied release.
	c := Choose(ChooseInput{BootstrapSHA: st.BootstrapSHA256, BootstrapVersion: "v1.0.0", Current: "v1.1.0", CurrentOK: true}, st)
	if c.Bootstrap {
		t.Fatalf("applied release lost to bootstrap: %+v", c)
	}
	if ev, _ := os.ReadFile(b.l.EventsPath()); !strings.Contains(string(ev), `"to":"v1.1.0"`) {
		t.Fatalf("events: %s", ev)
	}
}

func TestApplyRefusals(t *testing.T) {
	good := func(b *applyBox) ApplyOpts { return b.artifact("v1.1.0", "binary-1.1.0", nil) }
	cases := []struct {
		name string
		prep func(b *applyBox) ApplyOpts
		want string
	}{
		{"bad signature", func(b *applyBox) ApplyOpts {
			o := good(b)
			other, _ := packages.GenerateKey()
			b.key = other
			o2 := b.artifact("v1.1.0", "binary-1.1.0", nil)
			o.Sig = o2.Sig
			return o
		}, "record check failed"},
		{"unknown key", func(b *applyBox) ApplyOpts {
			other, _ := packages.GenerateKey()
			b.key = other
			return good(b)
		}, "record check failed"},
		{"wrong platform", func(b *applyBox) ApplyOpts { o := good(b); o.Platform = "plan9-arm"; return o }, "artifact check failed"},
		{"hash mismatch", func(b *applyBox) ApplyOpts {
			o := good(b)
			if err := os.WriteFile(o.File, []byte("binary-9.9.9"), 0o644); err != nil {
				t.Fatal(err)
			}
			return o
		}, "artifact check failed"},
		{"missing sig file", func(b *applyBox) ApplyOpts {
			o := good(b)
			_ = os.Remove(o.Sig)
			o.Sig = ""
			return o
		}, "signature"},
		{"bad list", func(b *applyBox) ApplyOpts {
			st := &State{V: 1, Phase: "idle"}
			st.AddBad("v1.1.0", "smoke")
			_ = os.MkdirAll(b.l.Releases, 0o755)
			_ = SaveState(b.l.StatePath(), st)
			return good(b)
		}, "bad list"},
		{"same version", func(b *applyBox) ApplyOpts { return b.artifact("v1.0.0", "x", nil) }, "already running"},
		{"min_from", func(b *applyBox) ApplyOpts {
			return b.artifact("v1.1.0", "binary-1.1.0", func(r *release.Record) { r.MinFrom = "v1.0.5" })
		}, "needs at least v1.0.5"},
		{"rollback_floor", func(b *applyBox) ApplyOpts {
			dir := filepath.Join(b.l.Releases, "v1.5.0")
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "hatamoto"), []byte("x"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, "release.json"), []byte(`{"rollback_floor":"v1.4.0"}`), 0o644)
			_ = b.l.SwapSymlink("current", "v1.5.0")
			st := &State{V: 1, Phase: "idle", Run: RunRelease, CurrentSeen: "v1.5.0"}
			_ = SaveState(b.l.StatePath(), st)
			return b.artifact("v1.3.0", "old", nil)
		}, "rollback floor v1.4.0"},
		{"different binary same version", func(b *applyBox) ApplyOpts {
			dir := filepath.Join(b.l.Releases, "v1.1.0")
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "hatamoto"), []byte("other"), 0o755)
			return good(b)
		}, "different binary"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newApplyBox(t, "v1.0.0")
			o := c.prep(b)
			_, err := Apply(b.cfg, o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
			if st := b.state(); st.Pending != nil {
				t.Fatalf("refused apply left pending: %+v", st.Pending)
			}
		})
	}
}

func TestApplyNonSemverBootstrapNeverBlocks(t *testing.T) {
	b := newApplyBox(t, "dev-abc123")
	if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", func(r *release.Record) { r.MinFrom = "v9.0.0" })); err != nil {
		t.Fatalf("unknown running version blocked apply: %v", err)
	}
}

// Staging goes through an unpredictable temp dir: a planted .tmp-<version>
// symlink is never followed or written through.
func TestApplyStagesInRandomTempDir(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	victim := filepath.Join(b.dir, "victim")
	if err := os.Symlink(victim, filepath.Join(b.l.Releases, ".tmp-v1.1.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("staged through a planted symlink: %v", err)
	}
	if _, err := os.Stat(b.l.Binary("v1.1.0")); err != nil {
		t.Fatal(err)
	}
}

// A binary whose release dir is a symlink target is refused at open time.
func TestWriteSyncedRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hatamoto")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeSynced(link, strings.NewReader("new"), 0o755); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Fatalf("target changed: %q", got)
	}
}

// The CLI refuses a uid that does not own releases/.
func TestApplyAndRollbackRefuseOtherUID(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	old := geteuid
	geteuid = func() int { return os.Geteuid() + 4242 }
	t.Cleanup(func() { geteuid = old })
	_, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil))
	if err == nil || !strings.Contains(err.Error(), "sudo -u") || !strings.Contains(err.Error(), "docker exec -u") {
		t.Fatalf("apply: %v", err)
	}
	if _, err := Rollback(b.cfg); err == nil || !strings.Contains(err.Error(), "sudo -u") {
		t.Fatalf("rollback: %v", err)
	}
	if _, serr := os.Stat(b.l.StatePath()); serr == nil {
		t.Fatal("state written by the wrong user")
	}
}

// The advice names the real owner and never tells anyone to run as root.
func TestOwnerAdviceNeverSuggestsRoot(t *testing.T) {
	root := ownerAdvice("releases", 0, "root", 10001)
	other := ownerAdvice("releases", 10001, "app", 0)
	for _, msg := range []string{root, other} {
		if strings.Contains(msg, "-u root") || strings.Contains(msg, "sudo root") {
			t.Errorf("advises root: %s", msg)
		}
	}
	if !strings.Contains(root, "chown") || strings.Contains(root, "docker exec") {
		t.Errorf("root-owned advice: %s", root)
	}
	if !strings.Contains(other, "sudo -u app") || !strings.Contains(other, "docker exec -u 10001") {
		t.Errorf("owner advice: %s", other)
	}
}

func TestStateRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	_ = os.WriteFile(real, []byte("{}"), 0o600)
	link := filepath.Join(dir, "state.json")
	_ = os.Symlink(real, link)
	if _, err := LoadState(link); err == nil {
		t.Fatal("followed a symlinked state.json")
	}
}

func TestApplyThenManualRollbackBadLists(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	o := b.artifact("v1.1.0", "binary", nil)
	if _, err := Apply(b.cfg, o); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(b.cfg); err != nil {
		t.Fatal(err)
	}
	if st := b.state(); !st.IsBad("v1.1.0") {
		t.Fatal("manual rollback should bad-list v1.1.0")
	}
}

func TestApplyNotifiesRunningSupervisor(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	// A real process stands in for the supervisor and records SIGHUP.
	marker := filepath.Join(b.dir, "hup")
	cmd := exec.Command("sh", "-c", `trap 'touch "$0"; exit 0' HUP; while :; do sleep 0.05; done`, marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	holdPIDFile(t, b.l, strconv.Itoa(cmd.Process.Pid))
	time.Sleep(200 * time.Millisecond) // let sh install the trap

	res, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Notified || !strings.Contains(res.Message, "supervisor notified") {
		t.Fatalf("result: %+v", res)
	}
	st := b.state()
	if st.Pending == nil || st.Pending.Kind != "apply" || st.Pending.Version != "v1.1.0" {
		t.Fatalf("pending: %+v", st.Pending)
	}
	if b.l.Link("current") != "" {
		t.Fatal("the CLI must leave the links to the supervisor")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor never got SIGHUP")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestApplyRefusesWhileSwitchPending(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	_ = SaveState(b.l.StatePath(), &State{V: 1, Phase: "probation", Pending: &Pending{Kind: "apply", Version: "v1.0.9"}})
	if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil)); err == nil || !strings.Contains(err.Error(), "still in progress") {
		t.Fatalf("got %v", err)
	}
}

func TestRollback(t *testing.T) {
	t.Run("no previous", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		if _, err := Rollback(b.cfg); err == nil || !strings.Contains(err.Error(), "no previous release") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("to bootstrap", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := Rollback(b.cfg); err != nil {
			t.Fatal(err)
		}
		st := b.state()
		if st.Run != RunBootstrap || !st.IsBad("v1.1.0") || st.Pending != nil {
			t.Fatalf("state: %+v", st)
		}
		if b.l.Link("current") != "v1.1.0" {
			t.Fatal("current should stay; current_seen keeps the bootstrap")
		}
	})
	t.Run("to previous release", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		for _, v := range []string{"v1.1.0", "v1.2.0"} {
			if _, err := Apply(b.cfg, b.artifact(v, "bin-"+v, nil)); err != nil {
				t.Fatal(err)
			}
			if v == "v1.1.0" {
				b.markRan()
			}
		}
		if _, err := Rollback(b.cfg); err != nil {
			t.Fatal(err)
		}
		if b.l.Link("current") != "v1.1.0" || !b.state().IsBad("v1.2.0") {
			t.Fatalf("current=%q bad=%v", b.l.Link("current"), b.state().Bad)
		}
	})
	t.Run("bad-listed previous is refused without force", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		for _, v := range []string{"v1.1.0", "v1.2.0"} {
			if _, err := Apply(b.cfg, b.artifact(v, "bin-"+v, nil)); err != nil {
				t.Fatal(err)
			}
			if v == "v1.1.0" {
				b.markRan()
			}
		}
		_ = WithLock(b.l.LockPath(), func() error {
			st := b.state()
			st.AddBad("v1.1.0", "crash loop")
			return SaveState(b.l.StatePath(), st)
		})
		if _, err := Rollback(b.cfg); err == nil || !strings.Contains(err.Error(), "bad list") {
			t.Fatalf("got %v", err)
		}
		if b.l.Link("current") != "v1.2.0" {
			t.Fatal("refused rollback moved the links")
		}
		b.cfg.Force = true
		if _, err := Rollback(b.cfg); err != nil || b.l.Link("current") != "v1.1.0" {
			t.Fatalf("forced rollback: %v current=%s", err, b.l.Link("current"))
		}
	})
	t.Run("unknown bootstrap version is refused when a rollback floor exists", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		if _, err := Apply(b.cfg, b.artifact("v1.1.0", "bin-v1.1.0", nil)); err != nil { // floor v1.0.0
			t.Fatal(err)
		}
		// A child of an older supervisor: no state.bootstrap_version, and the
		// caller is the release, not the bootstrap, so it has no stamp to offer.
		if v := b.state().BootstrapVersion; v != "" {
			t.Fatalf("test needs a state without bootstrap_version, got %q", v)
		}
		b.cfg.BootstrapVersion = ""
		if _, err := Rollback(b.cfg); err == nil || !strings.Contains(err.Error(), "version is unknown") {
			t.Fatalf("got %v", err)
		}
		if b.l.Link("current") != "v1.1.0" || b.state().Pending != nil {
			t.Fatal("refused rollback changed state")
		}
	})
	t.Run("second offline apply keeps previous at what ran before the first", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0") // nothing but the bootstrap has run
		for _, v := range []string{"v1.1.0", "v1.2.0"} {
			if _, err := Apply(b.cfg, b.artifact(v, "bin-"+v, nil)); err != nil {
				t.Fatal(err)
			}
		}
		if got := b.l.Link("previous"); got != "" || !b.state().PreviousIsBootstrap {
			t.Fatalf("previous = %q (bootstrap=%v): v1.1.0 never ran and must not be the rollback target", got, b.state().PreviousIsBootstrap)
		}
		if _, err := Rollback(b.cfg); err != nil || b.state().Run != RunBootstrap {
			t.Fatalf("rollback: %v state %+v", err, b.state())
		}
	})
	t.Run("second offline apply after a run release", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		if _, err := Apply(b.cfg, b.artifact("v1.1.0", "bin-a", nil)); err != nil {
			t.Fatal(err)
		}
		b.markRan() // v1.1.0 ran and passed
		for _, v := range []string{"v1.2.0", "v1.3.0"} {
			if _, err := Apply(b.cfg, b.artifact(v, "bin-"+v, nil)); err != nil {
				t.Fatal(err)
			}
		}
		if got := b.l.Link("previous"); got != "v1.1.0" {
			t.Fatalf("previous = %q, want the last release that ran", got)
		}
	})
	t.Run("rollback to a release that never ran needs force", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		for _, v := range []string{"v1.1.0", "v1.2.0"} {
			if _, err := Apply(b.cfg, b.artifact(v, "bin-"+v, nil)); err != nil {
				t.Fatal(err)
			}
		}
		// A state written before previous was kept at the last good release.
		if err := b.l.SwapSymlink("previous", "v1.1.0"); err != nil {
			t.Fatal(err)
		}
		_ = WithLock(b.l.LockPath(), func() error {
			st := b.state()
			st.Ungated = &Pending{Kind: "apply", Version: "v1.1.0", Before: &LinkSnapshot{Current: "v1.0.0", Run: RunRelease}}
			return SaveState(b.l.StatePath(), st)
		})
		if _, err := Rollback(b.cfg); err == nil || !strings.Contains(err.Error(), "never ran") {
			t.Fatalf("got %v", err)
		}
		if b.l.Link("current") != "v1.2.0" {
			t.Fatal("refused rollback moved the links")
		}
		b.cfg.Force = true
		if _, err := Rollback(b.cfg); err != nil || b.l.Link("current") != "v1.1.0" {
			t.Fatalf("forced rollback: %v current=%s", err, b.l.Link("current"))
		}
	})
	t.Run("keeps the previous release's proven status", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		for _, v := range []string{"v1.1.0", "v1.2.0"} {
			if _, err := Apply(b.cfg, b.artifact(v, "bin-"+v, nil)); err != nil {
				t.Fatal(err)
			}
			if v == "v1.1.0" {
				b.markRan()
			}
		}
		if b.state().Unproven != "v1.2.0" {
			t.Fatalf("setup: %+v", b.state())
		}
		if _, err := Rollback(b.cfg); err != nil {
			t.Fatal(err)
		}
		if st := b.state(); st.Unproven != "" || st.Ungated != nil {
			t.Fatalf("the rollback target was good before, it must not become unproven: %+v", st)
		}
	})
	t.Run("floor", func(t *testing.T) {
		b := newApplyBox(t, "v1.0.0")
		if _, err := Apply(b.cfg, b.artifact("v1.1.0", "a", nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(b.cfg, b.artifact("v1.2.0", "b", func(r *release.Record) { r.RollbackFloor = "v1.1.5" })); err != nil {
			t.Fatal(err)
		}
		if _, err := Rollback(b.cfg); err == nil || !strings.Contains(err.Error(), "rollback floor v1.1.5") {
			t.Fatalf("got %v", err)
		}
	})
}

// holdPIDFile writes content into supervisor.pid and holds the flock on it
// for the rest of the test, like a live supervisor.
func holdPIDFile(t *testing.T, l Layout, content string) {
	t.Helper()
	_ = os.MkdirAll(l.Releases, 0o755)
	f, err := os.OpenFile(l.PIDPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content + "\n"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
}

// The supervisor is PID 1 in the image: a pid file saying 1 with the lock
// held must be notified, not bypassed by an ungated link swap.
func TestApplyNotifiesSupervisorWithPID1(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	holdPIDFile(t, b.l, "1")
	var got []int
	b.cfg.notify = func(pid int) error { got = append(got, pid); return nil }
	res, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Notified || len(got) != 1 || got[0] != 1 {
		t.Fatalf("notified=%v signals=%v", res.Notified, got)
	}
	if b.state().Pending == nil || b.l.Link("current") != "" {
		t.Fatal("the switch must be left to the supervisor")
	}
}

// A pid file nobody holds the lock on (crash, power loss, reused pid) is
// stale: no signal goes out and the offline path runs.
func TestApplyIgnoresStalePIDFile(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	_ = os.WriteFile(b.l.PIDPath(), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644) // alive, but no lock
	b.cfg.notify = func(pid int) error { t.Fatalf("signalled %d from a stale pid file", pid); return nil }
	res, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Notified || b.l.Link("current") != "v1.1.0" || b.state().Pending != nil {
		t.Fatalf("expected the offline switch: %+v", res)
	}
	if b.state().Unproven != "v1.1.0" {
		t.Fatalf("an ungated switch must leave the release unproven: %+v", b.state())
	}
}

func TestSecondSupervisorCannotTakeTheLock(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	l1, err := acquirePIDLock(b.l.PIDPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { lockRetry = d }(lockRetry)
	lockRetry = 100 * time.Millisecond
	if _, err := acquirePIDLock(b.l.PIDPath()); err == nil {
		t.Fatal("second lock succeeded")
	}
	if pid, ok := supervisorPID(b.l); !ok || pid != os.Getpid() {
		t.Fatalf("holder: %d %v", pid, ok)
	}
	l1.release()
	if _, ok := supervisorPID(b.l); ok {
		t.Fatal("released lock still reads as running")
	}
}

func TestVerifyStored(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil)); err != nil {
		t.Fatal(err)
	}
	// Apply used the test platform; VerifyStored uses the real one.
	if runtime.GOOS+"-"+runtime.GOARCH != testPlatform {
		t.Skip("stored-binary check runs for the host platform only")
	}
	if err := VerifyStored(b.l, "v1.1.0", b.opts); err != nil {
		t.Fatalf("freshly staged release: %v", err)
	}
	if err := VerifyStored(b.l, "v1.1.0", release.Options{Release: packages.TrustSet{}}); err == nil {
		t.Error("accepted a record signed by an untrusted key")
	}
	if err := os.Rename(filepath.Join(b.l.Releases, "v1.1.0"), filepath.Join(b.l.Releases, "v1.2.0")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStored(b.l, "v1.2.0", b.opts); err == nil || !strings.Contains(err.Error(), "not v1.2.0") {
		t.Errorf("record for another version: %v", err)
	}
	_ = os.Rename(filepath.Join(b.l.Releases, "v1.2.0"), filepath.Join(b.l.Releases, "v1.1.0"))
	if err := os.WriteFile(b.l.Binary("v1.1.0"), []byte("planted"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStored(b.l, "v1.1.0", b.opts); err == nil {
		t.Error("accepted a planted binary")
	}
	_ = os.Remove(filepath.Join(b.l.Releases, "v1.1.0", "release.json"))
	if err := VerifyStored(b.l, "v1.1.0", b.opts); err == nil {
		t.Error("accepted a release with no record")
	}
}

// What lands on disk is what was hashed: a source that differs from the
// record, or runs past its size, stages nothing.
func TestWriteVerifiedChecksTheBytesItWrites(t *testing.T) {
	art := release.Artifact{SHA256: shaOf("binary"), Size: 6}
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok")
	if err := writeVerified(ok, strings.NewReader("binary"), art, release.Options{}, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{"same size, other bytes": "binarY", "grew": "binary+more", "short": "bin"} {
		if err := writeVerified(filepath.Join(dir, "x"), strings.NewReader(src), art, release.Options{}, 0o755); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := writeVerified(filepath.Join(dir, "y"), strings.NewReader("binary"), art, release.Options{MaxArtifact: 3}, 0o755); err == nil {
		t.Error("size cap ignored")
	}
}

func TestApplyStagesNothingForAChangedSource(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	o := b.artifact("v1.1.0", "binary", nil)
	if err := os.WriteFile(o.File, []byte("swapped!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(b.cfg, o); err == nil || !strings.Contains(err.Error(), "artifact check failed") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(b.l.Binary("v1.1.0")); err == nil {
		t.Fatal("staged a binary that does not match its record")
	}
	ents, _ := os.ReadDir(b.l.Releases)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("left %s behind", e.Name())
		}
	}
}

// The CLI's liveness probe takes a shared lock for an instant. A supervisor
// starting at that moment waits for it instead of exiting.
func TestSupervisorStartSurvivesTheCLIProbe(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	probe, err := os.OpenFile(b.l.PIDPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := flockNB(probe, syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	fd, unlocked := int(probe.Fd()), make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		close(unlocked)
	})
	defer func() { <-unlocked }() // the probe is closed only after the callback is done with its fd
	l, err := acquirePIDLock(b.l.PIDPath())
	if err != nil {
		t.Fatalf("supervisor refused to start under a probe: %v", err)
	}
	l.release()
}

// An offline apply has not been gated: it logs "staged-offline", and only the
// gate at the next start logs success.
func TestOfflineApplyLogsStagedNotSuccess(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil)); err != nil {
		t.Fatal(err)
	}
	evs := readEvents(t, b.l.EventsPath())
	if len(evs) != 1 || evs[0].Phase != PhaseStagedOffline {
		t.Fatalf("events after an offline apply: %+v", evs)
	}
}

// An offline rollback away from the bootstrap has no release to bad-list and
// is not re-gated as an apply; it logs its own phase, and the state remembers
// it so the next start's gate can report how the target came up.
func TestOfflineRollbackFromBootstrapLogsItsOwnPhase(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	if _, err := Apply(b.cfg, b.artifact("v1.1.0", "binary", nil)); err != nil {
		t.Fatal(err)
	}
	st := b.state() // the bootstrap is what runs now (a restart chose it)
	st.Run = RunBootstrap
	if err := WithLock(b.l.LockPath(), func() error { return SaveState(b.l.StatePath(), st) }); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(b.cfg); err != nil {
		t.Fatal(err)
	}
	evs := readEvents(t, b.l.EventsPath())
	if len(evs) != 2 || evs[1].Phase != PhaseRolledBackOffline {
		t.Fatalf("events: %+v", evs)
	}
	if b.state().OfflineRollback == "" {
		t.Fatalf("state does not remember the offline rollback: %+v", b.state())
	}
}

func TestStageLeavesLinksAndPendingAlone(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	o := b.artifact("v1.1.0", "binary-1.1.0", nil)
	res, err := Stage(b.cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "v1.1.0" || !strings.Contains(res.Message, "restart to apply") {
		t.Fatalf("result: %+v", res)
	}
	if !b.l.Valid("v1.1.0") || b.l.Link("current") != "" || b.l.Link("previous") != "" {
		t.Fatalf("links moved: current=%q previous=%q", b.l.Link("current"), b.l.Link("previous"))
	}
	if st := b.state(); st.Pending != nil || st.Run == RunRelease {
		t.Fatalf("state: %+v", st)
	}
	cfg := b.cfg
	cfg.Release = b.opts
	rec, err := StoredRecord(cfg, "v1.1.0")
	if err != nil || rec.Version != "v1.1.0" {
		t.Fatalf("StoredRecord: %+v %v", rec, err)
	}
	if _, err := StoredRecord(cfg, "v9.9.9"); err == nil {
		t.Fatal("record of an unstaged release")
	}
	// Staging again is harmless, and a later Apply reuses the staged binary.
	if _, err := Stage(b.cfg, o); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(b.cfg, o); err != nil {
		t.Fatal(err)
	}
}

func TestRunningFollowsThePIDLock(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_ = os.MkdirAll(b.l.Releases, 0o755)
	if Running(b.cfg) {
		t.Fatal("running without a holder")
	}
	l, err := acquirePIDLock(b.l.PIDPath())
	if err != nil {
		t.Fatal(err)
	}
	if !Running(b.cfg) {
		t.Fatal("holder not seen")
	}
	l.release()
	if Running(b.cfg) {
		t.Fatal("released lock still running")
	}
}
