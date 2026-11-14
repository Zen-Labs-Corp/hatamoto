package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

var restoreTrust = TrustIDs{Release: []string{"r1", "r2"}, Manifest: []string{"m1"}, Env: []string{"e1"}}

// archSpec describes a test archive. Hashes are computed from Parts when the
// archive is written; tamper then edits the manifest (to break a hash, say).
type archSpec struct {
	Parts  []string          // entry names, in order
	Bodies map[string][]byte // entry bodies
	Media  map[string]string // content.tar.gz entries by full name (media/...)
	Mf     manifest
	tamper func(*manifest)
}

func baseSpec() *archSpec {
	trust := restoreTrust
	return &archSpec{
		Bodies: map[string][]byte{coreName: []byte("core"), "plugin_forms.dump": []byte("forms"), secretsName: []byte("SECRET_LINE\n"), configName: []byte("CFG=1\n")},
		Parts:  []string{coreName, "plugin_forms.dump", secretsName, configName, contentName},
		Media:  map[string]string{"media/2026/a.txt": "aaaa", "media/b.txt": "bb"},
		Mf: manifest{
			Format: manifestFormat, Version: manifestVersion, Kind: KindManual,
			Created:         time.Date(2026, 10, 25, 10, 15, 0, 0, time.UTC),
			HatamotoVersion: "v1.2.3", ServerMajor: 16, PgDump: "16.2",
			Plugins: []string{"forms"}, Trust: &trust, ConfigPath: "/etc/hatamoto/hatamoto.env",
		},
	}
}

