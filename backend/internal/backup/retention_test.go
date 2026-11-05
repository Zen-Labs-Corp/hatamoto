package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRetention(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, RetentionFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRetentionDefaults(t *testing.T) {
	st := ResolveRetention(t.TempDir())
	if st.Retention != (Retention{10, 5, 7}) || len(st.Locked) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestResolveRetentionOrder(t *testing.T) {
	dir := t.TempDir()
	writeRetention(t, dir, `{"manual":20,"pre_update":8}`)
	t.Setenv(EnvKeep, "30")
	t.Setenv(EnvKeepScheduled, "2")
	st := ResolveRetention(dir)
	if st.Retention != (Retention{30, 8, 2}) {
		t.Fatalf("counts = %+v", st.Retention)
	}
	if !st.Locked[KindManual] || st.Locked[KindPreUpdate] || !st.Locked[KindScheduled] {
		t.Fatalf("locked = %v", st.Locked)
	}
	if st.EnvVar[KindManual] != EnvKeep || st.EnvVar[KindScheduled] != EnvKeepScheduled {
		t.Fatalf("env vars = %v", st.EnvVar)
	}
}

func TestResolveRetentionIgnoresInvalid(t *testing.T) {
	for _, env := range []string{"0", "51", "x", "-3", "1.5"} {
		t.Setenv(EnvKeep, env)
		dir := t.TempDir()
		writeRetention(t, dir, `{"manual":12}`)
		if st := ResolveRetention(dir); st.Manual != 12 || st.Locked[KindManual] {
			t.Errorf("env %q: %+v", env, st)
		}
	}
	t.Setenv(EnvKeep, "")
	for name, body := range map[string]string{
		"zero": `{"manual":0}`, "high": `{"manual":51,"pre_update":51}`, "string": `{"manual":"9"}`,
		"fraction": `{"manual":2.5}`, "bad json": `{"manual":`, "unknown": `{"other":3}`, "missing": `{}`,
	} {
		dir := t.TempDir()
		writeRetention(t, dir, body)
		if st := ResolveRetention(dir); st.Retention != (Retention{10, 5, 7}) {
			t.Errorf("%s: %+v", name, st.Retention)
		}
	}
	dir := t.TempDir()
	writeRetention(t, dir, `{"manual":0,"pre_update":3}`)
	if st := ResolveRetention(dir); st.Retention != (Retention{10, 3, 7}) {
		t.Errorf("partial: %+v", st.Retention)
	}
}

func TestSaveRetention0600AtomicKeepsOtherKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	if err := SaveRetention(dir, map[Kind]int{KindManual: 4}); err != nil {
		t.Fatal(err)
	}
	if err := SaveRetention(dir, map[Kind]int{KindScheduled: 9}); err != nil {
		t.Fatal(err)
	}
	if st := ResolveRetention(dir); st.Retention != (Retention{4, 5, 9}) {
		t.Fatalf("counts = %+v", st.Retention)
	}
	fi, err := os.Stat(filepath.Join(dir, RetentionFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", fi, err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("leftover files: %v", ents)
	}
	for _, n := range []int{0, 51, -1} {
		if err := SaveRetention(dir, map[Kind]int{KindManual: n}); err == nil {
			t.Errorf("count %d accepted", n)
		}
	}
	if st := ResolveRetention(dir); st.Manual != 4 {
		t.Fatalf("rejected save changed the file: %+v", st)
	}
}
