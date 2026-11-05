//go:build integration

package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/sdk"
)

// csRig is a run in its transform phase over a real changeset engine.
type csRig struct {
	t      *testing.T
	a      *API
	rc     RunCtx
	eng    *changeset.Engine
	runs   *plugrun.Store
	schema string
	tag    string
	live   map[string]bool
	cs     int64
}

func newCSRig(t *testing.T) *csRig {
	t.Helper()
	r := &csRig{t: t, tag: strings.ReplaceAll(uuid.NewString(), "-", "")[:8], live: map[string]bool{}}
	var store *plugrun.Store
	a, rc, store := runRig(t, func(_ *plugrun.Run, s *Services) {
		db := s.Runs.DB
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		schemas := entity.NewPostgresSchemaRepository(db)
		sc, err := schemas.Create(context.Background(), &entity.CreateSchemaInput{Name: "csg " + r.tag, Slug: "csg-" + r.tag,
			Schema: jsontext.Value(`{"type":"object","properties":{"content":{"type":"string"}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		r.schema = sc.Slug
		ents := entity.NewService(schemas, entity.NewPostgresEntityRepository(db), log)
		r.eng = &changeset.Engine{DB: db, Logger: log, Writers: map[changeset.Kind]changeset.KindWriter{
			changeset.KindEntity:   changeset.EntityWriter{Svc: ents},
			changeset.KindTerm:     changeset.TermWriter{Svc: taxonomy.NewService(taxonomy.NewPostgresRepository(db), log)},
			changeset.KindMenu:     changeset.MenuWriter{Svc: menu.NewService(menu.NewPostgresRepository(db), log)},
			changeset.KindRedirect: changeset.RedirectWriter{Svc: redirect.NewService(db, log)},
		}}
		s.Changesets = r.eng
		s.Entities = ents
		s.LivePath = func(_ context.Context, p string) bool { return r.live[p] }
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
			_ = schemas.Delete(bg, sc.ID)
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_terms WHERE slug LIKE $1`, r.tag+"%")
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_menus WHERE slug LIKE $1`, r.tag+"%")
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "/"+r.tag+"%")
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_changeset_source_keys WHERE source_key LIKE $1`, r.tag+"%")
		})
	})
	r.a, r.rc, r.runs = a, rc, store
	r.rc.Phase = sdk.RunPhaseTransform
	r.rc.Proposes = []string{sdk.ProposeSiteSettings}
	r.newChangeset()
	return r
}

// newChangeset opens a changeset for the run, as the driver does at start.
func (r *csRig) newChangeset() {
	r.t.Helper()
	cs, err := r.eng.Begin(context.Background(), r.rc.RunID, r.rc.Plugin)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.runs.SetChangeset(context.Background(), r.rc.RunID, cs); err != nil {
		r.t.Fatal(err)
	}
	r.cs = cs
}

func (r *csRig) call(fn string, args any) (jsontext.Value, *sdk.HostError) {
	r.t.Helper()
	return callAs(r.t, r.a, r.rc, fn, args)
}

func (r *csRig) mustCall(fn string, args any) jsontext.Value {
	r.t.Helper()
	res, he := r.call(fn, args)
	if he != nil {
		r.t.Fatalf("%s: %v", fn, he)
	}
	return res
}

func (r *csRig) key(k string) string { return r.tag + "-" + k }

func (r *csRig) count(q string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.runs.DB.Pool().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *csRig) entities() int {
	return r.count(`SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id WHERE s.slug = $1`, r.schema)
}

