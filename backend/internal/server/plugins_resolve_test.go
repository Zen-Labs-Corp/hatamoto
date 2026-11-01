package server

import (
	"context"
	"errors"
	"slices"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// siteLangRepo reads like the public repository: the site language ("en-XX")
// unless ctx carries a locale scope.
type siteLangRepo struct{ fakeResolveRepo }

func (r *siteLangRepo) GetPublishedBySlugAndSchemas(ctx context.Context, slug string, schemas []string) (*entity.Entity, error) {
	e, err := r.fakeResolveRepo.GetPublishedBySlugAndSchemas(ctx, slug, schemas)
	if err != nil {
		return nil, err
	}
	visible := entity.VisibleLocales(ctx)
	if visible == nil {
		visible = []string{"en-XX"}
	}
	if !slices.Contains(visible, e.Locale) {
		return nil, entity.ErrEntityNotFound
	}
	return e, nil
}

func TestResolvePluginPathLocalePrefixGate(t *testing.T) {
	t.Parallel()
	about := fixtureEntity("page", "about", "")
	about.Locale = "fr-XX"
	welcome := fixtureEntity("page", "welcome", "")
	welcome.Locale = "en-XX"
	secret := fixtureEntity("page", "secret", "")
	secret.Locale = "de-XX"
	repo := &siteLangRepo{fakeResolveRepo{rows: []*entity.Entity{about, welcome, secret}}}

	snapFor := func(entries ...entity.LocaleEntry) *entity.URLSnapshot {
		reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
		if len(entries) > 0 {
			set, err := entity.NewLocaleSet("en-XX", entries, false, "")
			if err != nil {
				t.Fatal(err)
			}
			reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
		}
		snap, err := reg.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	multi := snapFor(
		entity.LocaleEntry{Tag: "en-XX"},
		entity.LocaleEntry{Tag: "fr-XX", Segment: "xx"},
		entity.LocaleEntry{Tag: "de-XX", Segment: "yy", Hidden: true},
	)
	single := snapFor()

	tests := []struct {
		name string
		snap *entity.URLSnapshot
		path string
		// prefix is whether the plugin may use locale-prefixed paths.
		prefix bool
		want   *entity.Entity // nil = not found
	}{
		{"prefixed path finds the locale's entity", multi, "/xx/about", true, about},
		{"prefixed path with trailing slash", multi, "/xx/about/", true, about},
		{"unprefixed path is the default locale", multi, "/welcome", true, welcome},
		{"unprefixed path does not reach another locale", multi, "/about", true, nil},
		{"prefixed path does not reach another locale", multi, "/xx/welcome", true, nil},
		{"hidden locale prefix is not found", multi, "/yy/secret", true, nil},
		{"locale home is not an entity", multi, "/xx", true, nil},
		{"single locale: a prefix is just a path", single, "/xx/about", true, nil},
		{"single locale: plain path", single, "/welcome", true, welcome},
		{"gate off: prefixed path is not read in the locale", multi, "/xx/about", false, nil},
		{"gate off: hidden locale prefix is not found", multi, "/yy/secret", false, nil},
		{"gate off: unprefixed path is unchanged", multi, "/welcome", false, welcome},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolvePluginPath(context.Background(), repo, tc.snap, tc.path, tc.prefix)
			if tc.want == nil {
				if !errors.Is(err, entity.ErrEntityNotFound) {
					t.Fatalf("got %v, %v, want not found", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v, want %s", got, err, tc.want.Slug)
			}
		})
	}
}
