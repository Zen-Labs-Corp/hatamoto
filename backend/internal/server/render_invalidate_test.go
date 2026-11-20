package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// renderProbe wraps a real in-process cache holding one rendered-page entry,
// so tests can assert an invalidation dropped the render:* namespace.
type renderProbe struct{ c *cache.Cache }

const probeKey = "render:v1:probe:-:/p"

func newRenderProbe(log *slog.Logger) *renderProbe {
	p := &renderProbe{c: cache.New(log)}
	p.prime()
	return p
}

func (p *renderProbe) prime() {
	_ = p.c.Set(context.Background(), probeKey, []byte("x"), time.Minute)
}

func (p *renderProbe) cleared() bool {
	_, err := p.c.Get(context.Background(), probeKey)
	return err != nil
}

func TestEntityEventsClearRenderedPages(t *testing.T) {
	for _, kind := range []entity.EventKind{
		entity.EventCreated, entity.EventUpdated, entity.EventPublished, entity.EventDeleted,
	} {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		probe := newRenderProbe(log)
		c := probe.c
		_ = c.Set(context.Background(), cache.SitemapKey("index"), []byte("<xml/>"), time.Minute)
		s := &Server{
			logger: log,
			hooks:  hooks.New(log),
			infra:  &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, log)},
		}
		s.registerRenderInvalidation()
		sink := pluginhost.NewEntitySink(s.hooks, log)

		tok := c.Generation()
		sink.Emit(context.Background(), entity.Event{Kind: kind, ID: uuid.New(), Slug: "x"})
		s.hooks.Wait()

		// Every page carries TagAnyEntity, so an entity event rejects the
		// in-flight store of any page, including one of another entity.
		for _, path := range []string{"/b", "/l"} {
			if ok, _ := c.SetIfGenerationTagged(context.Background(), "render:v1:probe:-:"+path, []byte("x"), time.Minute, tok,
				pageCacheTags(&render.PageView{EntityID: uuid.NewString()})...); ok {
				t.Errorf("%s: in-flight store of %s accepted", kind, path)
			}
		}

		if !probe.cleared() {
			t.Errorf("%s did not clear render:*", kind)
		}
		if _, err := c.Get(context.Background(), cache.SitemapKey("index")); err == nil {
			t.Errorf("%s left a stale cached sitemap", kind)
		}
	}
}

// ownTaggedStoreSurvives registers the entity hooks over a real cache and
// reports whether an in-flight store of another entity's page, started before
// fire, is still accepted after it.
func ownTaggedStoreSurvives(t *testing.T, fire func(*Server)) bool {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := cache.New(log)
	s := &Server{
		logger: log,
		hooks:  hooks.New(log),
		infra:  &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, log)},
	}
	s.registerRenderInvalidation()
	tok := c.Generation()
	fire(s)
	s.hooks.Wait()
	ok, _ := c.SetIfGenerationTagged(context.Background(), "render:v1:probe:-:/b", []byte("b"), time.Minute, tok, cache.EntityTag(uuid.New()))
	return ok
}

func TestImportCompletedStaysGlobal(t *testing.T) {
	if ownTaggedStoreSurvives(t, func(s *Server) {
		pluginhost.NewEntitySink(s.hooks, s.logger).Emit(context.Background(), entity.Event{
			Kind: entity.EventImportCompleted, Import: &entity.ImportSummary{Source: "x"},
		})
	}) {
		t.Fatal("import.completed left an in-flight store alive")
	}
}

