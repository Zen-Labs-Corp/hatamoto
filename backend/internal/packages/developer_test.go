package packages

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
	"hatamoto.dev/core/internal/jsonx"
)

// devFixture serves a developer index (signed by the listing key) and a main
// index, and builds a Manager without a database.
type devFixture struct {
	t       *testing.T
	listing Key
	main    Key
	idx     Index // developer entries
	mainIdx Index
	srv     *httptest.Server
}

func newDevFixture(t *testing.T) *devFixture {
	t.Helper()
	f := &devFixture{t: t, listing: newKey(t), main: newKey(t)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var idx Index
		var key Key
		switch r.URL.Path {
		case "/developer.json":
			idx, key = f.idx, f.listing
		case "/registry.json":
			idx, key = f.mainIdx, f.main
		default:
			http.NotFound(w, r)
			return
		}
		now := time.Now().UTC()
		idx.Version, idx.IssuedAt, idx.ExpiresAt = 1, now.Add(-time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)
		body, _ := jsonx.Marshal(idx)
		doc, err := SignIndex(body, key)
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(doc)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *devFixture) manager(opts ...func(*Options)) *Manager {
	o := Options{
		Trust:             trust(f.t, f.main),
		Registry:          &Registry{URL: f.srv.URL + "/registry.json", Client: f.srv.Client(), Trust: trust(f.t, f.main), AllowInsecure: true},
		DeveloperRegistry: &Registry{URL: f.srv.URL + "/developer.json", Client: f.srv.Client(), Trust: trust(f.t, f.listing), AllowInsecure: true},
	}
	for _, fn := range opts {
		fn(&o)
	}
	return NewManager(o)
}

// theme returns a package named name signed by key, and lists it under listed.
func (f *devFixture) theme(name string, key Key, listed Key) *Package {
	f.t.Helper()
	raw, err := PackFiles(map[string][]byte{ThemeManifest: []byte(`{"name":"` + name + `","version":"1.0.0"}`)}, &key)
	if err != nil {
		f.t.Fatal(err)
	}
	p, err := Parse(raw, DefaultLimits)
	if err != nil {
		f.t.Fatal(err)
	}
	sum := p.SHA256()
	f.idx.Packages = append(f.idx.Packages, Entry{Kind: KindTheme, Name: name, Version: "1.0.0", URL: "/" + name + ".hmp", SHA256: hex.EncodeToString(sum[:]),
		Author: "Acme", SignerKey: listed.Public})
	return p
}

func TestDeveloperKeyMismatchIsRefused(t *testing.T) {
	f := newDevFixture(t)
	devKey, other := newKey(t), newKey(t)
	p := f.theme("sunrise", other, devKey) // signed by other, the index lists devKey

	_, err := f.manager().checkPolicy(context.Background(), p, SourceUpload)
	var de *DeveloperError
	if !errors.Is(err, ErrDeveloperKeyMismatch) || !errors.As(err, &de) {
		t.Fatalf("err = %v, want ErrDeveloperKeyMismatch", err)
	}
	if !strings.Contains(err.Error(), other.ID) || !strings.Contains(err.Error(), devKey.ID) {
		t.Errorf("message must name both keys: %v", err)
	}
}

func TestDeveloperPackageNotInTheIndexStaysUntrusted(t *testing.T) {
	f := newDevFixture(t)
	devKey := newKey(t)
	p := f.theme("sunrise", devKey, devKey)
	f.idx.Packages = nil // not listed

	if _, err := f.manager().checkPolicy(context.Background(), p, SourceUpload); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("err = %v, want ErrUntrustedKey", err)
	}
}

func TestDeveloperIndexOffOrOnlySignersRefusesGrant(t *testing.T) {
	f := newDevFixture(t)
	devKey := newKey(t)
	p := f.theme("sunrise", devKey, devKey)

	off := f.manager(func(o *Options) { o.DeveloperRegistry = nil })
	if _, err := off.checkPolicy(context.Background(), p, SourceUpload); !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("feature off: %v, want ErrUntrustedKey", err)
	}
	if _, err := off.DeveloperIndex(context.Background(), false); !errors.Is(err, ErrRegistryOff) {
		t.Errorf("DeveloperIndex off: %v", err)
	}

	demo := f.manager(func(o *Options) { o.OnlySigners = []string{KeyID(mustPub(t, f.main))} })
	if _, err := demo.checkPolicy(context.Background(), p, SourceUpload); !errors.Is(err, ErrSignerNotAllowed) {
		t.Errorf("OnlySigners: %v, want ErrSignerNotAllowed", err)
	}
	if _, err := demo.DeveloperIndex(context.Background(), false); !errors.Is(err, ErrRegistryOff) {
		t.Errorf("DeveloperIndex in the demo: %v", err)
	}
	if _, err := demo.FetchFromRegistry(context.Background(), KindTheme, "sunrise"); err == nil {
		t.Error("the demo must not fetch a developer package")
	}
}