func contentGz(t *testing.T, media map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(media))
	for n := range media {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(media[n]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(media[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// buildArchive writes the archive of the spec after mut and returns its path.
func buildArchive(t *testing.T, mut func(*archSpec)) string {
	t.Helper()
	s := baseSpec()
	if mut != nil {
		mut(s)
	}
	s.Bodies[contentName] = contentGz(t, s.Media)
	var parts []part
	for _, n := range s.Parts {
		sum := sha256.Sum256(s.Bodies[n])
		parts = append(parts, part{Name: n, Size: int64(len(s.Bodies[n])), SHA256: hex.EncodeToString(sum[:])})
	}
	s.Mf.Parts = parts
	if s.tamper != nil {
		s.tamper(&s.Mf)
	}
	path := filepath.Join(t.TempDir(), "b.tar")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	write := func(name string, b []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range s.Parts {
		write(n, s.Bodies[n])
	}
	mfb, _ := jsonx.Marshal(s.Mf)
	write(manifestName, mfb)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type restoreRig struct {
	o     RestoreOptions
	q     *engineQuerier
	calls [][]string
	tools map[string]string // tool name to its --version output
	admin *engineQuerier    // answers the admin URL connection
	// adminURL is the URL OpenAdmin was last called with; adminErr fails it.
	adminURL string
	adminErr error
	grants   string // the SQL the rig's pg_restore prints for a dump's ACL entries
}

func newRestoreRig(t *testing.T) *restoreRig {
	t.Helper()
	r := &restoreRig{
		grants: "GRANT SELECT ON TABLE public.hatamoto_instance TO hm_src;\nGRANT SELECT ON TABLE public.hatamoto_instance TO reporting;\n",
		q:      &engineQuerier{major: 16, sysID: "7001", instance: "10.0.0.1:5432@t0"},
		admin:  &engineQuerier{major: 16, sysID: "7001", instance: "10.0.0.1:5432@t0"},
		tools:  map[string]string{"pg_restore": "pg_restore (PostgreSQL) 16.2", "psql": "psql (PostgreSQL) 16.2"},
	}
	base := t.TempDir()
	r.o = RestoreOptions{
		Data: filepath.Join(base, "data"), MediaDir: filepath.Join(base, "uploads"), SecretsDir: filepath.Join(base, "secrets"),
		DatabaseURL: testURL, Version: "v1.2.3", Q: r.q,
		Getenv:       func(string) string { return "" },
		Trust:        func() (TrustIDs, error) { return restoreTrust, nil },
		PauseOffsite: func(context.Context, string) error { return nil },
		OpenAdmin: func(_ context.Context, url string) (Querier, func(), error) {
			r.adminURL = url
			if r.adminErr != nil {
				return nil, nil, r.adminErr
			}
			return r.admin, func() {}, nil
		},
		Run: func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
			r.calls = append(r.calls, append([]string{name}, args...))
			if out, ok := r.tools[name]; ok && len(args) == 1 && args[0] == "--version" {
				return []byte(out + "\n"), nil
			}
			if name == "pg_restore" && len(args) > 0 {
				return r.readDump(args), nil
			}
			return nil, errors.New("unexpected exec " + name)
		},
	}
	return r
}

// restoreListing is what the rig's pg_restore -l prints for a dump: the source
// app role owns the instance table, and one ACL entry has no grantee in it.
const restoreListing = `;
2000; 1259 9 TABLE public hatamoto_instance hm_src
2001; 0 0 ACL public TABLE hatamoto_instance hm_src
`

// readDump answers the role analysis: the listing of any dump, and the grants
// of the ACL entries (r.grants, one statement per line).
func (r *restoreRig) readDump(args []string) []byte {
	if args[0] == "-l" {
		return []byte(restoreListing)
	}
	return []byte(r.grants)
}

// serverTools makes the server and both client tools report one major.
func (r *restoreRig) serverTools(major int) {
	r.q.major = int64(major)
	r.tools["pg_restore"] = fmt.Sprintf("pg_restore (PostgreSQL) %d.0", major)
	r.tools["psql"] = fmt.Sprintf("psql (PostgreSQL) %d.0", major)
}

func (r *restoreRig) dry(t *testing.T, src string) (*RestorePlan, error) {
	t.Helper()
	return Restore(t.Context(), r.o, RestoreRequest{Source: src, AdminURL: "postgres://su:pw@h/postgres", DryRun: true})
}

func wantRefusal(t *testing.T, err error, substrs ...string) {
	t.Helper()
	var ref *RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v, want a RefusalError", err)
	}
	for _, s := range substrs {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("refusal %q lacks %q", err.Error(), s)
		}
	}
}

func TestRestoreDryRunPlanOutput(t *testing.T) {
	r := newRestoreRig(t)
	plan, err := r.dry(t, buildArchive(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	plan.Print(&out)
	want := `warning  grants to role reporting are skipped (no such role here)
warning  config not written; recorded path "/etc/hatamoto/hatamoto.env"; pass --config-out with the path you want
backup   20261025T101500Z-manual (hatamoto "v1.2.3", pg 16, 1 plugin)
checks   version ok, pg major ok (backup 16, server 16), trust keys ok, admin url ok, target db empty, media dir empty, roles ok (hm_src -> u, 1 skipped)
would    restore core.dump, run role bootstrap, provision plugin_forms, restore plugin_forms.dump,
         restore 2 media files (0.0 MiB), write secrets.env
dry run: nothing written
`
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	// The recorded path is a hint only: nothing is written there.
	if w := "config not written; recorded path \"/etc/hatamoto/hatamoto.env\"; pass --config-out with the path you want"; !slices.Contains(plan.Warnings, w) {
		t.Errorf("warnings = %v", plan.Warnings)
	}
}

func TestRestoreSourceByIDAndPath(t *testing.T) {
	r := newRestoreRig(t)
	arch := buildArchive(t, nil)
	if _, err := r.dry(t, arch); err != nil {
		t.Fatalf("path: %v", err)
	}
	id := NewID(baseSpec().Mf.Created, KindManual)
	if err := os.MkdirAll(filepath.Join(r.o.Data, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(arch)
	if err := os.WriteFile(filepath.Join(r.o.Data, "backups", id+".tar"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.dry(t, id); err != nil {
		t.Fatalf("id: %v", err)
	}
	if _, err := r.dry(t, "20260101T000000Z-manual"); err == nil || !strings.Contains(err.Error(), "20260101T000000Z-manual") {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := r.dry(t, "../etc/passwd"); err == nil {
		t.Fatal("path without .tar and missing file accepted")
	}
	if _, err := r.dry(t, "not-an-id"); err == nil || !strings.Contains(err.Error(), "neither") {
		t.Fatalf("bad id: %v", err)
	}
}

func TestRestoreRefusals(t *testing.T) {
	cases := map[string]struct {
		mut   func(*archSpec)
		rig   func(*restoreRig)
		wants []string
	}{
		"version mismatch": {
			mut:   func(s *archSpec) { s.Mf.HatamotoVersion = "v1.0.0" },
			wants: []string{`hatamoto "v1.0.0"`, "this is v1.2.3", "matching release"},
		},
		"empty recorded version": {
			mut:   func(s *archSpec) { s.Mf.HatamotoVersion = "" },
			wants: []string{"records no hatamoto version"},
		},
		"empty running version": {
			rig:   func(r *restoreRig) { r.o.Version = "" },
			wants: []string{"this binary has no version"},
		},
		"backup newer than the server": {
			mut:   func(s *archSpec) { s.Mf.ServerMajor = 18 },
			wants: []string{"PostgreSQL 18, the server is 16", "restore into PostgreSQL 18 or newer"},
		},
		"backup below the supported floor": {
			mut:   func(s *archSpec) { s.Mf.ServerMajor = 15 },
			rig:   func(r *restoreRig) { r.serverTools(18) },
			wants: []string{"PostgreSQL 15; restore supports backups from PostgreSQL 16 or newer"},
		},
		"pg_restore older than the server": {
			rig:   func(r *restoreRig) { r.q.major = 18; r.tools["psql"] = "psql (PostgreSQL) 18.0" },
			wants: []string{"pg_restore 16 does not match the server's PostgreSQL 18"},
		},
		"pg_restore newer than the server": {
			rig:   func(r *restoreRig) { r.tools["pg_restore"] = "pg_restore (PostgreSQL) 19.1" },
			wants: []string{"pg_restore 19 does not match the server's PostgreSQL 16"},
		},
		"psql older than the server": {
			rig:   func(r *restoreRig) { r.q.major = 18; r.tools["pg_restore"] = "pg_restore (PostgreSQL) 18.0" },
			wants: []string{"psql 16 does not match the server's PostgreSQL 18"},
		},
		"psql newer than the server": {
			rig:   func(r *restoreRig) { r.tools["psql"] = "psql (PostgreSQL) 17.4" },
			wants: []string{"psql 17 does not match the server's PostgreSQL 16"},
		},
		"trust release IDs": {
			rig: func(r *restoreRig) {
				r.o.Trust = func() (TrustIDs, error) { return TrustIDs{Release: []string{"other"}, Manifest: []string{"m1"}}, nil }
			},
			wants: []string{"release signing keys", "other", "r1 r2"},
		},
		"trust manifest IDs": {
			rig: func(r *restoreRig) {
				r.o.Trust = func() (TrustIDs, error) { return TrustIDs{Release: []string{"r1", "r2"}}, nil }
			},
			wants: []string{"manifest signing keys", "none"},
		},
		"non-empty db": {
			rig:   func(r *restoreRig) { r.q.schemas = [][]string{{"public"}, {"hatamoto_admin"}} },
			wants: []string{"target database is not empty", "public, hatamoto_admin"},
		},
		"bad part hash": {
			mut: func(s *archSpec) {
				s.tamper = func(m *manifest) { m.Parts[0].SHA256 = strings.Repeat("0", 64) }
			},
			wants: []string{"part core.dump does not match its checksum"},
		},
		"missing part": {
			mut: func(s *archSpec) {
				s.Parts = []string{coreName, secretsName, configName, contentName} // plugin dump absent
				s.tamper = func(m *manifest) {
					m.Parts = append(m.Parts, part{Name: "plugin_forms.dump", Size: 5, SHA256: strings.Repeat("1", 64)})
				}
			},
			wants: []string{"part plugin_forms.dump is missing"},
		},
		"no core dump": {
			mut: func(s *archSpec) {
				s.Parts = []string{secretsName, configName, contentName}
				s.Mf.Plugins = nil
			},
			wants: []string{"no core.dump"},
		},
		"dot-dot media entry": {
			mut:   func(s *archSpec) { s.Media["media/../evil"] = "x" },
			wants: []string{`"media/../evil" would write outside`},
		},
		"absolute media entry": {
			mut:   func(s *archSpec) { s.Media["media//etc/evil"] = "x" },
			wants: []string{"would write outside"},
		},
		"plugin name that is no schema": {
			mut:   func(s *archSpec) { s.Mf.Plugins = []string{"x; drop"} },
			wants: []string{`plugin "x; drop"`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRestoreRig(t)
			if c.rig != nil {
				c.rig(r)
			}
			_, err := r.dry(t, buildArchive(t, c.mut))
			wantRefusal(t, err, c.wants...)
		})
	}
}

func TestRestoreRefusesAmbientLibpqVars(t *testing.T) {
	for _, name := range []string{"PGSERVICE", "PGSERVICEFILE", "PGHOSTADDR"} {
		t.Run(name, func(t *testing.T) {
			r := newRestoreRig(t)
			r.o.Getenv = func(k string) string {
				if k == name {
					return "x"
				}
				return ""
			}
			_, err := r.dry(t, buildArchive(t, nil))
			wantRefusal(t, err, name+" is set in the environment")
		})
	}
	r := newRestoreRig(t)
	if _, err := r.dry(t, buildArchive(t, nil)); err != nil {
		t.Fatalf("clean environment refused: %v", err)
	}
}

func TestRestoreRefusesNonEmptyMediaDir(t *testing.T) {
	r := newRestoreRig(t)
	if err := os.MkdirAll(r.o.MediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.o.MediaDir, "x.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := r.dry(t, buildArchive(t, nil))
	wantRefusal(t, err, "media dir", "is not empty")
}

func TestRestoreSecretsFile(t *testing.T) {
	r := newRestoreRig(t)
	arch := buildArchive(t, nil)
	if err := os.MkdirAll(r.o.SecretsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.o.SecretsDir, secretsName)
	if err := os.WriteFile(path, []byte("SECRET_LINE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.dry(t, arch); err != nil {
		t.Fatalf("identical secrets.env must be accepted: %v", err)
	}
	if err := os.WriteFile(path, []byte("different\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := r.dry(t, arch)
	wantRefusal(t, err, "different secrets.env")
}

func TestRestoreCollectsAllRefusals(t *testing.T) {
	r := newRestoreRig(t)
	r.q.schemas = [][]string{{"public"}}
	_, err := r.dry(t, buildArchive(t, func(s *archSpec) { s.Mf.HatamotoVersion = "v0"; s.Mf.ServerMajor = 17 }))
	wantRefusal(t, err, `hatamoto "v0"`, "the server is 16", "not empty")
}

// An older backup restores into a newer server when both tools match the
// server, and an equal major restores as before.
func TestRestoreAcceptsOlderAndEqualMajor(t *testing.T) {
	for _, c := range []struct{ backup, server int }{{16, 18}, {17, 18}, {16, 16}, {18, 18}} {
		r := newRestoreRig(t)
		r.serverTools(c.server)
		plan, err := r.dry(t, buildArchive(t, func(s *archSpec) { s.Mf.ServerMajor = c.backup }))
		if err != nil {
			t.Fatalf("backup %d into server %d: %v", c.backup, c.server, err)
		}
		want := fmt.Sprintf("pg major ok (backup %d, server %d)", c.backup, c.server)
		if !strings.Contains(strings.Join(plan.Checks, "|"), want) {
			t.Errorf("checks = %v, want %q", plan.Checks, want)
		}
	}
}

// Each wrong tool is reported on its own.
func TestRestoreReportsEachWrongToolSeparately(t *testing.T) {
	r := newRestoreRig(t)
	r.q.major = 18
	_, err := r.dry(t, buildArchive(t, nil))
	wantRefusal(t, err, "pg_restore 16 does not match the server's PostgreSQL 18", "psql 16 does not match the server's PostgreSQL 18")
}

// An archive from before secrets, config and trust IDs were recorded restores
// with warnings, never a refusal.
func TestRestoreOldArchiveWarns(t *testing.T) {
	r := newRestoreRig(t)
	plan, err := r.dry(t, buildArchive(t, func(s *archSpec) {
		s.Parts = []string{coreName, "plugin_forms.dump", contentName}
		s.Mf.Trust, s.Mf.ConfigPath = nil, ""
	}))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"no trust key IDs", "no secrets.env", "no config in the backup"} {
		if !strings.Contains(joined, w) {
			t.Errorf("warnings lack %q: %v", w, plan.Warnings)
		}
	}
	for _, c := range plan.Checks {
		if c == "trust keys ok" {
			t.Error("trust keys reported ok without a record")
		}
	}
	for _, w := range plan.Would {
		if w == "write secrets.env" || strings.HasPrefix(w, "write config") {
			t.Errorf("plan writes %q", w)
		}
	}
}

func TestRestoreEnvTrustDifferenceIsAWarning(t *testing.T) {
	r := newRestoreRig(t)
	r.o.Trust = func() (TrustIDs, error) {
		t := restoreTrust
		t.Env = []string{"zzz"}
		return t, nil
	}
	plan, err := r.dry(t, buildArchive(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 3 || !strings.Contains(plan.Warnings[0], "HATAMOTO_TRUSTED_KEYS") { // then the skipped grants, then the config hint
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestRestoreConfigTarget(t *testing.T) {
	r := newRestoreRig(t)
	arch := buildArchive(t, nil)
	out := filepath.Join(t.TempDir(), "mine.env")
	plan, err := Restore(t.Context(), r.o, RestoreRequest{Source: arch, AdminURL: "postgres://su:pw@h/postgres", ConfigOut: out, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if last := plan.Would[len(plan.Would)-1]; last != "write config to "+out {
		t.Fatalf("would = %v", plan.Would)
	}
	// An existing target is never a plan step; it warns.
	if err := os.WriteFile(out, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err = Restore(t.Context(), r.o, RestoreRequest{Source: arch, AdminURL: "postgres://su:pw@h/postgres", ConfigOut: out, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(plan.Would, ","), "write config") || !strings.Contains(strings.Join(plan.Warnings, "\n"), "not overwritten") {
		t.Fatalf("would = %v warnings = %v", plan.Would, plan.Warnings)
	}
	if b, _ := os.ReadFile(out); string(b) != "keep" {
		t.Fatal("existing config touched")
	}
}

func snapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			fi, _ := d.Info()
			got[p] = d.Type().String() + fi.Mode().String()
			if d.Type().IsRegular() {
				b, _ := os.ReadFile(p)
				got[p] += string(b)
			}
			return nil
		})
	}
	return got
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	r := newRestoreRig(t)
	base := filepath.Dir(r.o.MediaDir)
	arch := buildArchive(t, nil)
	tmpBefore, _ := filepath.Glob(filepath.Join(os.TempDir(), "hatamoto-restore-*"))
	before := snapshot(t, base)
	if _, err := r.dry(t, arch); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, base); !reflect.DeepEqual(before, after) {
		t.Fatalf("tree changed:\nbefore %v\nafter  %v", before, after)
	}
	for _, p := range []string{r.o.Data, r.o.MediaDir, r.o.SecretsDir} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists after a dry run: %v", p, err)
		}
	}
	if tmpAfter, _ := filepath.Glob(filepath.Join(os.TempDir(), "hatamoto-restore-*")); len(tmpAfter) != len(tmpBefore) {
		t.Error("dry run left temp files")
	}
	// Only --version probes and the read-only dump reads of the role check ran.
	for _, c := range r.calls {
		readOnly := c[0] == "pg_restore" && !slices.ContainsFunc(c, func(a string) bool { return strings.HasPrefix(a, "--dbname") })
		if !readOnly && (len(c) != 2 || c[1] != "--version") {
			t.Errorf("exec %v in a dry run", c)
		}
	}
}

func withRoles(s *archSpec) {
	s.Mf.Roles = []string{"hatamoto_role_admin_abcd1234", "hm_p_abcd1234_forms"}
}

// A cluster that already holds the backup's roles refuses the dry run and the
// real run alike, names the roles, and writes nothing.
func TestRestoreRefusesExistingClusterRoles(t *testing.T) {
	for _, dry := range []bool{true, false} {
		r := newRestoreRig(t)
		r.q.existingRoles = []string{"hm_p_abcd1234_forms", "hatamoto_role_admin_abcd1234", "unrelated"}
		base := filepath.Dir(r.o.MediaDir)
		before := snapshot(t, base)
		_, err := Restore(t.Context(), r.o, RestoreRequest{Source: buildArchive(t, withRoles), AdminURL: "postgres://su:pw@h/postgres", DryRun: dry})
		wantRefusal(t, err, "cluster roles hatamoto_role_admin_abcd1234, hm_p_abcd1234_forms already exist; drop them if a failed restore left them, or restore into a cluster without them")
		if strings.Contains(err.Error(), "unrelated") {
			t.Errorf("refusal names an unrelated role: %v", err)
		}
		if after := snapshot(t, base); !reflect.DeepEqual(before, after) {
			t.Fatalf("dry=%v: tree changed:\nbefore %v\nafter  %v", dry, before, after)
		}
		for _, c := range r.calls {
			if len(c) != 2 || c[1] != "--version" {
				t.Errorf("dry=%v: exec %v after a refusal", dry, c)
			}
		}
	}
}

func TestRestoreClusterRolesFreeOrUnrecorded(t *testing.T) {
	r := newRestoreRig(t)
	r.q.existingRoles = []string{"unrelated"}
	plan, err := r.dry(t, buildArchive(t, withRoles))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Checks, "cluster roles ok") {
		t.Errorf("checks = %v", plan.Checks)
	}
	// An archive without recorded roles is not checked, even if names clash.
	r = newRestoreRig(t)
	r.q.existingRoles = []string{"hatamoto_role_admin_abcd1234", "hm_p_abcd1234_forms"}
	plan, err = r.dry(t, buildArchive(t, nil))
	if err != nil {
		t.Fatalf("old manifest refused: %v", err)
	}
	if slices.Contains(plan.Checks, "cluster roles ok") {
		t.Errorf("checks = %v", plan.Checks)
	}
}

func TestRestoreRejectsNonBackups(t *testing.T) {
	r := newRestoreRig(t)
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.tar")
	if err := os.WriteFile(junk, []byte("not a tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.dry(t, junk); err == nil {
		t.Fatal("junk accepted")
	}
	noManifest := filepath.Join(dir, "nm.tar")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "core.dump", Mode: 0o600, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	if err := os.WriteFile(noManifest, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.dry(t, noManifest); err == nil || !strings.Contains(err.Error(), "no manifest.json") {
		t.Fatalf("err = %v", err)
	}
	wrongFormat := buildArchive(t, func(s *archSpec) { s.Mf.Format = "other" })
	if _, err := r.dry(t, wrongFormat); err == nil || !strings.Contains(err.Error(), "not a hatamoto backup") {
		t.Fatalf("err = %v", err)
	}
	newer := buildArchive(t, func(s *archSpec) { s.Mf.Version = objectMediaManifestVersion + 1 })
	if _, err := r.dry(t, newer); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreUnreachableDatabaseIsNotARefusal(t *testing.T) {
	r := newRestoreRig(t)
	r.o.Q = nil
	r.o.OpenPool = nil
	r.o.DatabaseURL = "postgres://u:p@127.0.0.1:1/none?connect_timeout=1"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := Restore(ctx, r.o, RestoreRequest{Source: buildArchive(t, nil), DryRun: true})
	var ref *RefusalError
	if err == nil || errors.As(err, &ref) || !strings.Contains(err.Error(), "cannot reach the target database") {
		t.Fatalf("err = %v", err)
	}
}

func TestWrapList(t *testing.T) {
	got := wrapList("would    ", []string{"a", strings.Repeat("b", 60), strings.Repeat("c", 40)})
	want := "would    a, " + strings.Repeat("b", 60) + ",\n         " + strings.Repeat("c", 40) + "\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRestoreConfigOutMustBeAbsoluteCleanWithParent(t *testing.T) {
	r := newRestoreRig(t)
	arch := buildArchive(t, nil)
	dir := t.TempDir()
	for out, want := range map[string]string{
		"rel.env":                            "absolute, clean",
		dir + "/../x.env":                    "absolute, clean",
		filepath.Join(dir, "nodir", "x.env"): "does not exist",
	} {
		_, err := Restore(t.Context(), r.o, RestoreRequest{Source: arch, AdminURL: "postgres://su:pw@h/postgres", ConfigOut: out, DryRun: true})
		wantRefusal(t, err, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "nodir")); err == nil {
		t.Error("a parent directory was created")
	}
}

// The admin URL and the target are checked in check(), so a dry run shows the
// refusal and nothing is written before it.
func TestRestoreChecksAdminURLAndTarget(t *testing.T) {
	arch := buildArchive(t, nil)
	for _, c := range []struct {
		name string
		mut  func(r *restoreRig, req *RestoreRequest)
		want string
	}{
		{"admin not a postgres url", func(_ *restoreRig, q *RestoreRequest) { q.AdminURL = "mysql://root@h/x" }, "--admin-url is not a postgres URL"},
		{"admin url missing", func(_ *restoreRig, q *RestoreRequest) { q.AdminURL = "" }, "no admin URL"},
		{"admin cannot connect", func(r *restoreRig, _ *RestoreRequest) { r.adminErr = errors.New("password authentication failed") }, "--admin-url cannot connect"},
		{"admin is neither a superuser nor CREATEROLE and CREATEDB", func(r *restoreRig, _ *RestoreRequest) { r.admin.notSuper = true }, "--admin-url needs a superuser or a role with CREATEROLE and CREATEDB"},
		{"admin lacks CREATE on the target database", func(r *restoreRig, _ *RestoreRequest) { r.admin.noDBCreate = true }, "needs CREATE on the target database"},
		{"admin does not inherit the app role", func(r *restoreRig, _ *RestoreRequest) { r.admin.noAppInherit = true }, "must inherit the application role"},
		{"admin reaches another cluster", func(r *restoreRig, _ *RestoreRequest) { r.admin.sysID = "9999" }, "different PostgreSQL cluster"},
		{"app url has no role", func(r *restoreRig, _ *RestoreRequest) { r.o.DatabaseURL = "postgres://db.example/site" }, "DATABASE_URL needs the app role"},
		{"app url names no database", func(r *restoreRig, _ *RestoreRequest) { r.o.DatabaseURL = "postgres://u@db.example" }, "DATABASE_URL names no database"},
		{"app role has an odd name", func(r *restoreRig, _ *RestoreRequest) { r.o.DatabaseURL = "postgres://Odd-Role@db.example/site" }, "unusual name"},
		{"admin url has a query key the tools cannot get", func(_ *restoreRig, q *RestoreRequest) { q.AdminURL = "postgres://su@h/postgres?search_path=x" }, "--admin-url has the query parameter"},
		{"app url has a query key the tools cannot get", func(r *restoreRig, _ *RestoreRequest) { r.o.DatabaseURL = "postgres://u@db.example/site?search_path=x" }, "DATABASE_URL has the query parameter"},
		{"app url is not postgres", func(r *restoreRig, _ *RestoreRequest) { r.o.DatabaseURL = "mysql://u@db.example/site" }, "DATABASE_URL is not a postgres URL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRestoreRig(t)
			req := RestoreRequest{Source: arch, AdminURL: "postgres://su:pw@h/postgres", DryRun: true}
			c.mut(r, &req)
			_, err := Restore(t.Context(), r.o, req)
			wantRefusal(t, err, c.want)
			if strings.Contains(err.Error(), "pw@") || strings.Contains(err.Error(), "secret") {
				t.Errorf("refusal leaks a password: %v", err)
			}
		})
	}
}

// Without pg_control_system for the app role, the two connections are compared
// by server address, port and postmaster start time.
func TestRestoreClusterFallbackWhenControlDataUnreadable(t *testing.T) {
	arch := buildArchive(t, nil)
	r := newRestoreRig(t)
	r.q.sysErr = errors.New("permission denied for function pg_control_system")
	if _, err := r.dry(t, arch); err != nil {
		t.Fatalf("same instance: %v", err)
	}
	r.admin.instance = "10.0.0.9:5432@t0"
	_, err := r.dry(t, arch)
	wantRefusal(t, err, "different PostgreSQL cluster")
}

func TestRestoreAdminURLPointsAtTargetDatabase(t *testing.T) {
	r := newRestoreRig(t)
	if _, err := r.dry(t, buildArchive(t, nil)); err != nil {
		t.Fatal(err)
	}
	if u, err := url.Parse(r.adminURL); err != nil || u.Path != "/site" || u.User.Username() != "su" {
		t.Errorf("admin connection URL = %q", r.adminURL)
	}
}

// A dbname in either URL's query wins in pgx; the admin connection must still
// land in the database the writes go to.
func TestRestoreAdminURLFollowsTargetDbnameQuery(t *testing.T) {
	r := newRestoreRig(t)
	r.o.DatabaseURL = "postgres://hm_app:x@127.0.0.1:1/site?dbname=scratch"
	_, err := Restore(t.Context(), r.o, RestoreRequest{Source: buildArchive(t, nil), AdminURL: "postgres://su:pw@h/postgres?dbname=other", DryRun: true})
	_ = err // the app connection fails; only the admin URL matters
	u, perr := url.Parse(r.adminURL)
	if perr != nil || u.Path != "/scratch" || u.Query().Has("dbname") {
		t.Errorf("admin connection URL = %q, want path /scratch and no dbname", r.adminURL)
	}
}

// Strings from the archive's manifest are untrusted: a newline or escape in
// them must not reach the terminal as one.
func TestRestoreQuotesManifestStrings(t *testing.T) {
	arch := buildArchive(t, func(s *archSpec) {
		s.Mf.HatamotoVersion = "v9\nrestore complete\x1b[2J"
		s.Mf.ConfigPath = "/x\nrestore complete"
	})
	r := newRestoreRig(t)
	_, err := Restore(t.Context(), r.o, RestoreRequest{Source: arch, AdminURL: "postgres://su@h/postgres", DryRun: true})
	wantRefusal(t, err, `hatamoto "v9\nrestore complete\x1b[2J"`)
	if err != nil && strings.ContainsAny(err.Error(), "\x1b") || strings.Count(err.Error(), "\n") != 1 {
		t.Errorf("raw control characters in %q", err)
	}
}

func TestRestoreDryRunReportsRoleMapping(t *testing.T) {
	cases := map[string]struct {
		url      string
		existing []string
		want     string
		warns    []string
	}{
		"mapped, one skipped": {testURL, nil, "roles ok (hm_src -> u, 1 skipped)", []string{"grants to role reporting are skipped (no such role here)"}},
		"grantee exists here": {testURL, []string{"reporting"}, "roles ok (hm_src -> u, 0 skipped)", nil},
		"same role":           {"postgres://hm_src:pw@db.example:5433/site", []string{"reporting"}, "roles ok (hm_src, 0 skipped)", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRestoreRig(t)
			r.o.DatabaseURL = c.url
			r.q.existingRoles = c.existing
			plan, err := r.dry(t, buildArchive(t, nil))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(plan.Checks, c.want) {
				t.Errorf("checks = %v, want %q", plan.Checks, c.want)
			}
			var got []string
			for _, w := range plan.Warnings {
				if strings.HasPrefix(w, "grants to role ") {
					got = append(got, w)
				}
			}
			if !slices.Equal(got, c.warns) {
				t.Errorf("skip warnings = %v, want %v", got, c.warns)
			}
		})
	}
}

func TestRestoreDryRunRefusesUnreadableDumps(t *testing.T) {
	r := newRestoreRig(t)
	r.o.Run = func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte(r.tools[name] + "\n"), nil
		}
		return []byte("pg_restore: error: not a dump\n"), errors.New("exit status 1")
	}
	_, err := r.dry(t, buildArchive(t, nil))
	wantRefusal(t, err, "cannot read the dumps for the role check")
}
