package server

import (
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

func TestNavLinksLoadsHomepageLazily(t *testing.T) {
	str := func(s string) *string { return &s }
	homeID, otherID := uuid.New(), uuid.New()
	newLinks := func(calls *int) *navLinks {
		return &navLinks{snap: entity.DefaultURLSnapshot(), loadHome: func() uuid.UUID {
			*calls++
			return homeID
		}}
	}

	var calls int
	l := newLinks(&calls)
	l.items([]*menu.MenuItem{
		{URL: str("/x"), EntitySlug: str("about"), EntityID: &otherID},
		{LinkType: menu.LinkTypeArchive, SchemaSlug: str("post")},
	})
	if calls != 0 {
		t.Errorf("URL and archive items loaded the homepage %d times, want 0", calls)
	}

	calls = 0
	l = newLinks(&calls)
	got := l.items([]*menu.MenuItem{
		{EntityID: &homeID, EntitySlug: str("platform")},
		{EntityID: &otherID, EntitySlug: str("about")},
	})
	if calls != 1 {
		t.Errorf("two entity items loaded the homepage %d times, want 1", calls)
	}
	if got[0].Href != "/" || got[1].Href != "/about" {
		t.Errorf("hrefs %q %q, want / /about", got[0].Href, got[1].Href)
	}

	// A literal with homeID set and no loader keeps working.
	fixed := &navLinks{snap: entity.DefaultURLSnapshot(), homeID: homeID}
	if href := fixed.menuHref(&menu.MenuItem{EntityID: &homeID, EntitySlug: str("platform")}); href != "/" {
		t.Errorf("fixed homeID href %q, want /", href)
	}
}
