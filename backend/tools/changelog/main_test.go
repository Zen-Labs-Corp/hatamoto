package main

import (
	"bytes"
	"strings"
	"testing"
)

func runCLI(t *testing.T, e env, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, er bytes.Buffer
	code = run(args, &o, &er, e)
	return code, o.String(), er.String()
}

func credEnv(url string, git gitRunner) env {
	return env{
		Getenv: mapEnv(map[string]string{envURL: url, envKey: testKey, envSchema: "schema-1"}),
		Git:    git, CredPaths: []string{},
	}
}

func TestMissingCredsExit2WithoutSecrets(t *testing.T) {
	e := env{Getenv: mapEnv(map[string]string{envKey: testKey}), ReadFile: memFS(nil), Git: syncGit(), CredPaths: []string{}}
	code, out, errOut := runCLI(t, e, "sync", "v0.26.0", "--channel", "edge", "--dry-run")
	if code != 2 || out != "" || strings.Contains(errOut, testKey) ||
		!strings.Contains(errOut, "missing credentials") || !strings.Contains(errOut, envURL) {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestUsageErrors(t *testing.T) {
	e := credEnv("http://127.0.0.1:1", syncGit())
	for _, args := range [][]string{
		nil,
		{"frobnicate"},
		{"sync"},
		{"sync", "v0.26.0", "--yanked", "--unyank"},
		{"sync", "v0.26.0", "--channel", "beta"},                   // bad channel
		{"sync", "v0.26.0", "--channel", "edge", "--bogus"},        // unknown flag
		{"sync", "v0.26.0", "--channel", "edge", "--api-key", "x"}, // key is never a flag
		{"sync", "a", "b", "--channel", "edge"},
		{"backfill"},
		{"backfill", "--channel", "v0.25.0"},
		{"backfill", "--channel", "v0.25.0=beta"},
		{"backfill", "--channel", "v0.25.0=stable"},                          // v0.26.0 has no channel
		{"backfill", "--channel", "v0.25.0=stable,v0.26.0=edge,v9.9.9=edge"}, // unknown tag
		{"notes"},
	} {
		if code, _, errOut := runCLI(t, e, args...); code != 2 {
			t.Errorf("%v: code %d, err %q", args, code, errOut)
		}
	}
}

func TestSyncCommand(t *testing.T) {
	f, c := newFakeAPI(t)
	e := credEnv(c.Base, syncGit())
	code, out, errOut := runCLI(t, e, "sync", "v0.26.0", "--channel", "edge")
	if code != 0 || strings.TrimSpace(out) != "v0.26.0 create" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, _ = runCLI(t, e, "--channel", "edge", "sync", "v0.26.0"); code != 2 {
		t.Errorf("flag before command: %d", code)
	}
	_ = out
	code, out, _ = runCLI(t, e, "sync", "--dry-run", "v0.26.0", "--channel", "edge")
	if code != 0 || strings.TrimSpace(out) != "v0.26.0 noop (dry-run)" {
		t.Errorf("code=%d out=%q", code, out)
	}
	for _, r := range f.reqs {
		if r.Key != testKey {
			t.Errorf("key header missing on %+v", r)
		}
		if strings.Contains(r.Path, testKey) {
			t.Error("key in URL")
		}
	}
}

func TestSyncFailureExit1WithoutKey(t *testing.T) {
	f, c := newFakeAPI(t)
	f.failAt = "GET /api/v1/entities"
	code, _, errOut := runCLI(t, credEnv(c.Base, syncGit()), "sync", "v0.26.0", "--channel", "edge")
	if code != 1 || strings.Contains(errOut, testKey) {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func TestBackfill(t *testing.T) {
	f, c := newFakeAPI(t)
	e := credEnv(c.Base, syncGit())
	args := []string{"backfill", "--channel", "v0.25.0=stable,v0.26.0=edge"}
	code, out, errOut := runCLI(t, e, args...)
	want := "v0.25.0 create\nv0.26.0 create\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	for _, ent := range f.entities {
		first := ent.Attributes["version"] == "v0.25.0"
		if first != (ent.Attributes["summary"] == firstReleaseSummary) {
			t.Errorf("summary placement wrong for %v", ent.Attributes["version"])
		}
		if first && len(ent.Attributes["features"].([]any)) != 0 {
			t.Error("v0.25.0 must have no PR list")
		}
	}
	if code, out, _ = runCLI(t, e, args...); code != 0 || out != "v0.25.0 noop\nv0.26.0 noop\n" {
		t.Errorf("rerun: code=%d out=%q", code, out)
	}
	for _, ent := range f.entities {
		ent.Attributes["yanked"] = true
	}
	if code, out, _ = runCLI(t, e, args...); code != 0 || out != "v0.25.0 noop\nv0.26.0 noop\n" {
		t.Errorf("backfill touched yanked: code=%d out=%q", code, out)
	}
	n := len(f.writes())
	if code, _, _ = runCLI(t, e, append(args, "--dry-run")...); code != 0 || len(f.writes()) != n {
		t.Error("dry-run wrote")
	}
}

func TestNotesNeedsNoCredentialsOrNetwork(t *testing.T) {
	e := env{Getenv: mapEnv(nil), ReadFile: memFS(nil), Git: syncGit(), CredPaths: []string{}}
	code, out, errOut := runCLI(t, e, "notes", "v0.26.0")
	if code != 0 || out != "Features\n  - entities-subnav: sidebar (#144)\n" {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestSyncUnyankFlag(t *testing.T) {
	f, c := newFakeAPI(t)
	e := credEnv(c.Base, syncGit())
	runCLI(t, e, "sync", "v0.26.0", "--channel", "edge", "--yanked")
	if onlyEntity(f).Attributes["yanked"] != true {
		t.Fatal("--yanked did not set")
	}
	if code, out, _ := runCLI(t, e, "sync", "v0.26.0"); code != 0 || out != "v0.26.0 noop\n" {
		t.Errorf("bare sync: code=%d out=%q", code, out)
	}
	if code, out, _ := runCLI(t, e, "sync", "v0.26.0", "--unyank"); code != 0 || out != "v0.26.0 update-meta\n" || onlyEntity(f).Attributes["yanked"] != false {
		t.Errorf("--unyank: code=%d out=%q", code, out)
	}
}

func TestRefusesPlainHTTPExceptLocalhost(t *testing.T) {
	for _, u := range []string{"http://hatamoto.dev", "ftp://x.example", "hatamoto.dev", "http://127.0.0.1.evil.example"} {
		code, _, errOut := runCLI(t, credEnv(u, syncGit()), "sync", "v0.26.0", "--channel", "edge", "--dry-run")
		if code != 2 || !strings.Contains(errOut, "https") || strings.Contains(errOut, testKey) {
			t.Errorf("%s: code=%d err=%q", u, code, errOut)
		}
	}
	for _, u := range []string{"http://127.0.0.1:1", "http://localhost:1", "https://hatamoto.dev"} {
		if err := checkTransport(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}
