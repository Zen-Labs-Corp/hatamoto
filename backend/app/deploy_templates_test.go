package app

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// readLines returns the file's lines.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")
}

// envValue returns the value of the first `value:` line after the `- key: KEY` entry,
// quotes kept, or "" when there is none.
func envValue(lines []string, key string) string {
	for i, l := range lines {
		if strings.TrimSpace(l) != "- key: "+key {
			continue
		}
		for _, n := range lines[i+1:] {
			t := strings.TrimSpace(n)
			if strings.HasPrefix(t, "- ") {
				return ""
			}
			if v, ok := strings.CutPrefix(t, "value: "); ok {
				return v
			}
		}
	}
	return ""
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// The one-click deploy templates carry the settings the image needs to start.
// No yaml module is a dependency of the backend, so the checks are line level.
func TestDeployTemplates(t *testing.T) {
	t.Run("render.yaml", func(t *testing.T) {
		lines := readLines(t, "../../render.yaml")
		for _, want := range []string{
			"runtime: image", "healthCheckPath: /health", "mountPath: /data",
			`postgresMajorVersion: "16"`, "property: connectionString",
		} {
			if !hasLine(lines, want) {
				t.Errorf("render.yaml: missing %q", want)
			}
		}
		for key, want := range map[string]string{
			"API_PORT": `"8090"`, "PORT": `"8090"`, "ENVIRONMENT": "production", "MEDIA_UPLOAD_DIR": "/data/uploads",
		} {
			if got := envValue(lines, key); got != want {
				t.Errorf("render.yaml: %s = %q, want %q", key, got, want)
			}
		}
		if hasLine(lines, "- key: CSP_ENFORCE") {
			t.Error("render.yaml: must not set CSP_ENFORCE")
		}
	})

	t.Run("do template", func(t *testing.T) {
		lines := readLines(t, "../../.do/deploy.template.yaml")
		for _, l := range lines {
			if s := strings.TrimSpace(l); s != "" && !strings.HasPrefix(s, "#") {
				if s != "spec:" {
					t.Errorf("deploy.template.yaml: first non-comment line is %q, want spec:", s)
				}
				break
			}
		}
		for _, want := range []string{
			"registry_type: GHCR", "registry: zen-labs-corp", "repository: hatamoto", "http_port: 8090",
			"http_path: /health", "engine: PG", `version: "16"`,
		} {
			if !hasLine(lines, want) {
				t.Errorf("deploy.template.yaml: missing %q", want)
			}
		}
		for key, want := range map[string]string{
			"DATABASE_URL": "${db.DATABASE_URL}", "ENVIRONMENT": "production", "API_PORT": `"8090"`,
		} {
			if got := envValue(lines, key); got != want {
				t.Errorf("deploy.template.yaml: %s = %q, want %q", key, got, want)
			}
		}
		if hasLine(lines, "- key: CSP_ENFORCE") {
			t.Error("deploy.template.yaml: must not set CSP_ENFORCE")
		}
		secret := false
		for i, l := range lines {
			if strings.TrimSpace(l) == "- key: HATAMOTO_SECRET" {
				end := min(i+4, len(lines))
				secret = regexp.MustCompile(`(?m)^\s*type: SECRET$`).MatchString(strings.Join(lines[i+1:end], "\n"))
			}
		}
		if !secret {
			t.Error("deploy.template.yaml: HATAMOTO_SECRET must be declared type: SECRET")
		}
	})
}
