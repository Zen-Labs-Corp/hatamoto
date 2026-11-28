package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBinaryLinkGuard proves the hatamoto binary links neither the release tool
// (where the signing code lives) nor ssh signing or agent code. The static
// export SFTP target is the one allowed ssh user: only internal/staticexport
// may import golang.org/x/crypto/ssh or github.com/pkg/sftp directly.
func TestBinaryLinkGuard(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	cmd := exec.Command(goBin, "list", "-deps", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./cmd/hatamoto")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/hatamoto: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 50 {
		t.Fatalf("go list returned only %d packages; the guard would prove nothing", len(lines))
	}
	for _, e := range linkGuardViolations(lines) {
		t.Error(e)
	}
}

const sshImporter = "hatamoto.dev/core/internal/staticexport"

// allowedSSH lists the only ssh packages the binary may link: the client and
// its key-decoding helper. Agent, signing and every other subpackage stay out.
var allowedSSH = map[string]bool{
	"golang.org/x/crypto/ssh":                       true,
	"golang.org/x/crypto/ssh/internal/bcrypt_pbkdf": true,
}

func isSSHOrSFTP(p string) bool {
	return strings.HasPrefix(p, "golang.org/x/crypto/ssh") || strings.HasPrefix(p, "github.com/pkg/sftp")
}

// linkGuardViolations takes `go list -deps -f '{{.ImportPath}} {{join .Imports " "}}'`
// lines and returns the sorted rule breaks.
func linkGuardViolations(lines []string) []string {
	var out []string
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		pkg, imports := f[0], f[1:]
		if strings.HasPrefix(pkg, "hatamoto.dev/core/tools/") {
			out = append(out, "the hatamoto binary links "+pkg)
		}
		if strings.HasPrefix(pkg, "golang.org/x/crypto/ssh") && !allowedSSH[pkg] {
			out = append(out, "the hatamoto binary links "+pkg)
		}
		if strings.HasPrefix(pkg, "hatamoto.dev/core/") && pkg != sshImporter {
			for _, imp := range imports {
				if isSSHOrSFTP(imp) {
					out = append(out, pkg+" imports "+imp+"; only internal/staticexport may")
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestLinkGuardViolations(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  int
	}{
		{"staticexport may import ssh and sftp", []string{
			"hatamoto.dev/core/internal/staticexport golang.org/x/crypto/ssh github.com/pkg/sftp",
			"golang.org/x/crypto/ssh",
		}, 0},
		{"another core package importing ssh", []string{
			"hatamoto.dev/core/internal/admin golang.org/x/crypto/ssh",
		}, 1},
		{"ssh agent is forbidden", []string{"golang.org/x/crypto/ssh/agent"}, 1},
		{"release tool is forbidden", []string{"hatamoto.dev/core/tools/release"}, 1},
		{"third-party sftp importing ssh is fine", []string{
			"github.com/pkg/sftp golang.org/x/crypto/ssh",
			"golang.org/x/crypto/ssh/internal/bcrypt_pbkdf",
		}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkGuardViolations(tc.lines); len(got) != tc.want {
				t.Errorf("violations = %v, want %d", got, tc.want)
			}
		})
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
