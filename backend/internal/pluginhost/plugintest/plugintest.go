// Package plugintest builds the test plugins under internal/pluginhost/testdata
// for tests in any package. It is for tests only.
package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	mu    sync.Mutex
	built = map[string][]byte{}
	dir   string
)

// Build compiles internal/pluginhost/testdata/<name> to a wasm reactor (once
// per test process) and returns the module.
func Build(tb testing.TB, name string) []byte {
	tb.Helper()
	mu.Lock()
	defer mu.Unlock()
	if b, ok := built[name]; ok {
		return b
	}
	src := findSource(tb, name)
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "plugintest"); err != nil {
			tb.Fatal(err)
		}
	}
	out := filepath.Join(dir, name+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("building test plugin %s: %v\n%s", name, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		tb.Fatal(err)
	}
	built[name] = b
	return b
}

// findSource walks up from the test's working directory to the module root.
func findSource(tb testing.TB, name string) string {
	tb.Helper()
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for d := wd; ; d = filepath.Dir(d) {
		p := filepath.Join(d, "internal", "pluginhost", "testdata", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(d) == d {
			tb.Fatalf("test plugin %s not found above %s", name, wd)
		}
	}
}
