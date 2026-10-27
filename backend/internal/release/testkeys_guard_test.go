package release

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultBuildHasNoTestSeams builds the hatamoto binary twice. The default
// build must contain neither test seam; the tagged build must contain both,
// so the check cannot go vacuous.
func TestDefaultBuildHasNoTestSeams(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary twice")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	root := filepath.Join("..", "..")
	build := func(tags string) []byte {
		out := filepath.Join(t.TempDir(), "hatamoto")
		args := []string{"build", "-o", out}
		if tags != "" {
			args = append(args, "-tags", tags)
		}
		cmd := exec.Command(goBin, append(args, "./cmd/hatamoto")...)
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %q: %v\n%s", tags, err, b)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cmd := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}} {{.Dir}} {{.GoFiles}}", "./cmd/hatamoto")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	// The updater's seams must be absent from the default build always. The
	// tagged build must contain them once the updater is linked into the
	// binary (so the check cannot go vacuous); before that it cannot.
	updaterLinked := strings.Contains(string(out), "hatamoto/core/internal/updater ")
	seams := map[string]bool{
		"HATAMOTO_TEST_TRUST_DIR":       true,
		"HATAMOTO_TEST_FAULT":           true,
		"HATAMOTO_TEST_UPDATE_INTERVAL": updaterLinked,
		"HATAMOTO_TEST_CLOCK_OFFSET":    updaterLinked,
	}
	def, tagged := build(""), build("hatamoto_testkeys")
	for s, mustBeInTagged := range seams {
		if bytes.Contains(def, []byte(s)) {
			t.Errorf("default build contains %s", s)
		}
		if mustBeInTagged && !bytes.Contains(tagged, []byte(s)) {
			t.Errorf("tagged build lacks %s: the guard would prove nothing", s)
		}
	}
	if strings.Contains(string(out), "_testkeys.go") {
		t.Errorf("default build compiles a *_testkeys.go file")
	}
}