func TestFirstPartyKeyNeverVouchesForADeveloperPackage(t *testing.T) {
	f := newDevFixture(t)
	devKey := newKey(t)
	p := f.theme("sunrise", devKey, devKey)
	// The index lists the first-party key for the package, but the host does
	// not trust it as a package key (TRUST_FIRST_PARTY=false).
	f.idx.Packages[0].SignerKey = firstPartyKeyForTest(t)
	f.idx.Packages[0].Name = "sunrise"
	if _, err := f.manager().checkPolicy(context.Background(), p, SourceUpload); err == nil {
		t.Fatal("a package must not become a developer package under a first-party key")
	}
}

func TestDeveloperIndexIsFilteredAgainstTheMainIndex(t *testing.T) {
	f := newDevFixture(t)
	devKey := newKey(t)
	f.theme("sunrise", devKey, devKey)
	f.theme("taken", devKey, devKey)
	f.mainIdx.Packages = []Entry{{Kind: KindTheme, Name: "taken", Version: "1.0.0", URL: "/t.hmp", SHA256: strings.Repeat("a", 64)}}

	idx, err := f.manager().DeveloperIndex(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Find(KindTheme, "taken"); ok {
		t.Error("a name in the main registry must be dropped from the developer index")
	}
	if e, ok := idx.Find(KindTheme, "sunrise"); !ok || e.Tier != TierDeveloper {
		t.Errorf("sunrise: %+v, %v", e, ok)
	}

	// The main registry failing closes the developer index (fail closed).
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/registry.json" {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	})
	if _, err := f.manager().DeveloperIndex(context.Background(), true); err == nil {
		t.Error("main index failing must make the developer index unavailable")
	}
}

func TestFetchFromRegistryFallsBackToTheDeveloperIndex(t *testing.T) {
	f := newDevFixture(t)
	devKey := newKey(t)
	p := f.theme("sunrise", devKey, devKey)
	base := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sunrise.hmp" {
			_, _ = w.Write(p.Data())
			return
		}
		base.ServeHTTP(w, r)
	})
	data, err := f.manager().FetchFromRegistry(context.Background(), KindTheme, "sunrise")
	if err != nil || len(data) == 0 {
		t.Fatalf("fetch: %d bytes, %v", len(data), err)
	}
	if _, err := f.manager().FetchFromRegistry(context.Background(), KindTheme, "nope"); err == nil || !strings.Contains(err.Error(), "no theme") {
		t.Errorf("unknown package: %v", err)
	}
}

func TestDeveloperOffMessage(t *testing.T) {
	e := &DeveloperError{Name: "sunrise", Author: "Acme", KeyID: "0123456789abcdef", Err: ErrDeveloperOff}
	want := "sunrise is a developer package (signed by Acme, key 0123456789abcdef, not reviewed by Hatamoto).\n" +
		"Developer packages are off on this site. An owner can turn them on in\n" +
		"Admin > Plugins > Package security > Allow developer packages."
	if e.Error() != want || !errors.Is(e, ErrDeveloperOff) {
		t.Errorf("message:\n%s", e.Error())
	}
	e.Author = ""
	if strings.Contains(e.Error(), "signed by") {
		t.Errorf("no author: %s", e.Error())
	}
	c := &DeveloperError{Name: "sunrise", Version: "1.3.0", KeyID: "bbbb", WantKeyID: "aaaa", Err: ErrDeveloperSignerChanged}
	if !strings.Contains(c.Error(), "bbbb") || !strings.Contains(c.Error(), "aaaa") || !errors.Is(c, ErrDeveloperSignerChanged) {
		t.Errorf("signer changed: %s", c.Error())
	}
}

func TestDeveloperRowIsVerifiedWithItsStoredKey(t *testing.T) {
	devKey := newKey(t)
	m := NewManager(Options{})
	row := Row{Kind: KindPlugin, Name: "acme", SignerKeyID: devKey.ID, Developer: true, DeveloperKey: devKey.Public}
	if err := m.usable(row); err != nil {
		t.Fatalf("developer row with its key: %v (the switch and the index must not matter)", err)
	}
	row.DeveloperKey = newKey(t).Public
	if err := m.usable(row); !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("wrong stored key: %v", err)
	}
	row.DeveloperKey = ""
	if err := m.usable(row); !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("no stored key: %v", err)
	}
	row = Row{Kind: KindPlugin, Name: "acme", SignerKeyID: devKey.ID, Developer: true, DeveloperKey: devKey.Public, DisabledReason: "revoked: key leaked"}
	if err := m.usable(row); err == nil || err.Error() != "revoked: key leaked" {
		t.Errorf("disabled row: %v", err)
	}
	// A non-developer row signed by an untrusted key is still refused.
	if err := m.usable(Row{SignerKeyID: devKey.ID}); !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("untrusted non-developer: %v", err)
	}
}

func mustPub(t *testing.T, k Key) []byte {
	t.Helper()
	pub, err := k.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func firstPartyKeyForTest(t *testing.T) string { return config.FirstPartyKey }
