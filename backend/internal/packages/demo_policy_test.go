package packages

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The public demo installs first-party packages only (Options.OnlySigners):
// unsigned and other signers' packages are refused, even when trusted or when
// the owner allowed unsigned packages.
func TestCheckPolicyOnlySigners(t *testing.T) {
	first, other := newKey(t), newKey(t)
	pack := func(k *Key) *Package {
		raw, err := PackFiles(themeFiles(), k)
		if err != nil {
			t.Fatal(err)
		}
		return parse(t, raw)
	}
	m := &Manager{Options: Options{Trust: trust(t, first, other), OnlySigners: []string{first.ID}}}
	for _, tc := range []struct {
		name    string
		p       *Package
		wantErr error
	}{
		{"first-party", pack(&first), nil},
		{"other trusted signer", pack(&other), ErrSignerNotAllowed},
		{"unsigned", pack(nil), ErrSignerNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, src := range []Source{SourceRegistry, SourceUpload, SourceURL, SourceDev} {
				if err := m.CheckPolicy(context.Background(), tc.p, src); !errors.Is(err, tc.wantErr) {
					t.Errorf("%s: err = %v, want %v", src, err, tc.wantErr)
				}
			}
		})
	}
	// Off by default: another trusted signer installs as before.
	open := &Manager{Options: Options{Trust: trust(t, first, other)}}
	if err := open.CheckPolicy(context.Background(), pack(&other), SourceRegistry); err != nil {
		t.Errorf("without OnlySigners: %v", err)
	}
}

// A registry fetch with OnlySigners set refuses a non-first-party entry before
// the archive is downloaded.
func TestFetchFromRegistryOnlySignersSkipsDownloadOfCommunity(t *testing.T) {
	key := newKey(t)
	dir := t.TempDir()
	for _, name := range []string{"mine", "theirs"} {
		files := themeFiles()
		files[ThemeManifest] = []byte(strings.Replace(string(files[ThemeManifest]), "sunrise", name, 1))
		raw, err := PackFiles(files, &key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+Extension), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := BuildIndexWith(dir, "p/", key, time.Now(), 24*time.Hour, BuildOptions{Meta: map[string]Meta{
		"mine":   {Tier: TierFirstParty},
		"theirs": {Tier: TierCommunity, Author: "someone"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var downloads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/registry.json" {
			_, _ = w.Write(doc)
			return
		}
		downloads = append(downloads, r.URL.Path)
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
	}))
	defer srv.Close()
	reg := &Registry{URL: srv.URL + "/registry.json", Trust: trust(t, key), Limits: DefaultLimits, AllowInsecure: true, Client: srv.Client()}
	m := &Manager{Options: Options{Registry: reg, OnlySigners: []string{key.ID}}}

	if _, err := m.FetchFromRegistry(context.Background(), KindTheme, "theirs"); !errors.Is(err, ErrSignerNotAllowed) {
		t.Fatalf("community entry: err = %v", err)
	}
	if len(downloads) != 0 {
		t.Fatalf("the community archive was downloaded before it was refused: %v", downloads)
	}
	if _, err := m.FetchFromRegistry(context.Background(), KindTheme, "mine"); err != nil {
		t.Fatalf("first-party entry: %v", err)
	}
	// Without OnlySigners nothing changes.
	open := &Manager{Options: Options{Registry: reg}}
	if _, err := open.FetchFromRegistry(context.Background(), KindTheme, "theirs"); err != nil {
		t.Fatalf("demo off: %v", err)
	}
}
