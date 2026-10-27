package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBinaryLinkGuard proves the hatamoto binary links neither the ssh
// libraries nor the release tool, which is where the signing code lives.
func TestBinaryLinkGuard(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	cmd := exec.Command(goBin, "list", "-deps", "./cmd/hatamoto")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/hatamoto: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 50 {
		t.Fatalf("go list returned only %d packages; the guard would prove nothing", len(deps))
	}
	for _, d := range deps {
		if strings.HasPrefix(d, "golang.org/x/crypto/ssh") {
			t.Errorf("the hatamoto binary links %s", d)
		}
		if strings.HasPrefix(d, "hatamoto.dev/core/tools/") {
			t.Errorf("the hatamoto binary links %s", d)
		}
	}
}

// TestNoNetworkInReleaseAndMigrations keeps net/http out of the libraries the
// binary uses for release facts: core makes no update network calls.
func TestNoNetworkInReleaseAndMigrations(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "migrations")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources in %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"net/http"`) {
				t.Errorf("%s imports net/http", f)
			}
		}
	}
}

// TestNoSSHInReleaseLibrary keeps the ssh packages out of internal/release.
func TestNoSSHInReleaseLibrary(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "golang.org/x/crypto/ssh") {
			t.Errorf("%s imports the ssh packages", f)
		}
	}
}
