package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/supervisor"
	"hatamoto.dev/core/internal/updater"
)

// signedRelease writes a binary, record and signature signed by a throwaway
// key and returns the paths with the trust options that accept them.
func signedRelease(t *testing.T, version string) (file, record, sig string, ro release.Options) {
	t.Helper()
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := key.PublicKey()
	ts := packages.TrustSet{}
	ts.Add(pub)
	body := []byte("pretend binary " + version)
	sum := sha256.Sum256(body)
	rec, err := release.MarshalRecord(release.Record{Version: version, Kind: "patch", SDK: "0.10.0",
		RollbackFloor: "v1.0.0", MinFrom: "v1.0.0",
		Artifacts: map[string]release.Artifact{runtime.GOOS + "-" + runtime.GOARCH: {SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := packages.SignBytes(key, rec)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file, record, sig = filepath.Join(dir, "hatamoto"), filepath.Join(dir, "release.json"), filepath.Join(dir, "release.json.sig")
	for p, c := range map[string][]byte{file: body, record: rec, sig: []byte(s.Signer + ":" + s.Signature)} {
		if err := os.WriteFile(p, c, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return file, record, sig, release.Options{Release: ts}
}

func TestRunUpdateApplyAndRollback(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	file, record, sig, ro := signedRelease(t, "v1.1.0")

	var out bytes.Buffer
	if err := runUpdateWith([]string{"apply", "--file", file, "--record", record, "--sig", sig}, &out, ro); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "staged and switched to v1.1.0") {
		t.Fatalf("output: %q", out.String())
	}
	target, err := os.Readlink(filepath.Join(data, "releases", "current"))
	if err != nil || target != "v1.1.0" {
		t.Fatalf("current -> %q, %v", target, err)
	}

	// The shipped trust set does not contain the throwaway key.
	data2 := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data2)
	err = runUpdateWith([]string{"apply", "--file", file, "--record", record, "--sig", sig}, &out, release.Options{})
	if err == nil || !strings.Contains(err.Error(), "record check failed") {
		t.Fatalf("embedded trust accepted a throwaway key: %v", err)
	}

	out.Reset()
	if err := runUpdateWith([]string{"rollback"}, &out, ro); err == nil || !strings.Contains(err.Error(), "no previous release") {
		t.Fatalf("rollback on a fresh dir: %v", err)
	}
}

func TestParseUpdateArgsErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"apply"}, {"apply", "--file", "f"}, {"apply", "--bogus"}, {"rollback", "x"}} {
		if _, err := parseUpdateArgs(args); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}

func TestParseUpdateRollbackForce(t *testing.T) {
	u, err := parseUpdateArgs([]string{"rollback", "--force"})
	if err != nil || !u.rollback || !u.force {
		t.Fatalf("%+v %v", u, err)
	}
	if u, err = parseUpdateArgs([]string{"rollback"}); err != nil || u.force {
		t.Fatalf("%+v %v", u, err)
	}
}

// Without HATAMOTO_DATA the CLI must not create ./data in a random cwd.
func TestUpdateRefusesUnknownDefaultDataDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HATAMOTO_DATA", "")
	var out bytes.Buffer
	err := runUpdateWith([]string{"rollback"}, &out, release.Options{})
	if err == nil || !strings.Contains(err.Error(), "no supervisor state") || !strings.Contains(err.Error(), "cd /opt/hatamoto") {
		t.Fatalf("got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(cwd, "data")); serr == nil {
		t.Fatal("created ./data in the cwd")
	}
}

func TestParseUpdateNewVerbs(t *testing.T) {
	ok := map[string]updateArgs{
		"status":                    {verb: "status"},
		"check":                     {verb: "check"},
		"check --dry-run":           {verb: "check", dryRun: true},
		"pin v1.4":                  {verb: "pin", pin: "v1.4"},
		"unpin":                     {verb: "unpin"},
		"apply v2.0.0":              {verb: "fetch", version: "v2.0.0"},
		"apply v2.0.0 --force":      {verb: "fetch", version: "v2.0.0", force: true},
		"apply v1.0.0-rc.1 --force": {verb: "fetch", version: "v1.0.0-rc.1", force: true},
	}
	for line, want := range ok {
		got, err := parseUpdateArgs(strings.Fields(line))
		if err != nil || got != want {
			t.Errorf("%q: got %+v, %v; want %+v", line, got, err, want)
		}
	}
	for _, line := range []string{"pin 1.4", "pin v1", "pin v1.4.2", "pin", "pin v1.4 v1.5", "apply 2.0.0", "apply v2.0",
		"apply v2.0.0 --bogus", "check x", "status x", "unpin x", "bogus"} {
		if _, err := parseUpdateArgs(strings.Fields(line)); err == nil {
			t.Errorf("%q: want error", line)
		}
	}
}

func TestRenderUpdateStatus(t *testing.T) {
	at := time.Date(2026, 11, 5, 3, 12, 0, 0, time.UTC)
	set := updater.Settings{Mode: "auto", Channel: "stable"}
	cases := []struct {
		name string
		set  updater.Settings
		st   updater.Status
		want string
	}{
		{"never checked", set, updater.Status{}, "version     v1.0.0 (channel stable, mode auto)\nstatus      no check yet\nlast check  never\n"},
		{"blocked", set, updater.Status{Decision: "blocked", Reason: "plugin hello needs SDK ^0.10 (target v1.0.1 has 0.11.0)", LastCheckAt: &at, LastResult: "ok"},
			"version     v1.0.0 (channel stable, mode auto)\nstatus      blocked: plugin hello needs SDK ^0.10 (target v1.0.1 has 0.11.0)\nlast check  2026-11-05T03:12:00Z ok\n"},
		{"scheduled", set, updater.Status{Decision: "scheduled", Target: "v1.0.1", Reason: "v1.0.1 is rolling out (bucket 42, ramp 5%)", LastCheckAt: &at, LastResult: "ok"},
			"version     v1.0.0 (channel stable, mode auto)\nstatus      scheduled: v1.0.1 is rolling out (bucket 42, ramp 5%)\nlast check  2026-11-05T03:12:00Z ok\n"},
		{"major", updater.Settings{Mode: "notify", Channel: "edge", Pin: "v1.0"}, updater.Status{Decision: "available_major", AvailableMajor: "v2.0.0", Reason: "v2.0.0 is a major release; apply it by hand", LastCheckAt: &at, LastResult: "ok"},
			"version     v1.0.0 (channel edge, mode notify)\npin         v1.0\nstatus      available major: v2.0.0 is a major release; apply it by hand\nmajor       v2.0.0 is available; install it with `hatamoto update apply v2.0.0`\nlast check  2026-11-05T03:12:00Z ok\n"},
		{"error", set, updater.Status{Decision: "up_to_date", Reason: "fetch channel stable: boom", LastCheckAt: &at, LastResult: "error"},
			"version     v1.0.0 (channel stable, mode auto)\nstatus      up to date: fetch channel stable: boom\nlast check  2026-11-05T03:12:00Z error\n"},
		{"rolled back", set, updater.Status{Decision: "rolled_back", Target: "v1.0.1", Reason: "health gate failed", LastCheckAt: &at, LastResult: "ok"},
			"version     v1.0.0 (channel stable, mode auto)\nstatus      rolled back v1.0.1: health gate failed\nlast check  2026-11-05T03:12:00Z ok\n"},
		{"off", set, updater.Status{Decision: "off", LastCheckAt: &at, LastResult: "skipped"},
			"version     v1.0.0 (channel stable, mode auto)\nstatus      off\nlast check  2026-11-05T03:12:00Z skipped\n"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		renderUpdateStatus(&out, "v1.0.0", c.set, c.st)
		if out.String() != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, out.String(), c.want)
		}
	}
}

func TestUpdateStatusNeedsADatabase(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	for _, verb := range []string{"status", "check", "pin v1.0", "unpin"} {
		var out bytes.Buffer
		err := runUpdateWith(strings.Fields(verb), &out, release.Options{})
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Errorf("%s: want a DATABASE_URL error, got %v", verb, err)
		}
	}
}

