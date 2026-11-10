//go:build integration

package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/sdk"
)

// termsITRig is a fetch-phase run of a ^0.20 plugin holding entities.read and
// terms.read over a real database whose statements are counted.
type termsITRig struct {
	a       *API
	rc      RunCtx
	db      *database.DB
	counter *testutil.QueryCounter
	tag     string
	schema  string
}

func newTermsITRig(t *testing.T) *termsITRig {
	t.Helper()
	r := &termsITRig{counter: &testutil.QueryCounter{}, tag: uuid.NewString()[:8]}
	r.schema = "tm-" + r.tag
	var rig *API
	a, rc, _ := runRig(t, func(_ *plugrun.Run, s *Services) {
		ctx := context.Background()
		pcfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		pcfg.ConnConfig.Tracer = r.counter
		pcfg.MaxConns, pcfg.MinConns = 4, 1
		r.db, err = database.NewWithConfig(ctx, pcfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(r.db.Close)
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		schemas := entity.NewPostgresSchemaRepository(r.db)
		sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: "terms " + r.tag, Slug: r.schema,
			Schema: jsontext.Value(`{"type":"object","properties":{}}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = r.db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
			_, _ = r.db.Pool().Exec(bg, `DELETE FROM hatamoto_terms WHERE slug LIKE '%-' || $1`, r.tag)
			_ = schemas.Delete(bg, sc.ID)
		})
		s.Entities = entity.NewService(schemas, entity.NewPostgresEntityRepository(r.db), log)
		s.Terms = taxonomy.NewService(taxonomy.NewPostgresRepository(r.db), log)
		s.PublicEntityIDs = entity.NewPostgresPublicRepository(r.db).PublicIDs
	})
	rig = a
	rig.manifest = func(string) (sdk.Manifest, bool) { return sdk.Manifest{Name: rc.Plugin, SDK: "^0.20"}, true }
	if err := rig.grants.(*GrantStore).Set(context.Background(), rc.Plugin, []sdk.Permission{sdk.PermEntitiesRead, sdk.PermTermsRead}); err != nil {
		t.Fatal(err)
	}
	r.a, r.rc = rig, rc
	return r
}

func (r *termsITRig) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := r.db.Pool().Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (r *termsITRig) term(t *testing.T, kind, slug string, parent *string) string {
	t.Helper()
	var id string
	err := r.db.Pool().QueryRow(context.Background(), `
		INSERT INTO hatamoto_terms (kind, slug, name, parent_id) VALUES ($1, $2, $3, $4::uuid) RETURNING id::text`,
		kind, slug+"-"+r.tag, "Name "+slug, parent).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *termsITRig) entity(t *testing.T, key, status, variant string, termIDs ...string) string {
	t.Helper()
	var id string
	err := r.db.Pool().QueryRow(context.Background(), `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, locale, variant_key, attributes, published_at)
		SELECT s.id, $2, $2, $3, hatamoto_site_language(), NULLIF($4, ''), '{}'::jsonb, now()
		  FROM hatamoto_entity_schemas s WHERE s.slug = $1 RETURNING id::text`,
		r.schema, key+"-"+r.tag, status, variant).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tid := range termIDs {
		r.exec(t, `INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1, $2)`, id, tid)
	}
	return id
}

type termsPage struct {
	Items []struct {
		ID    string            `json:"id"`
		Terms *[]sdk.EntityTerm `json:"terms"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func (r *termsITRig) list(t *testing.T, args map[string]any) termsPage {
	t.Helper()
	raw, he := callAs(t, r.a, r.rc, sdk.FnEntitiesList, args)
	if he != nil {
		t.Fatal(he)
	}
	var p termsPage
	if err := jsonx.UnmarshalPinned(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// The term filter and the terms in the result obey the fetch phase's rule:
// only what an anonymous visitor sees, and only the exact term asked for.
func TestTermsFetchPhaseFilter(t *testing.T) {
	r := newTermsITRig(t)
	workshops := r.term(t, "category", "workshops", nil)
	child := r.term(t, "category", "advanced", &workshops)
	goTag := r.term(t, "tag", "go", nil)
	pub := r.entity(t, "public", "published", "", workshops, goTag)
	r.entity(t, "child-only", "published", "", child)
	r.entity(t, "draft", "draft", "", workshops)
	r.entity(t, "variant", "published", "v1", workshops)
	r.entity(t, "untagged", "published", "")

	p := r.list(t, map[string]any{"schema_slug": r.schema, "term": map[string]any{"kind": "category", "slug": "workshops-" + r.tag}})
	if len(p.Items) != 1 || p.Items[0].ID != pub {
		t.Fatalf("filtered list = %+v, want only the public entity carrying the term itself", p.Items)
	}
	got := *p.Items[0].Terms
	if len(got) != 2 || got[0] != (sdk.EntityTerm{Kind: "category", Slug: "workshops-" + r.tag, Name: "Name workshops"}) || got[1].Kind != "tag" {
		t.Errorf("terms = %+v, want category then tag", got)
	}

	p = r.list(t, map[string]any{"schema_slug": r.schema, "term": map[string]any{"kind": "category", "slug": "nope-" + r.tag}})
	if p.Items == nil || len(p.Items) != 0 || p.NextCursor != nil {
		t.Errorf("unknown term: items=%v next=%v, want [] and null", p.Items, p.NextCursor)
	}

	// Unfiltered, the page still shows terms for public entries only.
	p = r.list(t, map[string]any{"schema_slug": r.schema})
	if len(p.Items) != 3 {
		t.Errorf("unfiltered list has %d items, want the 3 public ones", len(p.Items))
	}
	for _, it := range p.Items {
		if it.Terms == nil {
			t.Errorf("item %s has no terms key", it.ID)
		}
	}

	raw, he := callAs(t, r.a, r.rc, sdk.FnEntitiesGet, map[string]string{"id": pub})
	if he != nil {
		t.Fatal(he)
	}
	var one struct{ Terms []sdk.EntityTerm }
	if err := jsonx.UnmarshalPinned(raw, &one); err != nil || len(one.Terms) != 2 {
		t.Errorf("entities.get in the fetch phase: terms=%v err=%v", one.Terms, err)
	}
}

// The terms of a page come from one statement, however many entities it has.
func TestTermsQueryCountPerPage(t *testing.T) {
	r := newTermsITRig(t)
	a, b := r.term(t, "tag", "a", nil), r.term(t, "tag", "b", nil)
	var last string
	for i := range 50 {
		last = r.entity(t, fmt.Sprintf("e%02d", i), "published", "", a, b)
	}
	termQueries := func(since int) int {
		n := 0
		for _, q := range r.counter.Since(since) {
			if strings.Contains(q, "hatamoto_entity_terms") {
				n++
			}
		}
		return n
	}
	for _, limit := range []int{1, 50} {
		before := r.counter.Count()
		p := r.list(t, map[string]any{"schema_slug": r.schema, "limit": limit})
		if len(p.Items) != limit {
			t.Fatalf("limit %d returned %d items", limit, len(p.Items))
		}
		if n := termQueries(before); n != 1 {
			t.Errorf("list of %d: %d terms statements, want 1", limit, n)
		}
	}
	before := r.counter.Count()
	if _, he := callAs(t, r.a, r.rc, sdk.FnEntitiesGet, map[string]string{"id": last}); he != nil {
		t.Fatal(he)
	}
	if n := termQueries(before); n != 1 {
		t.Errorf("get: %d terms statements, want 1", n)
	}
}
