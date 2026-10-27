package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/packages"
)

func TestParseArgsRoutesPackageCommands(t *testing.T) {
	for _, args := range [][]string{{"keys", "generate"}, {"theme", "pack", "d"}, {"theme", "check", "d"}, {"plugin", "pack", "d"}, {"theme", "dev", "d"}, {"package", "install", "f"}, {"registry", "page", "f"}, {"registry", "index", "d"}} {
		if cmd, err := parseArgs(args); err != nil || cmd != cmdPackages {
			t.Errorf("%v: %v %v", args, cmd, err)
		}
	}
	if _, err := parseArgs([]string{"theme"}); err == nil {
		t.Error("`theme` alone is not a command")
	}
}

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestKeysGenerateThenPackSignedTheme(t *testing.T) {
	keys := t.TempDir()
	var out bytes.Buffer
	if err := runKeysGenerate([]string{"--dir", keys, "--name", "me"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "HATAMOTO_TRUSTED_KEYS=") {
		t.Errorf("output must say how to trust the key:\n%s", out.String())
	}
	if fi, err := os.Stat(filepath.Join(keys, "me.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret key mode: %v %v", fi, err)
	}
	if err := runKeysGenerate([]string{"--dir", keys, "--name", "me"}, &out); err == nil {
		t.Error("an existing key must not be overwritten")
	}
	if pub, _ := os.ReadFile(filepath.Join(keys, "me.pub")); strings.Contains(string(pub), "private") {
		t.Error("the public key file must not hold the secret")
	}

	theme := writeDir(t, map[string]string{
		"theme.json": `{"name":"sunrise","version":"1.0.0"}`, "layouts/base.html": "x", ".git/config": "secret", "node_modules/a/b.js": "x", "notes.txt": "x",
	})
	outFile := filepath.Join(t.TempDir(), "sunrise.hmp")
	out.Reset()
	if err := runPack(packages.KindTheme, []string{theme, "--key", filepath.Join(keys, "me.key"), "--out", outFile}, &out); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(outFile)
	p, err := packages.Parse(raw, packages.DefaultLimits)
	if err != nil || p.Signature == nil {
		t.Fatalf("parse: %v %+v", err, p)
	}
	pubJSON, _ := os.ReadFile(filepath.Join(keys, "me.pub"))
	var k packages.Key
	if err := json.Unmarshal(pubJSON, &k); err != nil {
		t.Fatal(err)
	}
	pub, _ := k.PublicKey()
	ts := packages.TrustSet{}
	ts.Add(pub)
	if err := ts.Verify(p); err != nil {
		t.Fatalf("the packed theme must verify against the public key: %v", err)
	}
	for _, leaked := range []string{".git/config", "node_modules/a/b.js", "notes.txt"} {
		if p.Has(leaked) {
			t.Errorf("%s was packed", leaked)
		}
	}
}

func TestPackRefusesTheWrongKindAndWarnsWhenUnsigned(t *testing.T) {
	plugin := writeDir(t, map[string]string{"plugin.json": `{"name":"hello","version":"1.0.0","sdk":"^0.7"}`, "plugin.wasm": "\x00asm"})
	var out bytes.Buffer
	if err := runPack(packages.KindTheme, []string{plugin, "--out", filepath.Join(t.TempDir(), "x.hmp")}, &out); err == nil || !strings.Contains(err.Error(), "plugin pack") {
		t.Errorf("a plugin dir through `theme pack`: %v", err)
	}
	out.Reset()
	if err := runPack(packages.KindPlugin, []string{plugin, "--out", filepath.Join(t.TempDir(), "hello.hmp")}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "UNSIGNED") {
		t.Errorf("an unsigned pack must say so:\n%s", out.String())
	}
}

func TestDirFingerprintChangesWithContent(t *testing.T) {
	dir := writeDir(t, map[string]string{"a.txt": "1"})
	before := dirFingerprint(dir)
	if before != dirFingerprint(dir) {
		t.Fatal("fingerprint must be stable")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("22"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirFingerprint(dir) == before {
		t.Error("a changed file must change the fingerprint")
	}
}
