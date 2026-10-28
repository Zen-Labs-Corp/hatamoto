package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
)

// pageRig is a signed registry index with one plugin, plus the key that
// signed it.
type pageRig struct {
	key   packages.Key
	index string // path of registry.json
	out   string // path the page goes to
}

func newPageRig(t *testing.T) pageRig {
	t.Helper()
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	hmp, err := packages.PackFiles(map[string][]byte{
		packages.PluginManifest: []byte(`{"name":"hello","version":"1.0.0","sdk":"^0.7","description":"Says hello","permissions":["db"]}`),
		packages.PluginWasm:     []byte("\x00asm\x01\x00\x00\x00"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pkgs := filepath.Join(dir, "packages")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "hello.hmp"), hmp, 0o644); err != nil {
		t.Fatal(err)
	}
	meta := map[string]packages.Meta{"hello": {Author: "acme", Source: "https://example.org/acme/hello", Tier: packages.TierCommunity}}
	doc, err := packages.BuildIndexWith(pkgs, "packages/", key, time.Now(), time.Hour, packages.BuildOptions{Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	r := pageRig{key: key, index: filepath.Join(dir, "registry.json"), out: filepath.Join(dir, "site", "plugins", "index.html")}
	if err := os.WriteFile(r.index, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r pageRig) run(t *testing.T, now time.Time, extra ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	args := append([]string{r.index, "--out", r.out}, extra...)
	err := registryPage(args, &buf, func() time.Time { return now })
	return buf.String(), err
}

func (r pageRig) assertNoOutput(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(r.out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the page was written despite the refusal (stat err = %v)", err)
	}
}

func TestRegistryPageWritesTheDirectory(t *testing.T) {
	r := newPageRig(t)
	msg, err := r.run(t, time.Now(), "--trusted-key", r.key.Public, "--title", "Acme plugins", "--write-url", "https://example.org/write")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "wrote 1 plugins to "+r.out) {
		t.Errorf("message = %q", msg)
	}
	b, err := os.ReadFile(r.out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hello v1.0.0", "Community", "by @acme", "Says hello", "1 permission", "Acme plugins", "https://example.org/write"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if st, _ := os.Stat(r.out); st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(r.out), ".page-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestRegistryPageRefusesBadIndexes(t *testing.T) {
	t.Run("unsigned", func(t *testing.T) {
		r := newPageRig(t)
		if err := os.WriteFile(r.index, []byte(`{"index":{"version":1},"signer":"","algorithm":"","signature":""}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := r.run(t, time.Now(), "--trusted-key", r.key.Public); !errors.Is(err, packages.ErrUnsigned) {
			t.Errorf("err = %v", err)
		}
		r.assertNoOutput(t)
	})
	t.Run("tampered", func(t *testing.T) {
		r := newPageRig(t)
		b, _ := os.ReadFile(r.index)
		if err := os.WriteFile(r.index, bytes.Replace(b, []byte("Says hello"), []byte("Says HELLO"), 1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := r.run(t, time.Now(), "--trusted-key", r.key.Public); !errors.Is(err, packages.ErrBadSignature) {
			t.Errorf("err = %v", err)
		}
		r.assertNoOutput(t)
	})
	t.Run("expired", func(t *testing.T) {
		r := newPageRig(t)
		if _, err := r.run(t, time.Now().Add(48*time.Hour), "--trusted-key", r.key.Public); !errors.Is(err, packages.ErrIndexExpired) {
			t.Errorf("err = %v", err)
		}
		r.assertNoOutput(t)
	})
	t.Run("untrusted signer", func(t *testing.T) {
		r := newPageRig(t)
		if _, err := r.run(t, time.Now()); !errors.Is(err, packages.ErrUntrustedKey) {
			t.Errorf("err = %v", err)
		}
		r.assertNoOutput(t)
	})
	t.Run("bad trusted key", func(t *testing.T) {
		r := newPageRig(t)
		if _, err := r.run(t, time.Now(), "--trusted-key", "nope"); err == nil {
			t.Error("a malformed key was accepted")
		}
		r.assertNoOutput(t)
	})
}

func TestRegistryPageUsage(t *testing.T) {
	if err := registryPage(nil, &bytes.Buffer{}, time.Now); err == nil || !strings.Contains(err.Error(), "usage: hatamoto registry page") {
		t.Errorf("err = %v", err)
	}
}
