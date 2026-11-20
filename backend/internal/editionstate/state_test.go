package editionstate

import (
	"context"
	"errors"
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
