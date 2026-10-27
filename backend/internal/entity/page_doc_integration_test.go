//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A5: the page_doc dual-write against a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/entity -run PageDocIntegration
func TestPageDocIntegration(t *testing.T) {
	db := newIntegrationDB(t)
	ctx := context.Background()
	schemaID := uuid.New()
	slug := "pdoc-" + uuid.NewString()[:8]
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`,
		schemaID, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	svc := NewService(NewPostgresSchemaRepository(db), NewPostgresEntityRepository(db), slog.New(slog.NewTextHandler(io.Discard, nil)))

	create := func(name, attrs string) *Entity {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: name, Attributes: json.RawMessage(attrs)})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return e
	}
	update := func(id uuid.UUID, attrs, summary string) (*Entity, error) {
		a := json.RawMessage(attrs)
		return svc.UpdateEntity(ctx, id, nil, &UpdateEntityInput{Attributes: &a, ChangeSummary: &summary})
	}
	load := func(id uuid.UUID) map[string]json.RawMessage {
		t.Helper()
		e, err := svc.GetEntity(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(e.Attributes, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	versions := func(id uuid.UUID) []*EntityVersion {
		t.Helper()
		vs, err := svc.ListVersions(ctx, id, 100)
		if err != nil {
			t.Fatal(err)
		}
		return vs
	}

	const sections = `[{"id":"a","type":"content","sort_order":0,"content":{"body":"<p>one</p>"}}]`
	const v1Doc = `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section","v1":true},"c":[` +
		`{"id":"a-c","t":"component","p":{"type":"content","content":{"body":"<p>doc</p>"}}}]}]}`
	const builderDoc = `{"v":2,"nodes":[{"id":"row","t":"container","p":{"tag":"div"},"c":[` +
		`{"id":"h","t":"heading","p":{"text":"Hi","level":2}}]}]}`

	// ISC-2/5: a doc write stores no page_sections; sections only on create convert once.
	e := create("mirror", `{"page_doc":`+v1Doc+`,"page_sections":`+sections+`}`)
	if m := load(e.ID); m["page_sections"] != nil || m["page_doc"] == nil {
		t.Fatalf("doc create: %v", m)
	}
	if _, err := update(e.ID, `{"page_doc":`+builderDoc+`}`, "layout"); err != nil {
		t.Fatal(err)
	}
	if m := load(e.ID); m["page_sections"] != nil || m["page_doc"] == nil {
		t.Errorf("builder-only doc write: %v", m)
	}

	// ISC-9: a changed page_sections write onto a page with a doc is refused.
	before := len(versions(e.ID))
	_, err := update(e.ID, `{"page_sections":`+sections+`}`, "v1 editor")
	if !errors.Is(err, ErrPageSectionsRemoved) {
		t.Errorf("sections write on a doc page: err = %v, want ErrPageSectionsRemoved", err)
	}
	if n := len(versions(e.ID)); n != before {
		t.Errorf("refused write created a version: %d -> %d", before, n)
	}

	// ISC-5: sections only on create store the doc and no mirror.
	p := create("converted", `{"title":"T","page_sections":`+sections+`}`)
	if m := load(p.ID); m["page_doc"] == nil || m["page_sections"] != nil || !jsonContains(m["page_doc"], "one") {
		t.Fatalf("create with sections: %v", m)
	}

	// ISC-2: a doc write on a page with a stored mirror leaves the mirror bytes alone.
	raw := `{"title":"T","page_sections":` + sections + `}`
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET attributes = $2::jsonb WHERE id = $1`, p.ID, raw); err != nil {
		t.Fatal(err)
	}
	var mirrorBefore string
	if err := db.Pool().QueryRow(ctx, `SELECT attributes->'page_sections'::text FROM hatamoto_entities WHERE id = $1`, p.ID).Scan(&mirrorBefore); err != nil {
		t.Fatal(err)
	}

	// ISC-6: a name-only change is not needed here; an unrelated attributes write converts once.
	if _, err := update(p.ID, `{"title":"T2","page_sections":`+sections+`}`, "edit"); err != nil {
		t.Fatal(err)
	}
	m := load(p.ID)
	if m["page_doc"] == nil || string(m["page_sections"]) == "" {
		t.Fatalf("convert-once kept no mirror: %v", m)
	}
	next := `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"section","v1":true},"c":[` +
		`{"id":"a-c","t":"component","p":{"type":"content","content":{"body":"<p>edited</p>"}}}]}]}`
	if _, err := update(p.ID, `{"title":"T2","page_doc":`+next+`}`, "doc edit"); err != nil {
		t.Fatal(err)
	}
	m = load(p.ID)
	if !jsonContains(m["page_doc"], "edited") {
		t.Errorf("doc not stored: %s", m["page_doc"])
	}
	var mirrorAfter string
	if err := db.Pool().QueryRow(ctx, `SELECT attributes->'page_sections'::text FROM hatamoto_entities WHERE id = $1`, p.ID).Scan(&mirrorAfter); err != nil {
		t.Fatal(err)
	}
	if mirrorAfter != mirrorBefore {
		t.Errorf("mirror rewritten:\n%s\n%s", mirrorBefore, mirrorAfter)
	}
	afterEdit := len(versions(p.ID))

	// ISC-10: a no-op whole-attributes round trip creates no version.
	if _, err := update(p.ID, string(mustMarshal(t, m)), "no-op"); err != nil {
		t.Fatal(err)
	}
	if n := len(versions(p.ID)); n != afterEdit {
		t.Errorf("no-op save created a version: %d -> %d", afterEdit, n)
	}

	// Restoring a pre-doc blob (sections only) is accepted and becomes a doc.
	var preDoc int
	for _, v := range versions(p.ID) {
		var a map[string]json.RawMessage
		_ = json.Unmarshal(v.Attributes, &a)
		if a["page_doc"] == nil && (preDoc == 0 || v.Version < preDoc) {
			preDoc = v.Version
		}
	}
	if preDoc == 0 {
		t.Fatal("no pre-doc version in history")
	}
	if _, err := svc.RestoreVersion(ctx, p.ID, nil, preDoc); err != nil {
		t.Fatalf("restore of a pre-doc blob: %v", err)
	}
	m = load(p.ID)
	if m["page_doc"] == nil || !jsonContains(m["page_doc"], "one") {
		t.Errorf("restore did not convert the restored sections: %s", m["page_doc"])
	}
}

func jsonContains(raw json.RawMessage, s string) bool {
	return len(raw) > 0 && strings.Contains(string(raw), s)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
