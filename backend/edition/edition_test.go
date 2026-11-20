package edition_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/edition"
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

var _ edition.Gate = fakeGate{}

func reset(t *testing.T) {
	t.Helper()
	edition.ResetForTest()
	t.Cleanup(edition.ResetForTest)
}

func TestCommunityDefault(t *testing.T) {
	reset(t)
	ctx := context.Background()
	if edition.Name() != "community" || edition.Name() != edition.Community {
		t.Errorf("Name = %q", edition.Name())
	}
	for _, n := range []string{"", "reports", "forms", "anything"} {
		if edition.FeatureEnabled(ctx, n) || edition.PluginEntitled(ctx, n) {
			t.Errorf("%q enabled in community", n)
		}
	}
	if _, err := edition.SiteID(ctx); !errors.Is(err, edition.ErrNoSiteID) {
		t.Errorf("SiteID err = %v", err)
	}
}

func TestRegister(t *testing.T) {
	reset(t)
	ctx := context.Background()
	edition.Register("example", fakeGate{
		features: map[string]bool{"reports": true},
		plugins:  map[string]bool{"forms": true},
	})
	if edition.Name() != "example" {
		t.Errorf("Name = %q", edition.Name())
	}
	if !edition.FeatureEnabled(ctx, "reports") || edition.FeatureEnabled(ctx, "other") {
		t.Error("feature lookup wrong")
	}
	if !edition.PluginEntitled(ctx, "forms") || edition.PluginEntitled(ctx, "other") {
		t.Error("plugin lookup wrong")
	}
}

func TestGateErrorFailsClosed(t *testing.T) {
	reset(t)
	ctx := context.Background()
	edition.Register("example", fakeGate{
		features: map[string]bool{"reports": true},
		plugins:  map[string]bool{"forms": true},
		err:      errors.New("x"),
	})
	if edition.FeatureEnabled(ctx, "reports") || edition.PluginEntitled(ctx, "forms") {
		t.Error("gate error must yield false")
	}
}

func TestRegisterAfterFreezePanics(t *testing.T) {
	reset(t)
	edition.FreezeForTest()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	edition.Register("example", fakeGate{})
}