func TestRunningVersionOfFollowsTheSupervisor(t *testing.T) {
	data := t.TempDir()
	cfg := supervisor.Config{Data: data, BootstrapVersion: "v1.0.0"}
	l := supervisor.NewLayout(data)
	if got := runningVersionOf(cfg); got != "v1.0.0" {
		t.Fatalf("fresh box: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(l.Releases, "v1.0.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.SwapSymlink("current", "v1.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.SaveState(l.StatePath(), &supervisor.State{Run: supervisor.RunRelease, CurrentSeen: "v1.0.2"}); err != nil {
		t.Fatal(err)
	}
	if got := runningVersionOf(cfg); got != "v1.0.2" {
		t.Fatalf("after a self-update: %s", got)
	}
	if err := supervisor.SaveState(l.StatePath(), &supervisor.State{Run: supervisor.RunBootstrap, CurrentSeen: "v1.0.2"}); err != nil {
		t.Fatal(err)
	}
	if got := runningVersionOf(cfg); got != "v1.0.0" {
		t.Fatalf("bootstrap won: %s", got)
	}
}

func TestUpdateOnlineVerbsRefuseAForeignOwner(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("HATAMOTO_UPDATE_URL", "")
	old := checkOwner
	checkOwner = func(supervisor.Layout) error { return errors.New("owned by somebody else") }
	defer func() { checkOwner = old }()
	for _, verb := range []string{"check", "check --dry-run", "pin v1.0", "unpin", "apply v1.0.1"} {
		err := runUpdateWith(strings.Fields(verb), io.Discard, release.Options{})
		if err == nil || !strings.Contains(err.Error(), "owned by somebody else") {
			t.Errorf("%s: want the owner refusal, got %v", verb, err)
		}
	}
	// status only reads.
	err := runUpdateWith([]string{"status"}, io.Discard, release.Options{})
	if err == nil || strings.Contains(err.Error(), "owned by") {
		t.Errorf("status must not check ownership: %v", err)
	}
	for _, f := range []string{"install_id", "backups", "releases"} {
		if _, err := os.Lstat(filepath.Join(data, f)); err == nil {
			t.Errorf("%s was created by a refused command", f)
		}
	}
}

func TestParseUpdateApplyNoBackup(t *testing.T) {
	u, err := parseUpdateArgs([]string{"apply", "v1.0.1", "--no-backup", "--force"})
	if err != nil || u.verb != "fetch" || !u.noBackup || !u.force {
		t.Fatalf("%+v %v", u, err)
	}
	if u, err = parseUpdateArgs([]string{"apply", "v1.0.1"}); err != nil || u.noBackup {
		t.Fatalf("default must back up: %+v %v", u, err)
	}
	if _, err = parseUpdateArgs([]string{"apply", "v1.0.1", "--bogus"}); err == nil || !strings.Contains(err.Error(), "--no-backup") {
		t.Fatalf("usage must name the flag: %v", err)
	}
}

func TestUpdateApplyWithoutADatabaseIsRefusedUnlessNoBackup(t *testing.T) {
	t.Setenv("HATAMOTO_DATA", t.TempDir())
	_, err := noDatabaseBackups{}.PreUpdate(t.Context(), "v1.0.1")
	if err == nil || !strings.Contains(err.Error(), "use --no-backup") {
		t.Fatalf("want the refusal, got %v", err)
	}
	// The engine itself refuses without a pool, with the engine's own text.
	eng := newBackupEngine(nil, "", "v1.0.0", nil)
	if _, err := (preUpdateBackups{eng}).PreUpdate(t.Context(), "v1.0.1"); err == nil || !strings.Contains(err.Error(), "database not reachable") {
		t.Fatalf("got %v", err)
	}
}

func TestUpdateRefusalBlockedByBackup(t *testing.T) {
	st := updater.Status{Decision: updater.KindBackupFailed, Target: "v1.0.1", LastResult: updater.ResultError,
		Reason: "pg_dump 18 does not match server 16"}
	for _, verb := range []string{"fetch", "check"} {
		err := updateRefusal(updateArgs{verb: verb, version: "v1.0.1"}, st)
		if err == nil {
			t.Fatalf("%s: a blocked update exited zero", verb)
		}
		for _, want := range []string{"v1.0.1", "pg_dump 18 does not match server 16", "--no-backup", "HATAMOTO_UPDATE_BACKUP=0"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q missing %q", verb, err, want)
			}
		}
	}
	if err := updateRefusal(updateArgs{verb: "check"}, updater.Status{Decision: updater.KindApplying}); err != nil {
		t.Fatalf("a clean run must not fail: %v", err)
	}
	if err := updateRefusal(updateArgs{verb: "fetch", version: "v1.0.1"}, updater.Status{LastResult: updater.ResultSkipped, Reason: "busy"}); err == nil {
		t.Fatal("skipped apply must still fail")
	}
}

func TestEveryInstallingVerbTakesABackup(t *testing.T) {
	for _, c := range []struct {
		u    updateArgs
		want bool
	}{
		{updateArgs{verb: "check"}, true},
		{updateArgs{verb: "fetch", version: "v1.0.1"}, true},
		{updateArgs{verb: "check", dryRun: true}, false},
		{updateArgs{verb: "fetch", noBackup: true}, false},
		{updateArgs{verb: "status"}, false},
	} {
		if got := takesBackup(c.u); got != c.want {
			t.Errorf("%+v: takesBackup=%v, want %v", c.u, got, c.want)
		}
	}
}
