package server

import (
	"context"
	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/media/modern"
	"hatamoto.dev/core/internal/siteconfig"
)

// themeExtraStore is the part of siteconfig.Store the format settings use.
type themeExtraStore interface {
	Get(ctx context.Context) (*siteconfig.Config, error)
	PatchThemeExtra(ctx context.Context, key string, raw jsontext.Value) error
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

// mediaFormatsService is what the admin's Image formats card talks to: the
// switches, and the rebuild job.
type mediaFormatsService struct {
	store   mediaFormatStore
	rebuild *media.Rebuilder
	changed func(ctx context.Context)
}

// Settings returns the switches.
func (m *mediaFormatsService) Settings(ctx context.Context) (media.Settings, error) {
	set, _, err := m.store.Load(ctx)
	return set, err
}

// SaveSettings stores the switches and purges rendered pages, so the markup
// follows them at once.
func (m *mediaFormatsService) SaveSettings(ctx context.Context, set media.Settings) error {
	if err := m.store.SaveSettings(ctx, set); err != nil {
		return err
	}
	m.changed(ctx)
	return nil
}

// Status returns the rebuild progress.
func (m *mediaFormatsService) Status(ctx context.Context) (media.State, error) {
	return m.rebuild.Status(ctx)
}

// StartRebuild begins a rebuild; media.ErrRebuildRunning when one is active.
func (m *mediaFormatsService) StartRebuild(ctx context.Context, actor string) error {
	return m.rebuild.Start(ctx, actor)
}

// newMediaFormats builds the rebuild job and the service over it. The job
// runs in the server's lifecycle context and audits through the admin sink.
func (s *Server) newMediaFormats() *mediaFormatsService {
	base := func() context.Context {
		if s.lifecycleCtx != nil {
			return s.lifecycleCtx
		}
		return context.Background()
	}
	hooks := media.RebuildHooks{
		Audit: func(ctx context.Context, action, actor string, detail map[string]any) {
			s.adminAudit(ctx, admin.AuditEvent{Actor: actor, Action: action, Target: "media", Detail: detail})
		},
		Done: s.mediaService.NotifyChanged,
	}
	s.mediaRebuilder = media.NewRebuilder(s.mediaService, s.mediaFormats, base, hooks, s.logger)
	return &mediaFormatsService{store: s.mediaFormats, rebuild: s.mediaRebuilder, changed: s.mediaService.NotifyChanged}
}