func TestEntityEventWithoutIDStaysGlobal(t *testing.T) {
	for name, payload := range map[string]any{
		"empty id":   sdk.EntityEvent{ID: ""},
		"bad id":     sdk.EntityEvent{ID: "not-a-uuid"},
		"other type": "x",
	} {
		if ownTaggedStoreSurvives(t, func(s *Server) {
			s.hooks.Fire(context.Background(), sdk.EntityUpdated.Name, payload)
		}) {
			t.Errorf("%s: event without a usable ID did not purge globally", name)
		}
	}
	if !ownTaggedStoreSurvives(t, func(s *Server) {
		s.hooks.Fire(context.Background(), sdk.EntityUpdated.Name, &sdk.EntityEvent{ID: uuid.NewString()})
	}) {
		t.Error("pointer payload with an ID went global")
	}
}

func TestSitemapCachedOnlyWhenBaseURLPinned(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		base string
		want bool
	}{{"", false}, {"  ", false}, {"https://example.com", true}} {
		cfg := &config.Config{}
		cfg.PublicSite.BaseURL = tc.base
		s := &Server{logger: log, cfg: cfg, infra: NewInfrastructure(log)}
		r := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
		r.Host = "evil.example"

		s.cacheSitemapXML(r, "index", []byte("<urlset>evil.example</urlset>"))
		_, hit := s.getCachedSitemapXML(r, httptest.NewRecorder(), cache.SitemapKey("index"))
		if hit != tc.want {
			t.Errorf("PUBLIC_BASE_URL %q: cached = %v, want %v", tc.base, hit, tc.want)
		}
	}
}

// The publish worker's promotion must purge synchronously: the plugin hook that
// also purges runs on its own goroutine, and with a long TTL the page has to be
// live as soon as the tick returns (no hooks.Wait here on purpose).
func TestScheduledPublishSinkClearsRenderedPagesSynchronously(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	probe := newRenderProbe(log)
	c := probe.c
	_ = c.Set(context.Background(), cache.SitemapKey("index"), []byte("<xml/>"), time.Minute)
	s := &Server{
		logger: log,
		infra:  &Infrastructure{Cache: c, CacheInvalidator: cache.NewCacheInvalidator(c, log)},
	}
	var notified bool
	sink := scheduledPublishSink{s: s, next: publishedFunc(func() { notified = true })}

	sink.EntityPublished(context.Background(), uuid.New(), "page", "later", "Later", 1)

	if !probe.cleared() {
		t.Error("scheduled publish did not clear render:*")
	}
	if _, err := c.Get(context.Background(), cache.SitemapKey("index")); err == nil {
		t.Error("scheduled publish left a stale cached sitemap")
	}
	if !notified {
		t.Error("plugin notification skipped")
	}
}

type publishedFunc func()

func (f publishedFunc) EntityPublished(context.Context, uuid.UUID, string, string, string, int) { f() }

// The narrow path a comment change will use: another entity's page, tagged by
// the real pageCacheTags, is untouched and its in-flight store still lands.
func TestRenderedEntityDropSparesOtherPages(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	c := cache.New(log)
	a, b := uuid.New(), uuid.New()
	tagsOf := func(id uuid.UUID) []string { return pageCacheTags(&render.PageView{EntityID: id.String()}) }
	keyA, keyB := "render:v1:probe:-:/a", "render:v1:probe:-:/b"
	if ok, _ := c.SetIfGenerationTagged(ctx, keyA, []byte("a"), time.Minute, c.Generation(), tagsOf(a)...); !ok {
		t.Fatal("store /a rejected")
	}
	if ok, _ := c.SetIfGenerationTagged(ctx, keyB, []byte("b"), time.Minute, c.Generation(), tagsOf(b)...); !ok {
		t.Fatal("store /b rejected")
	}
	tok := c.Generation()
	if err := cache.NewCacheInvalidator(c, log).InvalidateRenderedEntity(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, keyA); err == nil {
		t.Error("/a survived")
	}
	if _, err := c.Get(ctx, keyB); err != nil {
		t.Error("/b dropped")
	}
	if ok, _ := c.SetIfGenerationTagged(ctx, keyB, []byte("b2"), time.Minute, tok, tagsOf(b)...); !ok {
		t.Error("in-flight store of /b rejected")
	}
}
