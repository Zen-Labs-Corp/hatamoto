package editionstate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"testing"
)

type fakeGate struct {
	features, plugins map[string]bool
	err               error
}

func (g fakeGate) FeatureEnabled(_ context.Context, f string) (bool, error) {
	return g.features[f], g.err
}

func (g fakeGate) PluginEntitled(_ context.Context, p string) (bool, error) {
	return g.plugins[p], g.err
}

func fresh(t *testing.T) {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	fn()
}

func TestDefaults(t *testing.T) {
	fresh(t)
	ctx := context.Background()
	if Name() != Community {
		t.Errorf("Name = %q", Name())
	}
	if Feature(ctx, "x") || Plugin(ctx, "x") {
		t.Error("defaults must be false")
	}
	if _, err := SiteID(ctx); !errors.Is(err, ErrNoSiteID) {
		t.Errorf("SiteID err = %v", err)
	}
}

func TestRegisterAndLookups(t *testing.T) {
	fresh(t)
	ctx := context.Background()
	Register("example", fakeGate{features: map[string]bool{"a": true}, plugins: map[string]bool{"p": true}})
	if Name() != "example" {
		t.Errorf("Name = %q", Name())
	}
	if !Feature(ctx, "a") || Feature(ctx, "b") || !Plugin(ctx, "p") || Plugin(ctx, "q") {
		t.Error("lookups do not reflect the gate")
	}
}

func TestGateErrorFailsClosed(t *testing.T) {
	fresh(t)
	ctx := context.Background()
	Register("example", fakeGate{
		features: map[string]bool{"a": true}, plugins: map[string]bool{"p": true},
		err: errors.New("boom"),
	})
	if Feature(ctx, "a") || Plugin(ctx, "p") {
		t.Error("gate error must yield false")
	}
}

func TestRegisterPanics(t *testing.T) {
	g := fakeGate{}
	cases := []struct {
		name string
		fn   func()
	}{
		{"after freeze", func() { Freeze(); Register("example", g) }},
		{"twice", func() { Register("example", g); Register("other", g) }},
		{"nil gate", func() { Register("example", nil) }},
		{"typed nil gate", func() { Register("example", (*fakeGate)(nil)) }},
		{"community", func() { Register(Community, g) }},
		{"empty", func() { Register("", g) }},
		{"upper", func() { Register("Upper", g) }},
		{"digit first", func() { Register("1abc", g) }},
		{"too long", func() { Register("a"+strings.Repeat("b", 32), g) }},
		{"space", func() { Register("a b", g) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fresh(t)
			mustPanic(t, c.fn)
		})
	}
}

func TestSiteIDSource(t *testing.T) {
	fresh(t)
	SetSiteIDSource(func(context.Context) (string, error) { return "abc", nil })
	got, err := SiteID(context.Background())
	if err != nil || got != "abc" {
		t.Errorf("SiteID = %q, %v", got, err)
	}
	mustPanic(t, func() { SetSiteIDSource(nil) })
}

func TestRegisterPluginKeyPanics(t *testing.T) {
	good := make(ed25519.PublicKey, ed25519.PublicKeySize)
	cases := []struct {
		name string
		fn   func()
	}{
		{"after freeze", func() { Freeze(); RegisterPluginKey(good) }},
		{"twice", func() { RegisterPluginKey(good); RegisterPluginKey(good) }},
		{"nil", func() { RegisterPluginKey(nil) }},
		{"short", func() { RegisterPluginKey(good[:10]) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fresh(t)
			mustPanic(t, c.fn)
		})
	}
}

func TestPluginKeyCopyAndReset(t *testing.T) {
	fresh(t)
	if _, ok := PluginKey(); ok {
		t.Fatal("key set before registration")
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(pub)
	RegisterPluginKey(pub)
	pub[0] ^= 0xff // caller mutation must not leak in
	got, ok := PluginKey()
	if !ok || !got.Equal(want) {
		t.Fatalf("PluginKey = %x, %v", got, ok)
	}
	got[0] ^= 0xff // returned copy mutation must not leak in
	if again, _ := PluginKey(); !again.Equal(want) {
		t.Error("PluginKey returned shared storage")
	}
	Reset()
	if _, ok := PluginKey(); ok {
		t.Error("Reset did not clear the key")
	}
}
