package backup

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/media"
)

// rollbackRig is a restorer that has done everything: core, bootstrap, one
// plugin, media, secrets.env and config. Its psql calls are recorded.
type rollbackRig struct {
	x          *restorer
	stmts      []string // the -c statement of each psql call
	envs       [][]string
	failOn     string // a psql statement containing this fails
	dir        string
	mediaDir   string
	secretsEnv string
	config     string
	preexist   string // a file the run did not write
}

func newRollbackRig(t *testing.T) *rollbackRig {
	t.Helper()
	r := &rollbackRig{dir: t.TempDir()}
	r.mediaDir = filepath.Join(r.dir, "media")
	store, err := media.NewLocalStore(r.mediaDir, "/media")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a/one.bin", "a/b/two.bin"} {
		if _, err := store.Put(context.Background(), key, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	secretsDir := filepath.Join(r.dir, "secrets")
	r.secretsEnv = filepath.Join(secretsDir, "secrets.env")
	r.config = filepath.Join(r.dir, "hatamoto.conf")
	r.preexist = filepath.Join(r.dir, "keep.txt")
	for p, body := range map[string]string{r.secretsEnv: "k=v", r.config: "c", r.preexist: "keep"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.x = &restorer{
		o: RestoreOptions{
			MediaDir:    r.mediaDir,
			DatabaseURL: "postgres://hm_dst:pw@db.invalid:5432/target",
		},
		req: RestoreRequest{AdminURL: "postgres://adm:pw2@db.invalid:5432/postgres"},
		log: slog.New(slog.DiscardHandler),
		mf:  manifest{Plugins: []string{"webhooks"}, Roles: []string{"hm_p_abcd1234_webhooks", "hatamoto_role_admin_abcd1234"}},
		run: func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
			stmt := args[len(args)-1]
			r.stmts = append(r.stmts, stmt)
			r.envs = append(r.envs, env)
			if r.failOn != "" && strings.Contains(stmt, r.failOn) {
				return []byte("ERROR: permission denied"), errors.New("exit status 1")
			}
			return nil, nil
		},
	}
	r.x.led = rollbackLedger{
		coreDone: true, bootstrapStarted: true, plugins: []string{"webhooks"},
		store: store, mediaKeys: []string{"a/one.bin", "a/b/two.bin"}, mediaDirCreated: true,
		secretsCreated: r.secretsEnv, secretsDirCreated: true, configCreated: r.config,
	}
	return r
}

func TestRollbackUndoesEverythingTheRunWrote(t *testing.T) {
	r := newRollbackRig(t)
	cause := errors.New("restore stopped at media: boom")
	err := r.x.rollback(context.Background(), cause)
	var rf *RestoreFailure
	if !errors.As(err, &rf) || !errors.Is(err, cause) {
		t.Fatalf("err = %v, want a RestoreFailure wrapping the cause", err)
	}
	want := cause.Error() + "\n" +
		"rolled back: database emptied, roles hm_p_abcd1234_webhooks, hatamoto_role_admin_abcd1234 dropped, " +
		"2 media objects removed, secrets.env removed, config removed\n" +
		"target unchanged: fix the cause and run the command again"
	if err.Error() != want {
		t.Fatalf("error text:\n%s\nwant:\n%s", err.Error(), want)
	}
	for _, p := range []string{r.mediaDir, filepath.Dir(r.secretsEnv), r.config} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s still there (%v)", p, err)
		}
	}
	if _, err := os.Stat(r.preexist); err != nil {
		t.Errorf("a file the run did not write was touched: %v", err)
	}
	// Order: plugin schema, plugin role, admin schema, grant, admin role, database.
	wantStmts := []string{"DROP SCHEMA", "drop_plugin_role('webhooks')", "hatamoto_admin CASCADE", "REVOKE SET ON PARAMETER", "DROP ROLE IF EXISTS hatamoto_role_admin_abcd1234", "pg_database_owner"}
	if len(r.stmts) != len(wantStmts) {
		t.Fatalf("%d statements, want %d:\n%s", len(r.stmts), len(wantStmts), strings.Join(r.stmts, "\n"))
	}
	for i, w := range wantStmts {
		if !strings.Contains(r.stmts[i], w) {
			t.Errorf("statement %d = %s, want it to contain %q", i, r.stmts[i], w)
		}
	}
	if !strings.Contains(strings.Join(r.envs[2], " "), "PGUSER=adm") || !strings.Contains(strings.Join(r.envs[0], " "), "PGUSER=hm_dst") {
		t.Errorf("plugin steps run as the app role, admin steps as the admin URL role:\n%v\n%v", r.envs[0], r.envs[2])
	}
}

