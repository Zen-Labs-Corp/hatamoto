package server

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cdnpurge"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/purgestate"
)

func TestPurgeItemRules(t *testing.T) {
	id := uuid.New()
	live := &entity.Entity{ID: id, Status: entity.StatusPublished}
	draft := &entity.Entity{ID: id, Status: entity.StatusDraft}
	for name, c := range map[string]struct {
		ev   entity.Event
		kind string // "" = no purge
	}{
		"publish":          {entity.Event{Kind: entity.EventPublished, ID: id}, pgKindPublished},
		"edit live":        {entity.Event{Kind: entity.EventUpdated, ID: id, Status: "published", PrevStatus: "published"}, pgKindPublished},
		"unpublish":        {entity.Event{Kind: entity.EventUpdated, ID: id, Status: "draft", PrevStatus: "published"}, pgKindUnpublished},
		"edit draft":       {entity.Event{Kind: entity.EventUpdated, ID: id, Status: "draft", PrevStatus: "draft"}, ""},
		"create draft":     {entity.Event{Kind: entity.EventCreated, ID: id, Status: "draft"}, ""},
		"delete live":      {entity.Event{Kind: entity.EventDeleted, ID: id, Entity: live}, pgKindDeleted},
		"delete draft":     {entity.Event{Kind: entity.EventDeleted, ID: id, Entity: draft}, ""},
		"import":           {entity.Event{Kind: entity.EventImportCompleted, Import: &entity.ImportSummary{IDs: []uuid.UUID{id}}}, pgKindImport},
		"import none kept": {entity.Event{Kind: entity.EventImportCompleted, Import: &entity.ImportSummary{}}, ""},
	} {
		it, ok := purgeItem(c.ev)
		if ok != (c.kind != "") || it.Kind != c.kind {
			t.Errorf("%s: got (%q, %v), want kind %q", name, it.Kind, ok, c.kind)
		}
	}
}

func TestPurgeResolverURLs(t *testing.T) {
	ents := map[uuid.UUID]*entity.Entity{}
	add := func(slug, schema, locale string, st entity.EntityStatus) *entity.Entity {
		e := &entity.Entity{ID: uuid.New(), Slug: slug, Locale: locale, Status: st,
			Schema: &entity.EntitySchema{Slug: schema, IsBuiltin: schema == entity.PartSchemaSlug || schema == entity.BlockSchemaSlug}, Attributes: jsontext.Value(`{}`)}
		ents[e.ID] = e
		return e
	}
	warned := 0
	base := "https://example.com"
	res := &purgeResolver{
		baseURL: func(context.Context) string { return base },
		get: func(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
			if e, ok := ents[id]; ok {
				return e, nil
			}
			return nil, entity.ErrEntityNotFound
		},
		locate: func(_ context.Context, e *entity.Entity) entity.EntityLocation {
			if e.Schema.Slug == "post" {
				return entity.EntityLocation{Path: "/blog/" + e.Slug, ArchivePath: "/blog"}
			}
			return entity.EntityLocation{Path: "/" + e.Slug}
		},
		localeHidden: func(_ context.Context, l string) bool { return l == "fr" },
		warnBase:     func() { warned++ },
	}
	ctx := context.Background()
	check := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: got %v want %v", name, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: got %v want %v", name, got, want)
				return
			}
		}
	}

	page := add("a", "page", "en", entity.StatusPublished)
	post := add("p", "post", "en", entity.StatusPublished)
	check("page", res.URLs(ctx, cdnpurge.Item{Kind: pgKindPublished, ID: page.ID}), "https://example.com/a")
	check("post with archive", res.URLs(ctx, cdnpurge.Item{Kind: pgKindPublished, ID: post.ID}),
		"https://example.com/blog/p", "https://example.com/blog")

	check("hidden locale", res.URLs(ctx, cdnpurge.Item{Kind: pgKindPublished, ID: add("b", "page", "fr", entity.StatusPublished).ID}))
	check("part schema", res.URLs(ctx, cdnpurge.Item{Kind: pgKindPublished, ID: add("c", entity.PartSchemaSlug, "en", entity.StatusPublished).ID}))
	check("block schema", res.URLs(ctx, cdnpurge.Item{Kind: pgKindPublished, ID: add("d", entity.BlockSchemaSlug, "en", entity.StatusPublished).ID}))
	check("reverted publish", res.URLs(ctx, cdnpurge.Item{Kind: pgKindPublished, ID: add("e", "page", "en", entity.StatusDraft).ID}))
	check("unpublished draft keeps old URL", res.URLs(ctx, cdnpurge.Item{Kind: pgKindUnpublished, ID: add("f", "page", "en", entity.StatusDraft).ID}), "https://example.com/f")
	check("deleted", res.URLs(ctx, cdnpurge.Item{Kind: pgKindDeleted, Entity: page}), "https://example.com/a")
	check("import", res.URLs(ctx, cdnpurge.Item{Kind: pgKindImport, IDs: []uuid.UUID{page.ID, uuid.New()}}), "https://example.com/a")
	check("paths", res.URLs(ctx, cdnpurge.Item{Paths: []string{"/old", "/old/x"}}), "https://example.com/old", "https://example.com/old/x")
	check("all item", res.URLs(ctx, cdnpurge.Item{All: true}))

	base = ""
	check("no base", res.URLs(ctx, cdnpurge.Item{Paths: []string{"/old"}}))
	if warned != 1 {
		t.Errorf("warnBase called %d times, want 1", warned)
	}
}

func TestNewPurgeRunnerNilWhenUnset(t *testing.T) {
	purgestate.Reset()
	t.Cleanup(purgestate.Reset)
	s := &Server{cfg: &config.Config{}}
	if s.newPurgeRunner() != nil {
		t.Error("runner built with nothing configured")
	}
	s.cfg.Purge.URL = "https://cdn.example.com/purge"
	if s.newPurgeRunner() == nil {
		t.Error("runner not built with a purge URL")
	}
}

func TestPageCacheClearNotifiesPurge(t *testing.T) {
	n := 0
	p := &pageCacheService{cleared: func() { n++ }}
	if _, err := p.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cleared called %d times, want 1", n)
	}
}