func (r *csRig) publish() changeset.Result {
	r.t.Helper()
	res, err := r.eng.Publish(context.Background(), r.cs)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

// Items staged by the functions publish together; nothing is live before.
func TestChangesetFnsStageAndPublish(t *testing.T) {
	r := newCSRig(t)
	res := r.mustCall(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("p"), Schema: r.schema, Slug: r.key("p"), Name: "Parent",
		Attributes: jsontext.Value(`{"content":"x"}`), SEO: &sdk.SEO{MetaTitle: "T"}})
	var up sdk.UpsertResult
	_ = jsonx.UnmarshalPinned(res, &up)
	if up.SourceKey != r.key("p") || !up.Created {
		t.Fatalf("upsert result %+v", up)
	}
	r.mustCall(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("c"), Schema: r.schema, Slug: r.key("c"), Name: "Child", ParentKey: r.key("p")})
	r.mustCall(sdk.FnChangesetTermUpsert, sdk.TermUpsert{SourceKey: r.key("t"), Kind: "tag", Slug: r.key("t"), Name: "Tag"})
	r.mustCall(sdk.FnChangesetMenuUpsert, sdk.MenuUpsert{SourceKey: r.key("m"), Slug: r.key("m"), Name: "Menu",
		Items: []sdk.MenuItem{{Label: "Parent", EntityKey: r.key("p"), Children: []sdk.MenuItem{{Label: "Out", URL: "https://example.com/"}}}}})
	r.mustCall(sdk.FnChangesetRedirectUpsert, sdk.RedirectUpsert{SourceKey: r.key("r"), From: "/" + r.tag + "-old", To: "/" + r.key("p")})

	if n := r.entities(); n != 0 {
		t.Fatalf("%d entities live before publish", n)
	}
	if n := r.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1`, r.key("t")); n != 0 {
		t.Fatal("term live before publish")
	}
	if got := r.publish(); got.Applied != 5 || got.Skipped != 0 {
		t.Fatalf("publish %+v", got)
	}
	if n := r.entities(); n != 2 {
		t.Fatalf("%d entities after publish", n)
	}
	if n := r.count(`SELECT count(*) FROM hatamoto_entities WHERE slug = $1 AND parent_id = (SELECT id FROM hatamoto_entities WHERE slug = $2)`, r.key("c"), r.key("p")); n != 1 {
		t.Fatal("child is not under its parent")
	}
	if n := r.count(`SELECT count(*) FROM hatamoto_entities WHERE slug = $1 AND origin = 'plugin:' || $2`, r.key("p"), r.rc.Plugin); n != 1 {
		t.Fatal("entity origin is not the plugin")
	}
	if n := r.count(`SELECT count(*) FROM hatamoto_menu_items i JOIN hatamoto_menus m ON m.id = i.menu_id WHERE m.slug = $1 AND i.entity_id IS NOT NULL`, r.key("m")); n != 1 {
		t.Fatal("menu item does not link the entity")
	}

	// A later changeset of the same run deletes what the first one published.
	r.newChangeset()
	r.mustCall(sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{Kind: "redirect", SourceKey: r.key("r")})
	r.mustCall(sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{Kind: "term", SourceKey: r.key("t")})
	if _, he := r.call(sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{Kind: "term", SourceKey: r.key("never")}); he == nil || he.Code != sdk.ErrNotFound {
		t.Fatalf("delete of an unknown key: %v", he)
	}
	if got := r.publish(); got.Applied != 2 {
		t.Fatalf("delete publish %+v", got)
	}
	if n := r.count(`SELECT count(*) FROM hatamoto_terms WHERE slug = $1`, r.key("t")); n != 0 {
		t.Fatal("term survived its delete")
	}
	// The key now maps to nothing, so the next upsert creates again.
	r.newChangeset()
	var again sdk.UpsertResult
	_ = jsonx.UnmarshalPinned(r.mustCall(sdk.FnChangesetTermUpsert, sdk.TermUpsert{SourceKey: r.key("t"), Kind: "tag", Slug: r.key("t"), Name: "Tag"}), &again)
	if !again.Created {
		t.Fatalf("upsert after delete: %+v", again)
	}
}

func TestChangesetFnsParentKeyMustBeKnown(t *testing.T) {
	r := newCSRig(t)
	_, he := r.call(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("c"), Schema: r.schema, Name: "C", ParentKey: r.key("ghost")})
	if he == nil || he.Code != sdk.ErrInvalidArgument || !strings.Contains(he.Message, "stage parents before their children") {
		t.Fatalf("unknown parent: %v", he)
	}
	r.mustCall(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("p"), Schema: r.schema, Name: "P"})
	r.mustCall(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("c"), Schema: r.schema, Name: "C", ParentKey: r.key("p")})
	_, he = r.call(sdk.FnChangesetMenuUpsert, sdk.MenuUpsert{SourceKey: r.key("m"), Slug: r.key("m"), Name: "M", Items: []sdk.MenuItem{{Label: "X", EntityKey: r.key("ghost")}}})
	if he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("menu with an unknown entity key: %v", he)
	}
}

// ISC-16: an object whose origin is the owner is never reached, even through
// a source key that maps to it.
func TestChangesetFnsNotOwned(t *testing.T) {
	r := newCSRig(t)
	ctx := context.Background()
	// The owner makes an entity through an owner changeset.
	ownerCS, err := r.eng.Begin(ctx, uuid.New(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.eng.Stage(ctx, ownerCS, changeset.Item{Kind: changeset.KindEntity, Op: changeset.OpUpsert,
		Payload: jsontext.Value(`{"schema":"` + r.schema + `","name":"Mine","slug":"` + r.key("mine") + `","attributes":{}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.eng.Publish(ctx, ownerCS); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := r.runs.DB.Pool().QueryRow(ctx, `SELECT id::text FROM hatamoto_entities WHERE slug = $1`, r.key("mine")).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// A plugin key that maps to it (as a hand-over or a stale mapping would).
	if _, err := r.runs.DB.Pool().Exec(ctx, `INSERT INTO hatamoto_changeset_source_keys (origin, kind, source_key, target_id) VALUES ($1, 'entity', $2, $3)`,
		changeset.Origin(r.rc.Plugin), r.key("mine"), id); err != nil {
		t.Fatal(err)
	}
	_, he := r.call(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("mine"), Schema: r.schema, Name: "Taken over"})
	if he == nil || he.Code != sdk.ErrNotOwned || he.Message != "object belongs to the owner" {
		t.Fatalf("upsert of an owner object: %v", he)
	}
	_, he = r.call(sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{Kind: "entity", SourceKey: r.key("mine")})
	if he == nil || he.Code != sdk.ErrNotOwned {
		t.Fatalf("delete of an owner object: %v", he)
	}
}

