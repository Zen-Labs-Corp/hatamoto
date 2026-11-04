package server

import (
	"context"
	"encoding/json"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/media/modern"
	"hatamoto.dev/core/internal/siteconfig"
)

// themeExtraStore is the part of siteconfig.Store the format settings use.
type themeExtraStore interface {
	Get(ctx context.Context) (*siteconfig.Config, error)
	PatchThemeExtra(ctx context.Context, key string, raw json.RawMessage) error
}

// mediaFormatStore keeps the modern-format switches and the rebuild state in
// theme_extra. Reads go through the site config's cache.
type mediaFormatStore struct{ site themeExtraStore }

// Load returns the switches and the job state.
func (m mediaFormatStore) Load(ctx context.Context) (media.Settings, media.State, error) {
	cfg, err := m.site.Get(ctx)
	if err != nil {
		return media.Settings{WebP: true, AVIF: true}, media.State{}, err
	}
	set, st := media.LoadState(cfg.ThemeExtra)
	return set, st, nil
}

// SaveSettings stores the switches.
func (m mediaFormatStore) SaveSettings(ctx context.Context, set media.Settings) error {
	return m.site.PatchThemeExtra(ctx, media.SettingsKey, media.SettingsRaw(set))
}

// SaveState stores the rebuild job's progress.
func (m mediaFormatStore) SaveState(ctx context.Context, st media.State) error {
	return m.site.PatchThemeExtra(ctx, media.StateKey, media.StateRaw(st))
}

// policy is the set of formats to write and to offer: the owner's switches,
// limited to what this build can encode. A failed read keeps the defaults.
func (m mediaFormatStore) policy(ctx context.Context) media.Formats {
	set, _, _ := m.Load(ctx)
	return media.Formats{
		WebP: set.WebP && modern.Available(modern.WebP),
		AVIF: set.AVIF && modern.Available(modern.AVIF),
	}
}
