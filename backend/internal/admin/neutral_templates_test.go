package admin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckNeutralBansEvalAttributes is the failing-fixture test for the
// admin-template rule in scripts/check-neutral.sh: hx-on, js: values and
// hx-trigger filters need eval, which the CSP forbids.
func TestCheckNeutralBansEvalAttributes(t *testing.T) {
	t.Parallel()

	script, err := filepath.Abs("../../../scripts/check-neutral.sh")
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string) (string, error) {
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "HM_ADMIN_TEMPLATES="+dir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	bad := map[string]string{
		"hx-on.html":      `<button hx-on:click="x()">go</button>`,
		"hx-on-dash.html": `<button hx-on-click="x()">go</button>`,
		"vals.html":       `<div hx-vals='js:{a: 1}'></div>`,
		"headers.html":    `<div hx-headers="js:{a: 1}"></div>`,
		"trigger.html":    `<div hx-get="/x" hx-trigger="click[ctrlKey]"></div>`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := run(dir)
			if err == nil {
				t.Fatalf("check-neutral passed on %s:\n%s", body, out)
			}
			if !strings.Contains(out, name) {
				t.Errorf("output does not name %s:\n%s", name, out)
			}
		})
	}

	t.Run("clean", func(t *testing.T) {
		dir := t.TempDir()
		ok := `<div hx-get="/x" hx-trigger="input changed delay:300ms from:#q, change" hx-vals='{"a":"b"}'></div>`
		if err := os.WriteFile(filepath.Join(dir, "ok.html"), []byte(ok), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := run(dir); err != nil {
			t.Fatalf("clean templates rejected: %v\n%s", err, out)
		}
	})
}
