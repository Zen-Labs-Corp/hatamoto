package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/indexnow"
	"hatamoto.dev/core/internal/seo"
)

const testIndexNowKey = "unit-test-key-1"

func TestIndexNowItemRules(t *testing.T) {
	id := uuid.New()
	live := &entity.Entity{ID: id, Status: entity.StatusPublished}
	draft := &entity.Entity{ID: id, Status: entity.StatusDraft}
	for name, c := range map[string]struct {
		ev   entity.Event
		kind string // "" = no ping
	}{
		"publish":          {entity.Event{Kind: entity.EventPublished, ID: id}, inKindPublished},
		"edit live":        {entity.Event{Kind: entity.EventUpdated, ID: id, Status: "published", PrevStatus: "published"}, inKindPublished},
		"unpublish":        {entity.Event{Kind: entity.EventUpdated, ID: id, Status: "draft", PrevStatus: "published"}, inKindUnpublished},
		"edit draft":       {entity.Event{Kind: entity.EventUpdated, ID: id, Status: "draft", PrevStatus: "draft"}, ""},
		"create draft":     {entity.Event{Kind: entity.EventCreated, ID: id, Status: "draft"}, ""},
		"delete live":      {entity.Event{Kind: entity.EventDeleted, ID: id, Entity: live}, inKindDeleted},
		"delete draft":     {entity.Event{Kind: entity.EventDeleted, ID: id, Entity: draft}, ""},
		"delete no copy":   {entity.Event{Kind: entity.EventDeleted, ID: id}, ""},
		"import":           {entity.Event{Kind: entity.EventImportCompleted, Import: &entity.ImportSummary{IDs: []uuid.UUID{id}}}, inKindImport},
		"import none kept": {entity.Event{Kind: entity.EventImportCompleted, Import: &entity.ImportSummary{}}, ""},
	} {
		it, ok := indexNowItem(c.ev)
		if ok != (c.kind != "") || it.Kind != c.kind {
			t.Errorf("%s: got (%q, %v), want kind %q", name, it.Kind, ok, c.kind)
		}
	}
}

