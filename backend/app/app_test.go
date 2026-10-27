package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/migrations"
	"hatamoto.dev/sdk"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args []string
		want command
		err  bool
	}{
		{nil, cmdHelp, true},
		{[]string{"serve"}, cmdServe, false},
		{[]string{"serve", "--x"}, cmdHelp, true},
		{[]string{"migrate"}, cmdMigrate, false},
		{[]string{"migrate", "up"}, cmdMigrate, false},
		{[]string{"migrate", "status"}, cmdMigrateStatus, false},
		{[]string{"migrate", "plugins"}, cmdHelp, true},
		{[]string{"migrate", "down"}, cmdHelp, true},
		{[]string{"user", "create", "--email", "a@b.c"}, cmdUserCreate, false},
		{[]string{"user", "reset-password", "--email", "a@b.c"}, cmdUserResetPassword, false},
		{[]string{"user"}, cmdHelp, true},
		{[]string{"plugins", "list"}, cmdPluginsList, false},
		{[]string{"routes"}, cmdRoutes, false},
		{[]string{"version"}, cmdVersion, false},
		{[]string{"version", "--json"}, cmdVersion, false},
		{[]string{"version", "x"}, cmdHelp, true},
		{[]string{"healthcheck"}, cmdHealthcheck, false},
		{[]string{"healthcheck", "x"}, cmdHelp, true},
		{[]string{"sanitize-html-sections"}, cmdSanitizeHTMLSections, false},
		{[]string{"sanitize-html-sections", "--dry-run"}, cmdSanitizeHTMLSections, false},
		{[]string{"sanitize-html-sections", "--bogus"}, cmdHelp, true},
		{[]string{"entity", "list"}, cmdOps, false},
		{[]string{"redirect", "create", "--from", "/a", "--to", "/b"}, cmdOps, false},
		{[]string{"analytics", "crawlers"}, cmdOps, false},
		{[]string{"export", "site.tar.gz"}, cmdOps, false},
		{[]string{"import", "site.tar.gz"}, cmdOps, false},
		{[]string{"export"}, cmdHelp, true},
		{[]string{"backup", "list"}, cmdOps, false},
		{[]string{"backup"}, cmdHelp, true},
		{[]string{"builder", "convert"}, cmdBuilder, false},
		{[]string{"builder", "convert", "--dry-run", "--verify-render"}, cmdBuilder, false},
		{[]string{"builder", "convert", "--revert", "--entity", "x"}, cmdBuilder, false},
		{[]string{"builder"}, cmdHelp, true},
		{[]string{"builder", "frob"}, cmdHelp, true},
		{[]string{"supervise"}, cmdSupervise, false},
		{[]string{"supervise", "x"}, cmdHelp, true},
		{[]string{"update"}, cmdHelp, true},
		{[]string{"update", "bogus"}, cmdHelp, true},
		{[]string{"update", "apply"}, cmdHelp, true},
		{[]string{"update", "apply", "--file", "f"}, cmdHelp, true},
		{[]string{"update", "apply", "--file", "f", "--record", "r", "extra"}, cmdHelp, true},
		{[]string{"update", "apply", "--file", "f", "--record", "r"}, cmdUpdate, false},
		{[]string{"update", "apply", "--file", "f", "--record", "r", "--sig", "s"}, cmdUpdate, false},
		{[]string{"update", "rollback"}, cmdUpdate, false},
		{[]string{"update", "rollback", "x"}, cmdHelp, true},
		{[]string{"update", "status"}, cmdUpdate, false},
		{[]string{"update", "status", "x"}, cmdHelp, true},
		{[]string{"update", "check"}, cmdUpdate, false},
		{[]string{"update", "check", "--dry-run"}, cmdUpdate, false},
		{[]string{"update", "check", "--now"}, cmdHelp, true},
		{[]string{"update", "pin", "v1.4"}, cmdUpdate, false},
		{[]string{"update", "pin", "1.4"}, cmdHelp, true},
		{[]string{"update", "pin", "v1"}, cmdHelp, true},
		{[]string{"update", "pin"}, cmdHelp, true},
		{[]string{"update", "unpin"}, cmdUpdate, false},
		{[]string{"update", "apply", "v1.4.2"}, cmdUpdate, false},
		{[]string{"update", "apply", "v1.4.2", "--force"}, cmdUpdate, false},
		{[]string{"update", "apply", "1.4.2"}, cmdHelp, true},
		{[]string{"update", "apply", "v1.4"}, cmdHelp, true},
		{[]string{"bogus"}, cmdHelp, true},
	}
	for _, c := range cases {
		got, err := parseArgs(c.args)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("parseArgs(%v) = %v, %v", c.args, got, err)
		}
	}
}

