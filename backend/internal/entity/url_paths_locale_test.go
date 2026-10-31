package entity

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/urlpattern"
)

func localeSnap(t *testing.T, prefixAll bool) *URLSnapshot {
	t.Helper()
	set, err := NewLocaleSet("en", []LocaleEntry{{Tag: "en", Segment: ""}, {Tag: "fr", Segment: "fr"}, {Tag: "nl", Segment: "nl"}}, prefixAll, "")
	if prefixAll {
		set, err = NewLocaleSet("en", []LocaleEntry{{Tag: "en", Segment: "en"}, {Tag: "fr", Segment: "fr"}, {Tag: "nl", Segment: "nl"}}, true, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	snap := buildSnap(t, &EntitySchema{Slug: "page", Name: "Page"}, &EntitySchema{Slug: "post", Name: "Post"})
	snap.locales = set
	return snap
}

func TestLocate_LocalePrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		prefixAll bool
		schema    string
		locale    string
		want      string
	}{
		{"default unprefixed", false, "page", "en", "/about"},
		{"other locale prefixed", false, "page", "fr", "/fr/about"},
		{"post pattern prefixed", false, "post", "nl", "/nl/blog/about"},
		{"shared unprefixed", false, "page", LocaleShared, "/about"},
		{"unknown locale unprefixed", false, "page", "it", "/about"},
		{"empty locale unprefixed", false, "page", "", "/about"},
		{"prefix-all default", true, "page", "en", "/en/about"},
		{"prefix-all shared", true, "page", LocaleShared, "/about"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := localeSnap(t, tt.prefixAll)
			loc, found, err := snap.Locate(tt.schema, nil, urlpattern.Vars{Slug: "about", Locale: tt.locale})
			if err != nil || !found || loc.Path != tt.want {
				t.Fatalf("Locate = %q, %v, %v; want %q", loc.Path, found, err, tt.want)
			}
		})
	}
}

func TestLocate_LocalePrefixBranches(t *testing.T) {
	// Section page and archive crumb branches.
	snap := buildSnap(t,
		schemaWith("page", `{"section_pages":true}`),
		schemaWith("doc", `{"url_pattern":"/docs/{slug}","archive_path":"/docs"}`),
	)
	snap.locales = localeSnap(t, false).locales
	if loc, _, err := snap.Locate("page", nil, urlpattern.Vars{Slug: "docs", Locale: "fr"}); err != nil || loc.Path != "/fr/docs" || !loc.Section {
		t.Fatalf("section page = %+v, %v", loc, err)
	}
	if loc, _, err := snap.Locate("doc", nil, urlpattern.Vars{Slug: "intro", Locale: "fr"}); err != nil || loc.Path != "/fr/docs/intro" || loc.ArchivePath != "/fr/docs" {
		t.Fatalf("doc = %+v, %v", loc, err)
	}
	// Plugin builder branch.
	RegisterEntityURLBuilder("loc-plug", func(_, slug string) string { return "/p/" + slug })
	snap = buildSnap(t, &EntitySchema{Slug: "loc-plug", Name: "P"})
	snap.locales = localeSnap(t, false).locales
	if loc, ok, err := snap.Locate("loc-plug", nil, urlpattern.Vars{Slug: "x", Locale: "fr"}); err != nil || !ok || loc.Path != "/fr/p/x" {
		t.Fatalf("builder: %q %v %v", loc.Path, ok, err)
	}
}

func TestWithLocale_Home(t *testing.T) {
	t.Parallel()
	snap := localeSnap(t, false)
	for tag, want := range map[string]string{"fr": "/fr", "en": "/", LocaleShared: "/"} {
		if got := snap.WithLocale(tag, "/"); got != want {
			t.Errorf("WithLocale(%q, /) = %q, want %q", tag, got, want)
		}
	}
	if got := DefaultURLSnapshot().WithLocale("fr", "/x"); got != "/x" {
		t.Errorf("single-locale WithLocale = %q", got)
	}
}

func TestURLRegistry_SetLocales(t *testing.T) {
	t.Parallel()
	reg := NewURLRegistry(staticLister{[]*EntitySchema{{Slug: "page", Name: "Page"}}}, nil)
	set, _ := NewLocaleSet("en", []LocaleEntry{{Tag: "en", Segment: ""}, {Tag: "fr", Segment: "fr"}}, false, "")
	reg.SetLocales(func(context.Context) (*LocaleSet, error) { return set, nil })
	snap, err := reg.Snapshot(context.Background())
	if err != nil || snap.Degraded() || snap.Locales() != set {
		t.Fatalf("snapshot locales = %v, %v", snap.Locales(), err)
	}
}

// A failed locale read fails the snapshot and is not cached: the next call
// reloads instead of serving one-locale behaviour for the whole TTL.
func TestURLRegistry_LocalesErrorIsNotCached(t *testing.T) {
	t.Parallel()
	reg := NewURLRegistry(staticLister{[]*EntitySchema{{Slug: "page", Name: "Page"}}}, nil)
	set, _ := NewLocaleSet("en", []LocaleEntry{{Tag: "en", Segment: ""}, {Tag: "fr", Segment: "fr"}}, false, "")
	var fail atomic.Bool
	fail.Store(true)
	reg.SetLocales(func(context.Context) (*LocaleSet, error) {
		if fail.Load() {
			return nil, errors.New("config unavailable")
		}
		return set, nil
	})
	if snap, err := reg.Snapshot(context.Background()); err != nil || !snap.Degraded() || snap.Locales() != nil {
		t.Fatalf("failed locale read: degraded = %v, locales = %v, err = %v", snap != nil && snap.Degraded(), snap != nil && snap.Locales() != nil, err)
	}
	fail.Store(false)
	snap, err := reg.Snapshot(context.Background())
	if err != nil || snap.Locales() != set {
		t.Fatalf("after recovery: locales = %v, %v", snap.Locales(), err)
	}
}
