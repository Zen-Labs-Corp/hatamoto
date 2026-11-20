package backup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func hasContaining(list []string, sub string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, sub) })
}

// noArchiveSecrets drops secrets.env from the test archive.
func noArchiveSecrets(s *archSpec) {
	delete(s.Bodies, secretsName)
	s.Parts = slices.DeleteFunc(s.Parts, func(n string) bool { return n == secretsName })
}

func TestRestoreSecretMatch(t *testing.T) {
	r := newRestoreRig(t)
	plan, err := r.dry(t, buildArchive(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Checks, "server secret ok") || hasContaining(plan.Warnings, "secret") {
		t.Fatalf("checks %v warnings %v", plan.Checks, plan.Warnings)
	}
}

func TestRestoreSecretMismatchRefused(t *testing.T) {
	for _, key := range []string{"HATAMOTO_SECRET", "JWT_SECRET"} {
		r := newRestoreRig(t)
		r.o.Getenv = envOf(map[string]string{key: "other"})
		_, err := r.dry(t, buildArchive(t, nil))
		wantRefusal(t, err, "different server secret", "--force")
	}
}

func TestRestoreSecretMismatchForced(t *testing.T) {
	r := newRestoreRig(t)
	r.o.Getenv = envOf(map[string]string{"HATAMOTO_SECRET": "other"})
	arch := buildArchive(t, func(s *archSpec) { s.Mf.ContentRows = map[string]int64{"hatamoto_commenters": 2} })
	plan, err := Restore(t.Context(), r.o, RestoreRequest{Source: arch, AdminURL: "postgres://su:pw@h/postgres", DryRun: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasContaining(plan.Warnings, "--force") || !slices.Contains(plan.Would, "drop 2 commenter emails (different server secret)") {
		t.Fatalf("warnings %v would %v", plan.Warnings, plan.Would)
	}
}

func TestRestoreSecretNoCheck(t *testing.T) {
	r := newRestoreRig(t)
	plan, err := r.dry(t, buildArchive(t, func(s *archSpec) { s.Mf.SecretCheck = "" }))
	if err != nil {
		t.Fatal(err)
	}
	if !hasContaining(plan.Warnings, "no server secret check") {
		t.Fatalf("warnings %v", plan.Warnings)
	}
}

func TestRestoreSecretFromExistingSecretsFile(t *testing.T) {
	r := newRestoreRig(t)
	if err := os.MkdirAll(r.o.SecretsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.o.SecretsDir, secretsName), []byte(specSecrets), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := r.dry(t, buildArchive(t, noArchiveSecrets))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Checks, "server secret ok") {
		t.Fatalf("checks %v", plan.Checks)
	}
}

func TestRestoreSecretUnknown(t *testing.T) {
	r := newRestoreRig(t)
	plan, err := r.dry(t, buildArchive(t, noArchiveSecrets))
	if err != nil {
		t.Fatal(err)
	}
	if !hasContaining(plan.Warnings, "not verified") {
		t.Fatalf("warnings %v", plan.Warnings)
	}
}

func TestSecretFromEnvFile(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"# HATAMOTO_SECRET=no\nHATAMOTO_SECRET=a\n", "a"},
		{"JWT_SECRET=old\n", "old"},
		{"JWT_SECRET=old\nHATAMOTO_SECRET=new\n", "new"},
		{"SECRET_LINE\n", ""},
		{"", ""},
	} {
		if got := secretFromEnvFile([]byte(c.body)); got != c.want {
			t.Errorf("%q = %q, want %q", c.body, got, c.want)
		}
	}
}
