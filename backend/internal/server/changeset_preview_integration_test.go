//go:build integration

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/jsonx"
)

// A staged update and a staged create are previewed from a real database: the
// preview shows the staged title, the live page still shows the old one, and
// nothing is written (row counts, updated_at, item results).
func TestChangesetPreviewWritesNothing_Integration(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	e := newWireEnv(t, nil)
	s, _ := e.server(t, "/admin")
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sfx := uuid.NewString()[:6]
	schemaSlug, slug := "cp-"+sfx, "about-"+sfx
	schemaID, pageID := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes,origin)
		VALUES ($1,$2,'About',$3,'published',now(),'{"meta_title":"Old title"}'::jsonb,'plugin:cp-plugin')`, pageID, schemaID, slug)

	id, err := s.changesets.Begin(ctx, uuid.New(), "cp-plugin")
	if err != nil {
		t.Fatal(err)
	}
	upd, _ := jsonx.Marshal(map[string]any{"name": "About us", "attributes": map[string]any{"meta_title": "Staged title"}})
	cre, _ := jsonx.Marshal(map[string]any{"schema": schemaSlug, "name": "Pricing", "slug": "pricing-" + sfx, "attributes": map[string]any{"meta_title": "Staged new page"}})
	for _, it := range []changeset.Item{
		{Kind: changeset.KindEntity, Op: changeset.OpUpsert, TargetID: pageID.String(), Payload: upd},
		{Kind: changeset.KindEntity, Op: changeset.OpUpsert, SourceKey: "new-" + sfx, Payload: cre},
	} {
		if err := s.changesets.Stage(ctx, id, it); err != nil {
			t.Fatal(err)
		}
	}

	snapshot := func() string {
		var out string
		err := e.db.Pool().QueryRow(ctx, `SELECT
			(SELECT count(*) FROM hatamoto_entities WHERE schema_id=$1)::text || '|' ||
			(SELECT updated_at::text FROM hatamoto_entities WHERE id=$2) || '|' ||
			(SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id=$3 AND result IS NOT NULL)::text || '|' ||
			(SELECT count(*) FROM hatamoto_changeset_source_keys)::text || '|' ||
			(SELECT state FROM hatamoto_changesets WHERE id=$3)`, schemaID, pageID, id).Scan(&out)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()

	for seq, want := range map[int]string{1: "Staged title", 2: "Staged new page"} {
		body, err := s.PreviewChangeset(ctx, id, seq)
		if err != nil || !strings.Contains(string(body), "<title>"+want) {
			t.Errorf("preview of item %d: err %v, want title %q in\n%.600s", seq, err, want, body)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/"+slug, nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Old title") || strings.Contains(rec.Body.String(), "Staged title") {
		t.Errorf("live page: %d, must still show the old title", rec.Code)
	}
	live := httptest.NewRecorder()
	s.Handler().ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/pricing-"+sfx, nil))
	if live.Code != 404 {
		t.Errorf("the staged create is live: %d", live.Code)
	}
	if after := snapshot(); after != before {
		t.Errorf("preview wrote something:\nbefore %s\nafter  %s", before, after)
	}
	if !strings.HasSuffix(before, "|staged") || !strings.HasPrefix(before, "1|") {
		t.Errorf("unexpected start state %s", before)
	}
}
