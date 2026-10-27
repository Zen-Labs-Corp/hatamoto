package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
)

func TestRegistryIndexRefusesValidDaysOutsideTheRange(t *testing.T) {
	for _, days := range []string{"0", "-3", "181", "900"} {
		err := runRegistryIndex([]string{t.TempDir(), "--key", "unused", "--valid-days", days}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "--valid-days must be 1 to 180") {
			t.Errorf("--valid-days %s: %v", days, err)
		}
	}
}

func TestRegistryIndexMetaEndToEnd(t *testing.T) {
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	hmp, err := packages.PackFiles(map[string][]byte{
		packages.PluginManifest: []byte(`{"name":"hello","version":"1.0.0","sdk":"^0.7"}`),
		packages.PluginWasm:     []byte("\x00asm\x01\x00\x00\x00"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pkgs := filepath.Join(dir, "pkgs")
	if err := os.Mkdir(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "hello.hmp"), hmp, 0o644); err != nil {
		t.Fatal(err)
	}
	keyJSON, _ := json.Marshal(key)
	keyFile := filepath.Join(dir, "k.key")
	if err := os.WriteFile(keyFile, keyJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	metaFile := filepath.Join(dir, "meta.json")
	write := func(s string) {
		if err := os.WriteFile(metaFile, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	indexFile := filepath.Join(dir, "registry.json")

	write(`{"ghost":{"tier":"community"}}`)
	if err := runRegistryIndex([]string{pkgs, "--key", keyFile, "--out", indexFile, "--meta", metaFile}, io.Discard); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("a meta name outside the dir: %v", err)
	}
	write(`{"hello":{"author":"acme","source":"https://example.org/acme/hello","commit":"` + strings.Repeat("a", 40) + `","tier":"community"}}`)
	if err := runRegistryIndex([]string{pkgs, "--key", keyFile, "--out", indexFile, "--meta", metaFile}, io.Discard); err != nil {
		t.Fatal(err)
	}
	doc, _ := os.ReadFile(indexFile)
	ts, _ := packages.ParseTrustSet(key.Public)
	idx, err := packages.ParseIndex(doc, ts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := idx.Find(packages.KindPlugin, "hello"); e.Author != "acme" || e.Tier != packages.TierCommunity || e.Commit != strings.Repeat("a", 40) {
		t.Errorf("entry = %+v", e)
	}
}