func TestPrintPlugins(t *testing.T) {
	dir := t.TempDir()
	write := func(name, manifest string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "plugin.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fake", `{"name":"fake","version":"1.2.3","sdk":"^0.5"}`)
	write("broken", `{nope`)
	var b bytes.Buffer
	printPlugins(&b, dir)
	if !strings.HasPrefix(b.String(), "broken\t(") || !strings.HasSuffix(b.String(), "fake 1.2.3 ^0.5\n") {
		t.Fatalf("got %q", b.String())
	}
}

func TestPrintRoutes(t *testing.T) {
	var buf bytes.Buffer
	if err := printRoutes(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("GET /api/v1/entities/{id}\t")) {
		t.Fatalf("route table missing entity route:\n%s", buf.String())
	}
	// Rendering is always on: the public catch-all page route exists.
	if !bytes.Contains(buf.Bytes(), []byte("GET /{rest...}\t")) {
		t.Fatalf("route table missing the rendered-site catch-all:\n%s", buf.String())
	}
}

func TestPrintVersion(t *testing.T) {
	var b bytes.Buffer
	printVersion(&b)
	if !strings.HasPrefix(b.String(), "hatamoto ") || !strings.Contains(b.String(), "commit") {
		t.Fatalf("got %q", b.String())
	}
}

func TestPrintVersionJSON(t *testing.T) {
	var b bytes.Buffer
	if err := printVersionJSON(&b); err != nil {
		t.Fatal(err)
	}
	var info versionInfo
	if err := json.Unmarshal(b.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	head, err := migrations.Head()
	if err != nil {
		t.Fatal(err)
	}
	if info.SDK != sdk.Version || info.SchemaHead != head || info.Version == "" || info.Commit == "" {
		t.Fatalf("got %+v", info)
	}
	var loose map[string]any
	if err := json.Unmarshal(b.Bytes(), &loose); err != nil || len(loose) != 5 {
		t.Fatalf("want exactly 5 fields, got %v (%v)", loose, err)
	}
	if len(info.SDKCompatibleWith) != len(sdk.CompatibleWith) {
		t.Fatalf("sdk_compatible_with = %v, want %v", info.SDKCompatibleWith, sdk.CompatibleWith)
	}
}

func TestEnsureSecrets(t *testing.T) {
	dir := t.TempDir()
	log := newLogger()
	path := filepath.Join(dir, "secrets.env")
	clear := func() {
		t.Setenv("HATAMOTO_SECRET", "")
		t.Setenv("JWT_SECRET", "")
	}

	// Env wins (under either name) and nothing is written for it.
	clear()
	t.Setenv("JWT_SECRET", "from-env")
	if err := ensureSecrets(dir, log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secrets file written although env provided the secret: %v", err)
	}
	clear()
	t.Setenv("HATAMOTO_SECRET", "from-env")
	if err := ensureSecrets(dir, log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secrets file written although env provided the secret: %v", err)
	}

	// First boot generates and persists (0600).
	clear()
	if err := ensureSecrets(dir, log); err != nil {
		t.Fatal(err)
	}
	secret := os.Getenv("HATAMOTO_SECRET")
	if len(secret) < 32 {
		t.Fatalf("generated secret too short: %q", secret)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "HATAMOTO_SECRET="+secret) {
		t.Fatalf("generated secret not persisted:\n%s", raw)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode())
	}

	// Second boot reuses the file.
	clear()
	if err := ensureSecrets(dir, log); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HATAMOTO_SECRET") != secret {
		t.Fatal("persisted secret not reused")
	}
}

// A secrets file written by an older release holds JWT_SECRET: its value must
// keep being used, or flash cookies, previews and sealed credentials break.
func TestEnsureSecretsReadsTheOldFileKey(t *testing.T) {
	dir := t.TempDir()
	old := "old-release-secret-0123456789abcdef0123456789"
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("JWT_SECRET="+old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HATAMOTO_SECRET", "")
	t.Setenv("JWT_SECRET", "")
	if err := ensureSecrets(dir, newLogger()); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HATAMOTO_SECRET"); got != old {
		t.Fatalf("secret = %q, want the stored JWT_SECRET", got)
	}
}

func TestResolvePassword(t *testing.T) {
	pw, gen, err := resolvePassword(&userFlags{}, nil)
	if err != nil || !gen || len(pw) < minPasswordLen {
		t.Fatalf("generated: %q %v %v", pw, gen, err)
	}
	pw, gen, err = resolvePassword(&userFlags{passwordStdin: true}, strings.NewReader("correct-horse-battery\n"))
	if err != nil || gen || pw != "correct-horse-battery" {
		t.Fatalf("stdin: %q %v %v", pw, gen, err)
	}
	if _, _, err := resolvePassword(&userFlags{password: "short"}, nil); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := parseUserFlags("create", []string{"--role", "owner"}, true); err == nil {
		t.Fatal("missing --email accepted")
	}
}