func TestRollbackKeepsWhatItDidNotCreate(t *testing.T) {
	r := newRollbackRig(t)
	r.x.led.mediaDirCreated, r.x.led.secretsDirCreated = false, false
	r.x.led.secretsCreated, r.x.led.configCreated = "", ""
	if err := os.WriteFile(filepath.Join(r.mediaDir, "other.bin"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.x.rollback(context.Background(), errors.New("stopped")); err == nil {
		t.Fatal("no error")
	}
	for _, p := range []string{r.mediaDir, r.secretsEnv, r.config, filepath.Join(r.mediaDir, "other.bin")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(r.mediaDir, "a")} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s was not cleaned: %v", p, err)
		}
	}
}

func TestRollbackFailureListsLeftoverAndKeepsGoing(t *testing.T) {
	r := newRollbackRig(t)
	r.failOn = "DROP ROLE IF EXISTS"
	err := r.x.rollback(context.Background(), errors.New("stopped"))
	text := err.Error()
	if strings.Contains(text, "target unchanged") || !strings.Contains(text, "rollback incomplete: the target is NOT unchanged") {
		t.Fatalf("text = %s", text)
	}
	cmd := `  role hatamoto_role_admin_abcd1234: psql -d target -c 'DROP ROLE IF EXISTS hatamoto_role_admin_abcd1234'`
	if !strings.Contains(text, cmd) {
		t.Fatalf("leftover line %q missing:\n%s", cmd, text)
	}
	if strings.Contains(text, "hatamoto_role_admin_abcd1234 dropped") || !strings.Contains(text, "hm_p_abcd1234_webhooks dropped") {
		t.Fatalf("rolled-back list is wrong:\n%s", text)
	}
	if !strings.Contains(text, "database emptied") || !strings.Contains(text, "2 media objects removed") {
		t.Fatalf("later steps did not run:\n%s", text)
	}
	if strings.Contains(text, "pw") {
		t.Fatalf("a password leaked into the text:\n%s", text)
	}
	if n := strings.Count(text, "\n  "); n != 1 {
		t.Fatalf("%d leftovers, want 1:\n%s", n, text)
	}
}

func TestRollbackNothingWritten(t *testing.T) {
	r := newRollbackRig(t)
	r.x.led = rollbackLedger{}
	cause := errors.New("restore stopped at core database: boom")
	err := r.x.rollback(context.Background(), cause)
	want := cause.Error() + "\nrolled back: nothing had been written\ntarget unchanged: fix the cause and run the command again"
	if err.Error() != want {
		t.Fatalf("text = %s", err.Error())
	}
	if len(r.stmts) != 0 {
		t.Fatalf("a rollback with nothing written ran %v", r.stmts)
	}
}

func TestRollbackMediaLeftover(t *testing.T) {
	r := newRollbackRig(t)
	r.x.led.store = failingDelete{r.x.led.store}
	text := r.x.rollback(context.Background(), errors.New("stopped")).Error()
	if !strings.Contains(text, "  media a/one.bin: delete a/one.bin from the media store") {
		t.Fatalf("text = %s", text)
	}
}

type failingDelete struct{ media.BlobStore }

func (failingDelete) Delete(context.Context, string) error { return errors.New("denied") }

func TestRollbackDropsOnlySchemasTheRunCreated(t *testing.T) {
	r := newRollbackRig(t)
	r.x.led.schemasBefore = []string{"analytics", "it's"}
	if err := r.x.rollback(context.Background(), errors.New("stopped")); err == nil {
		t.Fatal("no error")
	}
	last := r.stmts[len(r.stmts)-1]
	for _, want := range []string{"ARRAY['public','information_schema','analytics','it''s']::name[]", "nspname <> ALL"} {
		if !strings.Contains(last, want) {
			t.Errorf("empty-database statement lacks %q:\n%s", want, last)
		}
	}
}

func TestRecordSchemasKeepsWhatTheTargetHad(t *testing.T) {
	x := &dbRestorer{restorer: &restorer{o: RestoreOptions{Q: &fakeQuerier{schemas: [][]string{{"analytics"}, {"pg_catalog"}}}}}}
	if err := x.recordSchemas(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(x.led.schemasBefore, ","); got != "analytics,pg_catalog" {
		t.Fatalf("schemasBefore = %s", got)
	}
}
