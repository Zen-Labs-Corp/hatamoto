package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execRig makes the restore rig accept the db tools and log their order. The
// archive has no plugins, so no database pool is opened.
func execRig(t *testing.T, log *[]string, onCall func(name string)) *restoreRig {
	t.Helper()
	r := newRestoreRig(t)
	base := r.o.Run
	r.o.Run = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return base(ctx, env, name, args...)
		}
		*log = append(*log, name)
		if onCall != nil {
			onCall(name)
		}
		return nil, nil
	}
	return r
}

func realRun(t *testing.T, r *restoreRig, src string) (*RestorePlan, error) {
	t.Helper()
	return Restore(t.Context(), r.o, RestoreRequest{Source: src, AdminURL: "postgres://su:pw@h/postgres"})
}

func noPlugins(s *archSpec) {
	s.Mf.Plugins = nil
	s.Mf.ConfigPath = "" // tests must never write the recorded /etc path
	s.Parts = []string{coreName, secretsName, configName, contentName}
}

func TestRestoreRunsEveryStepInOrder(t *testing.T) {
	var log []string
	var mediaAtPsql, secretsAtPsql bool
	r := execRig(t, &log, nil)
	r.o.Run = wrapRun(r.o.Run, func(name string) {
		if name == "psql" {
			_, err := os.Stat(r.o.MediaDir)
			mediaAtPsql = err == nil
			_, err = os.Stat(r.o.SecretsDir)
			secretsAtPsql = err == nil
		}
	})
	out := filepath.Join(t.TempDir(), "hatamoto.env")
	plan, err := Restore(t.Context(), r.o, RestoreRequest{
		Source: buildArchive(t, noPlugins), AdminURL: "postgres://su:pw@h/postgres", ConfigOut: out})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(log, ","); got != "pg_restore,psql" {
		t.Fatalf("exec order = %s", got)
	}
	if mediaAtPsql || secretsAtPsql {
		t.Error("files were written before the database steps finished")
	}
	for path, want := range map[string]string{
		filepath.Join(r.o.MediaDir, "2026", "a.txt"): "aaaa",
		filepath.Join(r.o.MediaDir, "b.txt"):         "bb",
		filepath.Join(r.o.SecretsDir, secretsName):   "SECRET_LINE\n",
		out: "CFG=1\n",
	} {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", path, b, err, want)
		}
	}
	var buf bytes.Buffer
	plan.Print(&buf)
	if !plan.Done || !strings.Contains(buf.String(), "done     ") || !strings.HasSuffix(buf.String(), "restore complete\n") ||
		strings.Contains(buf.String(), "would") {
		t.Errorf("output:\n%s", buf.String())
	}
}

// wrapRun calls hook for every non-version exec before running next.
func wrapRun(next ExecFunc, hook func(name string)) ExecFunc {
	return func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		if !(len(args) == 1 && args[0] == "--version") {
			hook(name)
		}
		return next(ctx, env, name, args...)
	}
}