func TestChangesetFnsRedirectRefusesLivePath(t *testing.T) {
	r := newCSRig(t)
	r.live["/"+r.tag+"-page"] = true
	_, he := r.call(sdk.FnChangesetRedirectUpsert, sdk.RedirectUpsert{SourceKey: r.key("r"), From: "/" + r.tag + "-page", To: "/x"})
	if he == nil || he.Code != sdk.ErrInvalidArgument || !strings.Contains(he.Message, "live content") {
		t.Fatalf("redirect from a live path: %v", he)
	}
	r.mustCall(sdk.FnChangesetRedirectUpsert, sdk.RedirectUpsert{SourceKey: r.key("r"), From: "/" + r.tag + "-gone", To: "/x", StatusCode: 302})
}

func TestChangesetFnsPropose(t *testing.T) {
	r := newCSRig(t)
	r.mustCall(sdk.FnChangesetPropose, sdk.Proposal{Kind: sdk.ProposeSiteSettings, Payload: jsontext.Value(`{"title":"New","time_zone":"Europe/Berlin"}`)})
	got, err := r.runs.Proposals(context.Background(), r.rc.RunID)
	if err != nil || len(got) != 1 || got[0].Kind != sdk.ProposeSiteSettings || !strings.Contains(string(got[0].Payload), "New") {
		t.Fatalf("stored proposals: %+v %v", got, err)
	}
	// The singleton changes only ever go through proposals.
	if n := r.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, r.cs); n != 0 {
		t.Fatalf("a proposal became a changeset item (%d)", n)
	}
	if _, he := r.call(sdk.FnChangesetPropose, sdk.Proposal{Kind: sdk.ProposeURLPatterns, Payload: jsontext.Value(`{}`)}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("undeclared kind: %v", he)
	}
}

// Inside a run the transform phase reads published entities only: a draft
// answers not_found by id and is absent from lists, and a list cannot ask for
// drafts.
func TestRunEntityReadsPublishedOnly(t *testing.T) {
	r := newCSRig(t)
	ctx := context.Background()
	if err := r.a.grants.(*GrantStore).Set(ctx, r.rc.Plugin, []string{sdk.PermEntitiesRead}); err != nil {
		t.Fatal(err)
	}
	r.mustCall(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("pub"), Schema: r.schema, Slug: r.key("pub"), Name: "Pub"})
	r.mustCall(sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: r.key("dr"), Schema: r.schema, Slug: r.key("dr"), Name: "Draft"})
	r.publish()
	if _, err := r.runs.DB.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = 'published' WHERE slug = $1`, r.key("pub")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runs.DB.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = 'draft' WHERE slug = $1`, r.key("dr")); err != nil {
		t.Fatal(err)
	}
	var pubID, draftID string
	_ = r.runs.DB.Pool().QueryRow(ctx, `SELECT id::text FROM hatamoto_entities WHERE slug = $1`, r.key("pub")).Scan(&pubID)
	_ = r.runs.DB.Pool().QueryRow(ctx, `SELECT id::text FROM hatamoto_entities WHERE slug = $1`, r.key("dr")).Scan(&draftID)

	r.mustCall(sdk.FnEntitiesGet, map[string]string{"id": pubID})
	if _, he := r.call(sdk.FnEntitiesGet, map[string]string{"id": draftID}); he == nil || he.Code != sdk.ErrNotFound {
		t.Fatalf("draft by id: %v", he)
	}
	if _, he := r.call(sdk.FnEntitiesList, map[string]string{"schema_slug": r.schema, "status": "draft"}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("draft list: %v", he)
	}
	var list struct {
		Items []struct{ ID string } `json:"items"`
	}
	if err := jsonx.UnmarshalPinned(r.mustCall(sdk.FnEntitiesList, map[string]string{"schema_slug": r.schema}), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != pubID {
		t.Fatalf("list in a run: %+v", list.Items)
	}
}
