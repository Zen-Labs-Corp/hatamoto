package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/packages"
)

func TestKeygenWritesPair(t *testing.T) {
	dir := t.TempDir()
	runOK(t, env{}, "keygen", "--kind", "release", "--out", dir)

	for name, want := range map[string]os.FileMode{"release.key": 0o600, "release.pub": 0o644} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", name, fi.Mode().Perm(), want)
		}
	}
	var pub packages.Key
	b, _ := os.ReadFile(filepath.Join(dir, "release.pub"))
	if err := json.Unmarshal(b, &pub); err != nil {
		t.Fatal(err)
	}
	pk, err := pub.PublicKey()
	if err != nil || pub.ID != packages.KeyID(pk) || pub.Private != "" {
		t.Fatalf("bad public file: %+v %v", pub, err)
	}
	if k, err := loadKeyFile(filepath.Join(dir, "release.key")); err != nil || k.ID != pub.ID {
		t.Fatalf("private file does not match: %v", err)
	}

	runErr(t, env{}, "keygen", "--kind", "release", "--out", dir) // never overwrites
	if err := runErr(t, env{}, "keygen", "--kind", "other", "--out", dir); err == nil {
		t.Fatal("invalid kind accepted")
	}
	runOK(t, env{}, "keygen", "--kind", "manifest", "--out", dir)
}
