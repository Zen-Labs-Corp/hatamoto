package staticexport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageTree(t *testing.T, tg Target, files map[string]string) string {
	t.Helper()
	staged, err := tg.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(staged, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return staged
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func publish(t *testing.T, tg Target, files map[string]string) error {
	t.Helper()
	staged := stageTree(t, tg, files)
	return tg.Publish(context.Background(), staged, Diff{}, Manifest{Files: map[string]Entry{}})
}

func TestDirTarget_SwapKeepsPrev(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	tg, err := NewDirTarget(out, DirGuard{})
	if err != nil {
		t.Fatal(err)
	}
	if err := publish(t, tg, map[string]string{"index.html": "one"}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(out, "index.html")) != "one" {
		t.Fatal("first publish missing")
	}
	if _, err := os.Stat(out + ".prev"); !os.IsNotExist(err) {
		t.Errorf("first publish must not create .prev: %v", err)
	}
	tg, err = NewDirTarget(out, DirGuard{})
	if err != nil {
		t.Fatalf("second export over an export must pass the guard: %v", err)
	}
	if err := publish(t, tg, map[string]string{"index.html": "two"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(out, "index.html")); got != "two" {
		t.Errorf("out = %q", got)
	}
	if got := readFile(t, filepath.Join(out+".prev", "index.html")); got != "one" {
		t.Errorf("prev = %q", got)
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o755 {
		t.Errorf("published dir mode = %v", info.Mode().Perm())
	}
}

func TestDirTarget_FailureKeepsPrevious(t *testing.T) {
	parent := t.TempDir()
	out := filepath.Join(parent, "site")
	tg, _ := NewDirTarget(out, DirGuard{})
	if err := publish(t, tg, map[string]string{"index.html": "keep"}); err != nil {
		t.Fatal(err)
	}
	staged := stageTree(t, tg, map[string]string{"index.html": "half-built"})
	tg.Discard(staged)

	if got := readFile(t, filepath.Join(out, "index.html")); got != "keep" {
		t.Errorf("out = %q", got)
	}
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("staging dir left behind: %s", e.Name())
		}
	}
}

func TestDirTarget_RollbackOnSecondRename(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	tg, _ := NewDirTarget(out, DirGuard{})
	if err := publish(t, tg, map[string]string{"index.html": "old"}); err != nil {
		t.Fatal(err)
	}

	calls := 0
	orig := rename
	rename = func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("injected")
		}
		return orig(a, b)
	}
	defer func() { rename = orig }()

	if err := publish(t, tg, map[string]string{"index.html": "new"}); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, filepath.Join(out, "index.html")); got != "old" {
		t.Errorf("out after rollback = %q", got)
	}
}

func TestDirTarget_GuardRefuses(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	media := filepath.Join(data, "media")
	g := DirGuard{Forbidden: []string{data, media}}
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "foreign")
	if err := os.MkdirAll(filepath.Join(foreign, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for name, out := range map[string]string{
		"relative":       "site",
		"root":           "/",
		"media dir":      media,
		"inside data":    filepath.Join(data, "static"),
		"parent of data": root,
		"foreign dir":    foreign,
		"a file":         file,
	} {
		if _, err := NewDirTarget(out, g); err == nil {
			t.Errorf("%s: %q must be refused", name, out)
		}
	}
	if _, err := NewDirTarget(empty, g); err != nil {
		t.Errorf("empty existing dir: %v", err)
	}
	if _, err := NewDirTarget(filepath.Join(root, "new", "site"), g); err != nil {
		t.Errorf("new dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "sub")); err != nil {
		t.Errorf("foreign dir touched: %v", err)
	}
}

func TestDirTarget_PreviousManifest(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	tg, _ := NewDirTarget(out, DirGuard{})
	if _, ok, err := tg.Previous(context.Background()); ok || err != nil {
		t.Fatalf("before first publish: ok=%v err=%v", ok, err)
	}
	next := Manifest{Files: map[string]Entry{"index.html": {SHA256: "aa", Size: 3, Type: "text/html"}}}
	staged := stageTree(t, tg, map[string]string{"index.html": "abc"})
	if err := tg.Publish(context.Background(), staged, Diff{}, next); err != nil {
		t.Fatal(err)
	}
	got, ok, err := tg.Previous(context.Background())
	if err != nil || !ok || got.Files["index.html"] != next.Files["index.html"] {
		t.Fatalf("Previous = %+v, %v, %v", got, ok, err)
	}
}