func TestIndexNowEligible(t *testing.T) {
	page := func(mod func(*entity.Entity)) *entity.Entity {
		e := &entity.Entity{Slug: "a", Schema: &entity.EntitySchema{Slug: "page"}, Attributes: json.RawMessage(`{}`)}
		if mod != nil {
			mod(e)
		}
		return e
	}
	noindexPages := &seo.Settings{Index: seo.Index{NoIndexTypes: map[string]bool{"page": true}}}
	for name, c := range map[string]struct {
		set  *seo.Settings
		e    *entity.Entity
		want bool
	}{
		"plain":             {&seo.Settings{}, page(nil), true},
		"no schema":         {&seo.Settings{}, page(func(e *entity.Entity) { e.Schema = nil }), false},
		"entry noindex":     {&seo.Settings{}, page(func(e *entity.Entity) { e.Attributes = json.RawMessage(`{"no_index":true}`) }), false},
		"hidden in sitemap": {&seo.Settings{}, page(func(e *entity.Entity) { e.Attributes = json.RawMessage(`{"hide_from_sitemap":true}`) }), false},
		"type noindex":      {noindexPages, page(nil), false},
		"hidden person":     {&seo.Settings{}, page(func(e *entity.Entity) { e.Schema = &entity.EntitySchema{Slug: entity.PersonSchemaSlug} }), false},
		"public person": {&seo.Settings{}, page(func(e *entity.Entity) {
			e.Schema = &entity.EntitySchema{Slug: entity.PersonSchemaSlug}
			e.Attributes = json.RawMessage(`{"public":true}`)
		}), true},
		"no author pages": {&seo.Settings{Sitemap: seo.Sitemap{NoAuthor: true}}, page(func(e *entity.Entity) {
			e.Schema = &entity.EntitySchema{Slug: entity.PersonSchemaSlug}
			e.Attributes = json.RawMessage(`{"public":true}`)
		}), false},
	} {
		if got := indexNowEligible(c.set, c.e); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}

type inRig struct {
	set    *seo.Settings
	ents   map[uuid.UUID]*entity.Entity
	mu     sync.Mutex
	posted []map[string]any
	runner *indexnow.Runner
	res    *indexNowResolver
	logs   strings.Builder
}

func newInRig(t *testing.T, set seo.IndexNow) *inRig {
	t.Helper()
	g := &inRig{set: &seo.Settings{IndexNow: set}, ents: map[uuid.UUID]*entity.Entity{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		g.mu.Lock()
		g.posted = append(g.posted, m)
		g.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	res := &indexNowResolver{
		settings: func(context.Context) *seo.Settings { return g.set },
		baseURL:  func(context.Context) string { return "https://example.com" },
		get: func(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
			if e, ok := g.ents[id]; ok {
				return e, nil
			}
			return nil, entity.ErrEntityNotFound
		},
		path:     func(_ context.Context, e *entity.Entity) string { return "/" + e.Slug },
		warnBase: func() {},
	}
	g.res = res
	g.runner = indexnow.New(res, slog.New(slog.NewTextHandler(&g.logs, nil)), indexnow.Options{
		Client:      srv.Client(),
		EndpointURL: func(string) string { return srv.URL + "/indexnow" },
	})
	return g
}

func (g *inRig) page(slug string, st entity.EntityStatus) *entity.Entity {
	e := &entity.Entity{ID: uuid.New(), Slug: slug, Status: st, Schema: &entity.EntitySchema{Slug: "page"}, Attributes: json.RawMessage(`{}`)}
	g.ents[e.ID] = e
	return e
}

func (g *inRig) urls(t *testing.T) []string {
	t.Helper()
	g.runner.Flush(context.Background())
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, p := range g.posted {
		for _, u := range p["urlList"].([]any) {
			out = append(out, u.(string))
		}
	}
	return out
}

func TestIndexNowSkipsHiddenLocale(t *testing.T) {
	g := newInRig(t, seo.IndexNow{Enabled: true, Key: testIndexNowKey})
	g.res.localeHidden = func(_ context.Context, l string) bool { return l == "fr" }
	e := g.page("bonjour", entity.StatusPublished)
	e.Locale = "fr"
	indexNowSink{r: g.runner}.Emit(context.Background(), entity.Event{Kind: entity.EventPublished, ID: e.ID})
	if got := g.urls(t); len(got) != 0 {
		t.Fatalf("hidden locale pinged: %v", got)
	}
}

func TestIndexNowPublishSubmitsOneURL(t *testing.T) {
	g := newInRig(t, seo.IndexNow{Enabled: true, Key: testIndexNowKey})
	e := g.page("hello", entity.StatusPublished)
	indexNowSink{r: g.runner}.Emit(context.Background(), entity.Event{Kind: entity.EventPublished, ID: e.ID})
	if got := g.urls(t); len(got) != 1 || got[0] != "https://example.com/hello" {
		t.Fatalf("urls = %v", got)
	}
	if !strings.Contains(g.logs.String(), "indexnow: submitted") || strings.Contains(g.logs.String(), testIndexNowKey) {
		t.Fatalf("log = %q", g.logs.String())
	}
}

func TestIndexNowOffSendsNothing(t *testing.T) {
	g := newInRig(t, seo.IndexNow{Key: testIndexNowKey})
	e := g.page("hello", entity.StatusPublished)
	indexNowSink{r: g.runner}.Emit(context.Background(), entity.Event{Kind: entity.EventPublished, ID: e.ID})
	if got := g.urls(t); len(got) != 0 {
		t.Fatalf("urls = %v, want none", got)
	}
}

func TestIndexNowUnpublishAndDelete(t *testing.T) {
	g := newInRig(t, seo.IndexNow{Enabled: true, Key: testIndexNowKey})
	sink := indexNowSink{r: g.runner}
	old := g.page("old", entity.StatusDraft) // now a draft, was published
	sink.Emit(context.Background(), entity.Event{Kind: entity.EventUpdated, ID: old.ID, Status: "draft", PrevStatus: "published"})
	gone := &entity.Entity{ID: uuid.New(), Slug: "gone", Status: entity.StatusPublished, Schema: &entity.EntitySchema{Slug: "page"}, Attributes: json.RawMessage(`{}`)}
	sink.Emit(context.Background(), entity.Event{Kind: entity.EventDeleted, ID: gone.ID, Entity: gone})
	got := g.urls(t)
	if len(got) != 2 || got[0] != "https://example.com/old" || got[1] != "https://example.com/gone" {
		t.Fatalf("urls = %v", got)
	}
}

func TestIndexNowImportIsOneRequestForManyChunks(t *testing.T) {
	g := newInRig(t, seo.IndexNow{Enabled: true, Key: testIndexNowKey})
	sink := indexNowSink{r: g.runner}
	for chunk := range 3 {
		var ids []uuid.UUID
		for i := range 5 {
			ids = append(ids, g.page("p"+string(rune('a'+chunk))+string(rune('a'+i)), entity.StatusPublished).ID)
		}
		ids = append(ids, g.page("draft"+string(rune('a'+chunk)), entity.StatusDraft).ID)
		sink.Emit(context.Background(), entity.Event{Kind: entity.EventImportCompleted, Import: &entity.ImportSummary{IDs: ids}})
	}
	got := g.urls(t)
	if len(got) != 15 || len(g.posted) != 1 {
		t.Fatalf("urls = %d in %d requests, want 15 in 1", len(got), len(g.posted))
	}
}

func TestIndexNowScheduledPublishNotifies(t *testing.T) {
	g := newInRig(t, seo.IndexNow{Enabled: true, Key: testIndexNowKey})
	e := g.page("later", entity.StatusPublished)
	(&Server{indexNow: g.runner}).notifyIndexNowPublished(e.ID)
	if got := g.urls(t); len(got) != 1 || got[0] != "https://example.com/later" {
		t.Fatalf("urls = %v", got)
	}
	(&Server{}).notifyIndexNowPublished(e.ID) // demo mode: no runner, no panic
}

func TestIndexNowKeyFileRoute(t *testing.T) {
	f := newRenderFixture(t)
	on := true
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), feed: stub("feed"), xsl: stub("xsl"), formPost: stub("form"),
		keyFile: func(_ context.Context, name string) (string, bool) {
			return testIndexNowKey, on && name == testIndexNowKey+".txt"
		},
	})
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	w := getPath(h, "/"+testIndexNowKey+".txt")
	if w.Code != http.StatusOK || w.Body.String() != testIndexNowKey || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("on: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Type"))
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if w := getPath(h, "/other.txt"); strings.Contains(w.Body.String(), testIndexNowKey) {
		t.Fatal("served for a wrong name")
	}
	on = false
	if w := getPath(h, "/"+testIndexNowKey+".txt"); w.Code != http.StatusNotFound {
		t.Fatalf("off: status %d, want 404", w.Code)
	}
}

func TestIndexNowKeyFileFollowsSettings(t *testing.T) {
	if _, ok := (&Server{}).indexNowKeyFile(context.Background(), "x.txt"); ok {
		t.Fatal("served without a settings store")
	}
}
