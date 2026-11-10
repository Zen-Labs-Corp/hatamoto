//go:build integration

package app

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
)

// A main-registry theme installs by name even when the developer index, which
// the plugin lookup consults after the main registry misses, cannot be read.
func TestFetchByNameFindsAThemeWhenTheDeveloperIndexIsDown(t *testing.T) {
	listing, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := packages.PackFiles(map[string][]byte{
		packages.ThemeManifest: []byte(`{"name":"sunrise","version":"1.0.0"}`),
		"partials/footer.html": []byte(`{{define "footer"}}<footer>sunrise</footer>{{end}}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := packages.Parse(raw, packages.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	sum := p.SHA256()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registry.json":
			now := time.Now().UTC()
			idx, _ := jsonx.Marshal(packages.Index{Version: 1, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
				Packages: []packages.Entry{{Kind: packages.KindTheme, Name: "sunrise", Version: "1.0.0", URL: "/sunrise.hmp", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw)), Tier: packages.TierFirstParty}}})
			doc, err := packages.SignIndex(idx, listing)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
		case "/sunrise.hmp":
			_, _ = w.Write(raw)
		default:
			http.Error(w, "down", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	pub, _ := listing.PublicKey()
	trust := packages.TrustSet{packages.KeyID(pub): pub}
	m := packages.NewManager(packages.Options{
		Logger:            quietLogger(),
		Registry:          &packages.Registry{URL: srv.URL + "/registry.json", Client: srv.Client(), AllowInsecure: true, Trust: trust, Limits: packages.DefaultLimits},
		DeveloperRegistry: &packages.Registry{URL: srv.URL + "/developer.json", Client: srv.Client(), AllowInsecure: true, Trust: trust, Limits: packages.DefaultLimits},
	})
	if _, err := fetchByName(context.Background(), m, "sunrise"); err != nil {
		t.Fatalf("theme by name with the developer index down: %v", err)
	}
	if _, err := fetchByName(context.Background(), m, "ghost"); err == nil {
		t.Fatal("an unlisted name with the developer index down must fail")
	}
}
