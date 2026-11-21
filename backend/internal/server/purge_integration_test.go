//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/purgestate"
)

// recordingPurger is a module-style purger that records what it is sent.
type recordingPurger struct {
	mu   sync.Mutex
	reqs []purgestate.Request
}

func (p *recordingPurger) Purge(_ context.Context, r purgestate.Request) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, purgestate.Request{URLs: slices.Clone(r.URLs), All: r.All})
	return nil
}

func (p *recordingPurger) requests() []purgestate.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.reqs)
}

func TestPurgeOnPublish_Integration(t *testing.T) {
	const want = "https://example.test/purge-me"
	var mu sync.Mutex
	var bodies []string
	var auth string
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		auth = r.Header.Get("Authorization")
		mu.Unlock()
	}))
	t.Cleanup(recv.Close)
	webhookBodies := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(bodies)
	}

	fake := &recordingPurger{}
	purgestate.Reset()
	purgestate.Register("test-cdn", fake)
	t.Cleanup(purgestate.Reset)

	e := newWireEnv(t, map[string]string{
		"PUBLIC_BASE_URL":              "https://example.test",
		"HATAMOTO_PURGE_URL":           recv.URL + "/purge",
		"HATAMOTO_PURGE_TOKEN":         "t0k",
		"HATAMOTO_PURGE_ALLOW_PRIVATE": "true",
	})
	s, _ := e.server(t, "/admin")
	if s.purge == nil {
		t.Fatal("purge runner not built")
	}

	ctx := context.Background()
	schema := uuid.New()
	pool := e.db.Pool()
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings)
		VALUES ($1,'PurgePage','purge-page','{"type":"object"}','{"url_pattern":"/{slug}"}')`, schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schema)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schema)
	})

	ent, err := s.entityService.CreateEntity(ctx, nil, &entity.CreateEntityInput{
		SchemaID: schema, Name: "purge-me", Slug: "purge-me", Locale: s.siteLanguage(ctx), Attributes: jsontext.Value(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.entityService.UpdateEntityStatus(ctx, ent.ID, nil, entity.StatusPublished); err != nil {
		t.Fatal(err)
	}
	s.purge.Flush(ctx) // deterministic: no Run goroutine, no sleeps

	if got := webhookBodies(); len(got) != 1 || !strings.Contains(got[0], `"`+want+`"`) || !strings.Contains(got[0], `"all":false`) {
		t.Fatalf("webhook bodies after publish = %v", got)
	}
	mu.Lock()
	gotAuth := auth
	mu.Unlock()
	if gotAuth != "Bearer t0k" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if got := fake.requests(); len(got) != 1 || !slices.Contains(got[0].URLs, want) || got[0].All {
		t.Fatalf("registered purger after publish = %+v", got)
	}

	// Leaving "published" purges the old URL again.
	if _, err := s.entityService.UpdateEntityStatus(ctx, ent.ID, nil, entity.StatusDraft); err != nil {
		t.Fatal(err)
	}
	s.purge.Flush(ctx)
	if got := webhookBodies(); len(got) != 2 || !strings.Contains(got[1], `"`+want+`"`) {
		t.Fatalf("webhook bodies after unpublish = %v", got)
	}
	if got := fake.requests(); len(got) != 2 || !slices.Contains(got[1].URLs, want) {
		t.Fatalf("registered purger after unpublish = %+v", got)
	}
}

func TestNoPurgeRunnerWhenUnset_Integration(t *testing.T) {
	purgestate.Reset()
	t.Cleanup(purgestate.Reset)
	e := newWireEnv(t, nil)
	s, _ := e.server(t, "/admin")
	if s.purge != nil {
		t.Fatal("purge runner built with nothing configured")
	}
}
