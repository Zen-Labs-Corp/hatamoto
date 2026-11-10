//go:build integration

package app

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

// A package named on the command line is read from the developer index when
// the main registry lacks it, refused with the client-view message while the
// owner's switch is off, installed and described once it is on, and an update
// signed by another key is refused whatever --confirm says.
func TestInstallByNameFromTheDeveloperIndex(t *testing.T) {
	dsn := scratchDSN(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}

	listing, dev, other := mustKey(t), mustKey(t), mustKey(t)
	served := map[string][]byte{}
	var entries []packages.Entry
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/developer.json" {
			now := time.Now().UTC()
			idx, _ := jsonx.Marshal(packages.Index{Version: 1, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), Packages: entries})
			doc, err := packages.SignIndex(idx, listing)
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(doc)
			return
		}
		if b, ok := served[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	list := func(version string, signedBy, listedKey packages.Key) {
		t.Helper()
		raw, err := packages.PackFiles(map[string][]byte{
			packages.ThemeManifest: []byte(`{"name":"sunrise","version":"` + version + `"}`),
			"partials/footer.html": []byte(`{{define "footer"}}<footer>sunrise</footer>{{end}}`),
		}, &signedBy)
		if err != nil {
			t.Fatal(err)
		}
		p, err := packages.Parse(raw, packages.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		sum := p.SHA256()
		served["/sunrise.hmp"] = raw
		entries = []packages.Entry{{Kind: packages.KindTheme, Name: "sunrise", Version: version, URL: "/sunrise.hmp", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw)), Author: "Acme", SignerKey: listedKey.Public}}
	}

	listingPub, _ := listing.PublicKey()
	eng, err := render.New(render.Options{Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	m := packages.NewManager(packages.Options{
		Pool: db.Pool(), Trust: packages.TrustSet{}, Engine: eng, Logger: quietLogger(),
		DeveloperRegistry: &packages.Registry{URL: srv.URL + "/developer.json", Client: srv.Client(), AllowInsecure: true,
			Trust: packages.TrustSet{packages.KeyID(listingPub): listingPub}, Limits: packages.DefaultLimits},
	})
	ctx = packages.WithActor(ctx, packages.Actor{Email: "cli"})

	list("1.0.0", dev, dev)
	// Switch off (the default): the client-view message, nothing installed.
	data, err := fetchByName(ctx, m, "sunrise")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Install(ctx, data, packages.InstallRequest{Source: packages.SourceRegistry})
	if !errors.Is(err, packages.ErrDeveloperOff) || !strings.Contains(err.Error(), "Allow developer packages") {
		t.Fatalf("switch off: %v", err)
	}
	// A name nobody lists.
	if _, err := fetchByName(ctx, m, "ghost"); err == nil || !strings.Contains(err.Error(), `no plugin or theme "ghost"`) {
		t.Fatalf("unknown name: %v", err)
	}

	if err := m.SetAllowDeveloper(ctx, true); err != nil {
		t.Fatal(err)
	}
	p, err := packages.ParseNew(data, packages.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, data, packages.InstallRequest{Source: packages.SourceRegistry}); err != nil {
		t.Fatal(err)
	}
	if got, want := developerNote(ctx, m, p), "developer: Acme, key "+dev.ID; got != want {
		t.Fatalf("note %q, want %q", got, want)
	}

	// The next version is signed (and listed) under another key: refused, and
	// --confirm does not change that.
	list("2.0.0", other, other)
	if _, err := m.DeveloperIndex(ctx, true); err != nil { // skip the 30 s cache
		t.Fatal(err)
	}
	data, err = fetchByName(ctx, m, "sunrise")
	if err != nil {
		t.Fatal(err)
	}
	for _, confirm := range []bool{false, true} {
		_, err := m.Install(ctx, data, packages.InstallRequest{Source: packages.SourceRegistry, Confirm: confirm})
		if !errors.Is(err, packages.ErrDeveloperSignerChanged) {
			t.Fatalf("signer change (confirm=%v): %v", confirm, err)
		}
	}
}

func mustKey(t *testing.T) packages.Key {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}
