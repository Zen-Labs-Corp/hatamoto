package backup

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestResolveRetentionClampsHighEnv(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	warnedEnv.Clear()
	t.Setenv(EnvKeep, "100")
	dir := t.TempDir()
	writeRetention(t, dir, `{"manual":12}`)
	for i := 0; i < 3; i++ {
		st := ResolveRetention(dir)
		if st.Manual != MaxKeep || !st.Locked[KindManual] || st.EnvVar[KindManual] != EnvKeep {
			t.Fatalf("state = %+v", st)
		}
	}
	if n := strings.Count(logs.String(), EnvKeep); n != 1 {
		t.Errorf("warned %d times, want once:\n%s", n, logs.String())
	}
	t.Setenv(EnvKeepPreUpdate, "0")
	if st := ResolveRetention(dir); st.Locked[KindPreUpdate] || !strings.Contains(logs.String(), EnvKeepPreUpdate) {
		t.Errorf("a zero env value must be ignored with a warning: %+v\n%s", st, logs.String())
	}
}

func TestResolveRetentionIgnoresInvalid(t *testing.T) {
	for _, env := range []string{"0", "x", "-3", "1.5"} {
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

func TestEnginePruneReturnsStoreError(t *testing.T) {
	e := &Engine{storeErr: errors.New("no dir")}
	if _, err := e.Prune(Retention{1, 1, 1}); err == nil {
		t.Fatal("a store error was swallowed")
	}
}

func TestEnginePruneSkipsWhileBackupRuns(t *testing.T) {
	r := newRig(t)
	r.e.opts.Retention = func() Retention { return Retention{50, 50, 50} }
	for range 3 {
		if _, err := r.e.Create(t.Context(), Request{Kind: KindManual}); err != nil {
			t.Fatal(err)
		}
	}
	release, err := TryLock(filepath.Join(r.data, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := r.e.Prune(Retention{1, 1, 1})
	release()
	if !errors.Is(err, ErrPruneDeferred) || deleted[KindManual] != 0 {
		t.Fatalf("pruned under a held lock: %v %v", deleted, err)
	}
	if list, _ := r.e.List(); len(list) != 3 {
		t.Fatalf("%d backups left, want 3", len(list))
	}
	if deleted, err := r.e.Prune(Retention{1, 1, 1}); err != nil || deleted[KindManual] != 2 {
		t.Fatalf("prune = %v %v", deleted, err)
	}
}

func TestSaveRetentionConcurrentKeepsEveryKind(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i, k := range []Kind{KindManual, KindPreUpdate, KindScheduled} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := SaveRetention(dir, map[Kind]int{k: 20 + i}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if st := ResolveRetention(dir); st.Retention != (Retention{20, 21, 22}) {
		t.Fatalf("lost a save: %+v", st.Retention)
	}
}

func TestFinishingBackupPrunesEveryKind(t *testing.T) {
	r := newRig(t)
	r.e.opts.Retention = func() Retention { return Retention{50, 50, 50} }
	for range 3 {
		if _, err := r.e.Create(t.Context(), Request{Kind: KindManual}); err != nil {
			t.Fatal(err)
		}
	}
	r.e.opts.Retention = func() Retention { return Retention{1, 50, 50} }
	if _, err := r.e.Create(t.Context(), Request{Kind: KindPreUpdate}); err != nil {
		t.Fatal(err)
	}
	manual := 0
	list, _ := r.e.List()
	for _, in := range list {
		if in.Kind == KindManual {
			manual++
		}
	}
	if manual != 1 {
		t.Fatalf("%d manual backups left after a pre-update backup, want 1", manual)
	}
}
