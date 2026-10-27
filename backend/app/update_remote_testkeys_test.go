//go:build hatamoto_testkeys

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// TestUpdateApplyNamedVersionFetchesVerifiesAndStages serves a signed channel
// from a loopback server (allowed in test builds only) and installs a major
// by name: the explicit route.
func TestUpdateApplyNamedVersionFetchesVerifiesAndStages(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	if !slices.Contains(release.Platforms, platform) {
		t.Skipf("no release platform %s", platform)
	}
	relKey, _ := packages.GenerateKey()
	manKey, _ := packages.GenerateKey()
	rp, _ := relKey.PublicKey()
	mp, _ := manKey.PublicKey()
	rts, mts := packages.TrustSet{}, packages.TrustSet{}
	rts.Add(rp)
	mts.Add(mp)

	body := []byte("pretend binary v2.0.0")
	sum := sha256.Sum256(body)
	rec, err := release.MarshalRecord(release.Record{Version: "v2.0.0", Kind: "major", SDK: "0.10.0", RollbackFloor: "v1.0.0", MinFrom: "v1.0.0",
		Artifacts: map[string]release.Artifact{platform: {SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}}})
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := packages.SignBytes(relKey, rec)
	now := time.Now().UTC()
	manifest, _ := json.Marshal(release.ChannelManifest{Channel: "stable", IssuedAt: now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), Releases: []release.Entry{{Version: "v2.0.0"}}, Yanked: []release.Yank{}})
	doc, err := packages.SignIndex(manifest, manKey)
	if err != nil {
		t.Fatal(err)
	}
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		switch r.URL.Path {
		case "/releases/channels/stable.json":
			w.Write(doc)
		case "/releases/v2.0.0/release.json":
			w.Write(rec)
		case "/releases/v2.0.0/release.json.sig":
			w.Write([]byte(sig.Signer + ":" + sig.Signature + "\n"))
		case "/releases/v2.0.0/hatamoto-" + platform:
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("HATAMOTO_UPDATE_URL", srv.URL+"/releases")
	t.Setenv("DATABASE_URL", "") // no plugin preflight, no stored status
	var out bytes.Buffer
	if err := runUpdateWith([]string{"apply", "v2.0.0"}, &out, release.Options{Release: rts, Manifest: mts}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "v2.0.0") {
		t.Fatalf("output: %q", out.String())
	}
	if got, err := os.ReadFile(filepath.Join(data, "releases", "v2.0.0", "hatamoto")); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("staged binary: %q %v", got, err)
	}
	if n := gets.Load(); n != 4 { // manifest, record, signature, artifact
		t.Fatalf("%d requests", n)
	}
	if left, _ := filepath.Glob(filepath.Join(data, "releases", ".tmp-*")); len(left) != 0 {
		t.Fatalf("temp dirs left: %v", left)
	}
	// The switch just made is unproven, so a second apply is refused up front.
	gets.Store(0)
	if err := runUpdateWith([]string{"apply", "v2.0.0"}, &out, release.Options{Release: rts, Manifest: mts}); err == nil || gets.Load() != 0 {
		t.Fatalf("second apply: err=%v gets=%d", err, gets.Load())
	}
	// A version the channel does not list is refused before any download.
	t.Setenv("HATAMOTO_DATA", t.TempDir())
	if err := runUpdateWith([]string{"apply", "v2.5.0"}, &out, release.Options{Release: rts, Manifest: mts}); err == nil || gets.Load() != 1 {
		t.Fatalf("unlisted version: err=%v gets=%d", err, gets.Load())
	}
}