func TestRestoreSecretsPermsAndExisting(t *testing.T) {
	var log []string
	r := execRig(t, &log, nil)
	if _, err := realRun(t, r, buildArchive(t, noPlugins)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(r.o.SecretsDir, secretsName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.env: %v, %v", fi, err)
	}
	if di, _ := os.Stat(r.o.SecretsDir); di.Mode().Perm() != 0o700 {
		t.Errorf("secrets dir mode = %v", di.Mode().Perm())
	}
	if m, _ := filepath.Glob(filepath.Join(r.o.SecretsDir, ".secrets-*")); len(m) != 0 {
		t.Errorf("temp files left: %v", m)
	}

	// The same bytes already there: accepted, untouched.
	var log2 []string
	r2 := execRig(t, &log2, nil)
	if err := os.MkdirAll(r2.o.SecretsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r2.o.SecretsDir, secretsName), []byte("SECRET_LINE\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := realRun(t, r2, buildArchive(t, noPlugins)); err != nil {
		t.Fatal(err)
	}

	// Different bytes: refused before any other write.
	var log3 []string
	r3 := execRig(t, &log3, nil)
	if err := os.MkdirAll(r3.o.SecretsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r3.o.SecretsDir, secretsName), []byte("OTHER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = realRun(t, r3, buildArchive(t, noPlugins))
	wantRefusal(t, err, "different secrets.env")
	if len(log3) != 0 {
		t.Errorf("tools ran despite the refusal: %v", log3)
	}
	if _, err := os.Stat(r3.o.MediaDir); err == nil {
		t.Error("media dir created despite the refusal")
	}
}

func TestRestoreConfigNeverOverwritten(t *testing.T) {
	var log []string
	r := execRig(t, &log, nil)
	out := filepath.Join(t.TempDir(), "existing.env")
	if err := os.WriteFile(out, []byte("MINE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Restore(t.Context(), r.o, RestoreRequest{
		Source: buildArchive(t, noPlugins), AdminURL: "postgres://su:pw@h/postgres", ConfigOut: out})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "MINE\n" {
		t.Errorf("config overwritten: %q", b)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "|"), "not overwritten") {
		t.Errorf("warnings = %v", plan.Warnings)
	}
}

func TestRestoreConfigOutWinsOverRecordedPath(t *testing.T) {
	var log []string
	r := execRig(t, &log, nil)
	recorded := filepath.Join(t.TempDir(), "recorded.env")
	out := filepath.Join(t.TempDir(), "out.env")
	arch := buildArchive(t, func(s *archSpec) { noPlugins(s); s.Mf.ConfigPath = recorded })
	if _, err := Restore(t.Context(), r.o, RestoreRequest{Source: arch, AdminURL: "postgres://su:pw@h/postgres", ConfigOut: out}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("config not written to --config-out")
	}
	if _, err := os.Stat(recorded); err == nil {
		t.Error("config written to the recorded path too")
	}
}

func TestRestoreMediaTraversalRefusedBeforeAnyWrite(t *testing.T) {
	var log []string
	r := execRig(t, &log, nil)
	arch := buildArchive(t, func(s *archSpec) {
		noPlugins(s)
		s.Media = map[string]string{"media/ok.txt": "ok", "media/../evil.txt": "x"}
	})
	_, err := realRun(t, r, arch)
	wantRefusal(t, err, "outside the media dir")
	if len(log) != 0 {
		t.Errorf("tools ran: %v", log)
	}
	if _, err := os.Stat(r.o.MediaDir); err == nil {
		t.Error("media dir created")
	}
}

func TestRestoreMediaSkipsUnsafeAndNonRegular(t *testing.T) {
	// restoreMedia on its own: a hostile entry is skipped even if it got here.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{{"media/../evil.txt", "x"}, {"media/good.txt", "g"}} {
		_ = tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body))})
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	arch := buildArchive(t, func(s *archSpec) { noPlugins(s); s.Media = map[string]string{"media/good.txt": "g"} })
	sc, err := scanArchive(arch)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "uploads")
	x := &restorer{o: RestoreOptions{MediaDir: dir}, src: arch, scan: sc, mf: sc.mf}
	if err := x.restoreMedia(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "good.txt")); string(b) != "g" {
		t.Errorf("good.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Error("escaped the media dir")
	}
}

func TestRestoreStopsAtFailingDatabaseStepBeforeFiles(t *testing.T) {
	var log []string
	r := execRig(t, &log, nil)
	r.o.Run = func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte(name + " (PostgreSQL) 16.2\n"), nil
		}
		return []byte("pg_restore: error: boom\n"), os.ErrInvalid
	}
	_, err := realRun(t, r, buildArchive(t, noPlugins))
	if err == nil || !strings.Contains(err.Error(), "restore stopped at core database") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(r.o.MediaDir); err == nil {
		t.Error("media written after a failed core restore")
	}
}
