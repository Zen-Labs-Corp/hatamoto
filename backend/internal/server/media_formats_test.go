package server

import (
	"context"
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/siteconfig"
)

type fakeThemeExtra struct{ extra map[string]json.RawMessage }

func (f *fakeThemeExtra) Get(context.Context) (*siteconfig.Config, error) {
	raw, _ := json.Marshal(f.extra)
	return &siteconfig.Config{ThemeExtra: raw}, nil
}

func (f *fakeThemeExtra) PatchThemeExtra(_ context.Context, key string, raw json.RawMessage) error {
	if f.extra == nil {
		f.extra = map[string]json.RawMessage{}
	}
	f.extra[key] = raw
	return nil
}

func TestMediaFormatStoreKeepsSettingsAndStateApart(t *testing.T) {
	ctx := context.Background()
	f := &fakeThemeExtra{extra: map[string]json.RawMessage{"builder": json.RawMessage(`{"x":1}`)}}
	st := mediaFormatStore{site: f}

	set, state, err := st.Load(ctx)
	if err != nil || !set.WebP || !set.AVIF || state.Running {
		t.Fatalf("defaults: %+v %+v %v", set, state, err)
	}
	if err := st.SaveSettings(ctx, media.Settings{WebP: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveState(ctx, media.State{Running: true, Total: 5, Done: 2}); err != nil {
		t.Fatal(err)
	}
	// Saving progress must not touch the switches, and the other theme keys stay.
	if err := st.SaveState(ctx, media.State{Running: true, Total: 5, Done: 3}); err != nil {
		t.Fatal(err)
	}
	set, state, _ = st.Load(ctx)
	if !set.WebP || set.AVIF || state.Done != 3 || string(f.extra["builder"]) != `{"x":1}` {
		t.Errorf("after saves: %+v %+v %s", set, state, f.extra["builder"])
	}
	if p := st.policy(ctx); !p.WebP || p.AVIF {
		t.Errorf("policy %+v", p)
	}
}

func TestCurrentMediaFormatsDefaultsToOn(t *testing.T) {
	if f := (&Server{}).currentMediaFormats(context.Background()); !f.WebP || !f.AVIF {
		t.Errorf("%+v", f)
	}
}
