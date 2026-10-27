package release

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedTrust(t *testing.T) {
	rel, man, err := LoadTrust()
	if err != nil {
		t.Fatal(err)
	}
	if len(rel) < 2 {
		t.Errorf("release set has %d keys, want at least 2 (primary and backup)", len(rel))
	}
	if len(man) < 1 {
		t.Errorf("manifest set has %d keys, want at least 1", len(man))
	}
	for id := range rel {
		if _, dup := man[id]; dup {
			t.Errorf("key %s is in both sets", id)
		}
	}
	files, err := keyFS.ReadDir("keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(rel)+len(man) {
		t.Errorf("%d key files but %d trusted keys", len(files), len(rel)+len(man))
	}
	for _, f := range files {
		b, _ := keyFS.ReadFile("keys/" + f.Name())
		if strings.Contains(string(b), `"private"`) {
			t.Errorf("%s carries a private field", f.Name())
		}
		if _, err := parseKey(b); err != nil {
			t.Errorf("%s: %v", f.Name(), err)
		}
	}
}

func TestEmbeddedTrustReturnsCopies(t *testing.T) {
	a := ReleaseTrust()
	for id := range a {
		delete(a, id)
	}
	if len(ReleaseTrust()) < 2 {
		t.Fatal("mutating a returned set changed the embedded one")
	}
}

func TestMarshalRecordDeterministic(t *testing.T) {
	rec := Record{Version: "v1.0.0", Artifacts: map[string]Artifact{
		"linux-arm64": {SHA256: "b", Size: 2}, "darwin-amd64": {SHA256: "a", Size: 1},
	}}
	first, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _ := MarshalRecord(rec)
		if !bytes.Equal(first, again) {
			t.Fatal("record bytes differ between runs")
		}
	}
	if first[len(first)-1] != '\n' || bytes.Index(first, []byte("darwin-amd64")) > bytes.Index(first, []byte("linux-arm64")) {
		t.Fatalf("want trailing newline and sorted keys:\n%s", first)
	}
}

func TestLoadTrustManyManifestKeys(t *testing.T) {
	mk := func(t *testing.T) []byte {
		k, _ := genKey(t)
		k.Private = ""
		b, err := json.Marshal(k)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	fsys := fstest.MapFS{
		"keys/manifest.pub":        {Data: mk(t)},
		"keys/manifest-2027.pub":   {Data: mk(t)},
		"keys/release-primary.pub": {Data: mk(t)},
		"keys/release-backup.pub":  {Data: mk(t)},
	}
	rel, man, err := loadTrust(fsys, "keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(rel) != 2 || len(man) != 2 {
		t.Fatalf("release %d, manifest %d keys; want 2 and 2", len(rel), len(man))
	}
	fsys["keys/other.pub"] = &fstest.MapFile{Data: mk(t)}
	if _, _, err := loadTrust(fsys, "keys"); err == nil {
		t.Fatal("an unexpected key file name must be refused")
	}
}
