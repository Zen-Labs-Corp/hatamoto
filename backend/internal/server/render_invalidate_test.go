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

		sink.Emit(context.Background(), entity.Event{Kind: kind, ID: uuid.New(), Slug: "x"})
		s.hooks.Wait()

		if !probe.cleared() {
			t.Errorf("%s did not clear render:*", kind)
		}
		if _, err := c.Get(context.Background(), cache.SitemapKey("index")); err == nil {
			t.Errorf("%s left a stale cached sitemap", kind)
		}
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
