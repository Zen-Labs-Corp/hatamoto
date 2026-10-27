package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// keyPair is a throwaway key written under dir; nothing outside t.TempDir().
type keyPair struct {
	key  packages.Key
	file string
	set  packages.TrustSet
}

func newKeyPair(t *testing.T, dir, name string) keyPair {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := k.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	set := packages.TrustSet{}
	set.Add(pub)
	file := filepath.Join(dir, name+".key")
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPair{key: k, file: file, set: set}
}

// writeDist writes four fake platform files and returns the facts file.
func writeDist(t *testing.T, dir, version string) string {
	t.Helper()
	for i, p := range release.Platforms {
		body := bytes.Repeat([]byte{byte('a' + i)}, 100+i)
		if err := os.WriteFile(filepath.Join(dir, "hatamoto-"+p), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	facts := filepath.Join(dir, "facts.json")
	js := `{"version":"` + version + `","commit":"abc123","sdk":"0.10.0","schema_head":202610201100}`
	if err := os.WriteFile(facts, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	return facts
}

// runOK runs the tool in-process and fails on error.
func runOK(t *testing.T, e env, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if err := run(args, &out, &errb, e); err != nil {
		t.Fatalf("release %v: %v\n%s%s", args, err, out.String(), errb.String())
	}
	return out.String()
}

func runErr(t *testing.T, e env, args ...string) error {
	t.Helper()
	var out, errb bytes.Buffer
	err := run(args, &out, &errb, e)
	if err == nil {
		t.Fatalf("release %v: expected an error\n%s", args, out.String())
	}
	return err
}

// buildRecordFile builds release.json in dir for version v1.2.3.
func buildRecordFile(t *testing.T, dir string) string {
	t.Helper()
	facts := writeDist(t, dir, "v1.2.3")
	out := filepath.Join(dir, "release.json")
	runOK(t, env{}, "record", "--version", "v1.2.3", "--kind", "patch", "--facts", facts,
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "https://example.test/n",
		"--dir", dir, "--out", out)
	return out
}
