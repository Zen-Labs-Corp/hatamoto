//go:build integration

package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
)

func (r *nestedRig) update(id uuid.UUID, in entity.UpdateEntityInput) error {
	r.t.Helper()
	_, err := r.svc.UpdateEntity(r.ctx, id, nil, &in)
	return err
}

// settings replaces a rig schema's settings through the service.
func (r *nestedRig) setSettings(schema uuid.UUID, settings string) error {
	raw := json.RawMessage(settings)
	_, err := r.svc.UpdateSchema(r.ctx, schema, &entity.UpdateSchemaInput{Settings: &raw})
	return err
}

func (r *nestedRig) note(slug string, parent *entity.Entity) (*entity.Entity, error) {
	in := &entity.CreateEntityInput{SchemaID: r.noteSch, Name: slug, Slug: slug, Attributes: json.RawMessage(`{}`)}
	if parent != nil {
		in.ParentID = &parent.ID
	}
	return r.svc.CreateEntity(r.ctx, nil, in)
}

// Slug uniqueness follows the schema's pattern: a flat pattern refuses one slug
// under two parents (the second row would be unreachable), {path} allows it,
// and switching a schema's pattern with conflicting data fails with a clear error.
func TestSlugScopeFollowsPattern_Integration(t *testing.T) {
	r := newNestedRig(t)

	// /notes/{slug}: flat.
	p1, err := r.note("p1", nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := r.note("p2", nil)
	if err != nil {
		t.Fatal(err)
	}
	dupA, err := r.note("dup", p1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.note("dup", p2)
	if !errors.Is(err, entity.ErrSlugAlreadyExists) || !strings.Contains(err.Error(), "across the whole schema") {
		t.Fatalf("flat schema, one slug under two parents: %v", err)
	}
	// Moving another entity onto the slug is refused the same way.
	other, err := r.note("other", p2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.update(other.ID, entity.UpdateEntityInput{Slug: ptr("dup")}); !errors.Is(err, entity.ErrSlugAlreadyExists) {
		t.Errorf("rename onto a taken slug: %v", err)
	}
	_ = dupA

	// {path}: siblings only.
	a, b := r.page("a", nil), r.page("b", nil)
	r.page("dup", a)
	r.page("dup", b)
	_, err = r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: "x", Slug: "dup", ParentID: &a.ID, Attributes: json.RawMessage(`{}`)})
	if !errors.Is(err, entity.ErrSlugAlreadyExists) || !strings.Contains(err.Error(), "same parent") {
		t.Errorf("sibling duplicate: %v", err)
	}

	// Switching the {path} schema to a flat pattern while two "dup" rows exist.
	err = r.setSettings(r.pageSch, `{"url_pattern":"/pages/{slug}"}`)
	if !errors.Is(err, entity.ErrInvalidSchema) || !strings.Contains(err.Error(), "dup") {
		t.Fatalf("pattern change with conflicting slugs: %v", err)
	}
	r.expect("/a/dup", 200) // nothing changed
	// After renaming one, the change goes through and the flat rule applies.
	var second uuid.UUID
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id = $1 AND slug = 'dup' ORDER BY created_at DESC LIMIT 1`, r.pageSch).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if err := r.update(second, entity.UpdateEntityInput{Slug: ptr("dup2")}); err != nil {
		t.Fatal(err)
	}
	if err := r.setSettings(r.pageSch, `{"url_pattern":"/pages/{slug}"}`); err != nil {
		t.Fatalf("pattern change without conflicts: %v", err)
	}
	_, err = r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: "x", Slug: "dup", ParentID: &b.ID, Attributes: json.RawMessage(`{}`)})
	if !errors.Is(err, entity.ErrSlugAlreadyExists) || !strings.Contains(err.Error(), "across the whole schema") {
		t.Errorf("flat rule after the switch: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// Names with no ASCII letters are saved, updated and imported: they get a hash
// slug instead of failing the request.
func TestNonASCIIBylines_Integration(t *testing.T) {
	r := newNestedRig(t)
	for _, name := range []string{"李明", "Ελένη"} {
		attrs := json.RawMessage(`{"author":"` + name + `"}`)
		e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.noteSch, Name: "n-" + name, Slug: "byline-" + uuid.NewString()[:8], Attributes: attrs})
		if err != nil {
			t.Fatalf("create with byline %q: %v", name, err)
		}
		if e.AuthorID == nil {
			t.Fatalf("%q: no author id", name)
		}
		a, err := r.srv.authors.Get(r.ctx, *e.AuthorID)
		if err != nil || a.Slug != author.Slugify(name) || !strings.HasPrefix(a.Slug, "author-") || a.Name != name {
			t.Fatalf("%q: author %+v %v", name, a, err)
		}
		// An update that sends attributes again resolves the same author.
		title := "x"
		if _, err := r.svc.UpdateEntity(r.ctx, e.ID, nil, &entity.UpdateEntityInput{Name: &title, Attributes: &attrs}); err != nil {
			t.Fatalf("update with byline %q: %v", name, err)
		}
		res, err := r.svc.Import(r.ctx, nil, entity.ImportBatch{Source: "it", Items: []entity.ImportItem{{
			Key: "byline-" + name, SchemaSlug: "note", Name: "I", Slug: "imp-" + uuid.NewString()[:8], Status: entity.StatusPublished,
			Attributes: json.RawMessage(`{}`), AuthorName: name,
		}}})
		if err != nil || res.Failed != 0 {
			t.Fatalf("import with author %q: %v %+v", name, err, res)
		}
	}
	// A migrated and a saved author agree: one row per name.
	var n int
	_ = r.db.Pool().QueryRow(r.ctx, `SELECT count(*) FROM hatamoto_authors`).Scan(&n)
	if n != 2 {
		t.Errorf("authors = %d, want 2", n)
	}
}

// A rename leaves an automatic redirect; a new page at the old path wins over
// it, an owner's redirect still wins over the page.
func TestAutoRedirectYieldsToNewPage_Integration(t *testing.T) {
	r := newNestedRig(t)
	about := r.page("about", nil)
	if err := r.update(about.ID, entity.UpdateEntityInput{Slug: ptr("about-us")}); err != nil {
		t.Fatal(err)
	}
	r.expect("/about", 301)
	r.expect("/about-us", 200)

	r.page("about", nil) // a new page takes the old path
	r.expect("/about", 200)
	if got := r.redirectTarget("/about"); got != "" {
		t.Errorf("the auto redirect still answers: %q", got)
	}
	r.expect("/about-us", 200)

	// Renaming again re-creates the redirect for the then-live /about only if
	// the page moved; an owner's rule at a page's path keeps precedence.
	t.Cleanup(func() {
		_, _ = r.db.Pool().Exec(r.ctx, `DELETE FROM hatamoto_redirects WHERE source_path = '/pricing'`)
	})
	if _, err := r.srv.redirectService.CreateRedirect(r.ctx, &redirect.CreateRedirectInput{SourcePath: "/pricing", TargetURL: "/plans", StatusCode: 301}); err != nil {
		t.Fatal(err)
	}
	r.page("pricing", nil)
	r.expect("/pricing", 301)

	// The same holds for a page that is published later (draft first) and for
	// a scheduled publish going through the worker sink.
	old := r.page("old", nil)
	if err := r.update(old.ID, entity.UpdateEntityInput{Slug: ptr("older")}); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: "old", Slug: "old", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.expect("/old", 301) // still a draft: the redirect holds
	r.exec(`UPDATE hatamoto_entities SET status = 'published' WHERE id = $1`, fresh.ID)
	scheduledPublishSink{s: r.srv, next: noopPublished{}}.EntityPublished(r.ctx, fresh.ID, "nested-page", "old", "old", 1)
	r.expect("/old", 200)
}

type noopPublished struct{}

func (noopPublished) EntityPublished(context.Context, uuid.UUID, string, string, string, int) {}

// Renaming a draft parent re-paths its published children, with redirects.
func TestDraftParentRenameRedirects_Integration(t *testing.T) {
	r := newNestedRig(t)
	au, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.pageSch, Name: "north", Slug: "north", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	r.page("services", au)
	r.expect("/north/services", 200)
	if err := r.update(au.ID, entity.UpdateEntityInput{Slug: ptr("northland")}); err != nil {
		t.Fatal(err)
	}
	r.expect("/northland/services", 200)
	r.expect("/north/services", 301)
	if got := r.redirectTarget("/north/services"); got != "/northland/services" {
		t.Errorf("redirect = %q", got)
	}
	r.expect("/north", 404) // the draft itself has no URL and gets no redirect
}

// A move cannot push the moved subtree below the depth cap, and a parent with
// children cannot be deleted.
func TestHierarchyGuards_Integration(t *testing.T) {
	r := newNestedRig(t)
	chain := func(prefix string, n int) []*entity.Entity {
		var out []*entity.Entity
		var parent *entity.Entity
		for i := 1; i <= n; i++ {
			parent = r.page(prefix+string(rune('0'+i)), parent)
			out = append(out, parent)
		}
		return out
	}
	a, b := chain("a", 5), chain("b", 5)
	if err := r.update(b[0].ID, entity.UpdateEntityInput{ParentID: &a[4].ID}); !errors.Is(err, entity.ErrParentTooDeep) {
		t.Fatalf("moving a 5-level subtree under depth 5: %v", err)
	}
	r.expect("/b1/b2/b3/b4/b5", 200) // nothing moved
	// A subtree that fits still moves.
	if err := r.update(b[4].ID, entity.UpdateEntityInput{ParentID: &a[2].ID}); err != nil {
		t.Fatalf("moving a leaf: %v", err)
	}

	if err := r.svc.DeleteEntity(r.ctx, a[0].ID); !errors.Is(err, entity.ErrHasChildren) {
		t.Fatalf("delete a parent: %v", err)
	}
	if err := r.svc.DeleteEntity(r.ctx, b[4].ID); err != nil {
		t.Fatalf("delete a leaf: %v", err)
	}
}

// Renaming or re-parenting a term redirects its old archive path.
func TestTermMoveRedirects_Integration(t *testing.T) {
	r := newNestedRig(t)
	tsvc := taxonomy.NewService(taxonomy.NewPostgresRepository(r.db), nil)
	tsvc.SetMoveObserver(r.srv.onTermsMoved)
	mk := func(name string, parent *taxonomy.Term) *taxonomy.Term {
		in := taxonomy.CreateTermInput{Kind: "genre", Name: name}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		term, err := tsvc.CreateTerm(r.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return term
	}
	world, news, sport := mk("World", nil), mk("News", nil), mk("Sport", nil)
	local := mk("Local", news)
	if _, err := tsvc.UpdateTerm(r.ctx, taxonomy.UpdateTermInput{ID: news.ID, ParentID: &world.ID}); err != nil {
		t.Fatal(err)
	}
	for old, want := range map[string]string{"/genre/news": "/genre/world/news", "/genre/news/local": "/genre/world/news/local"} {
		if got := r.redirectTarget(old); got != want {
			t.Errorf("redirect %s -> %q, want %q", old, got, want)
		}
	}
	// A rename moves the term and everything below it too.
	if _, err := tsvc.UpdateTerm(r.ctx, taxonomy.UpdateTermInput{ID: news.ID, Name: "Headlines", Slug: "headlines"}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/genre/world/news/local"); got != "/genre/world/headlines/local" {
		t.Errorf("after rename: %q", got)
	}
	if got := r.redirectTarget("/genre/news"); got != "/genre/world/headlines" {
		t.Errorf("no chain expected: %q", got)
	}
	// A name-only edit moves nothing.
	if _, err := tsvc.UpdateTerm(r.ctx, taxonomy.UpdateTermInput{ID: sport.ID, Name: "Sports", Slug: "sport"}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/genre/sport"); got != "" {
		t.Errorf("unexpected redirect %q", got)
	}
	_ = local
}

// Renaming an author without a slug keeps /author/{slug}.
func TestAuthorRenameKeepsSlug_Integration(t *testing.T) {
	r := newNestedRig(t)
	a, err := r.srv.authors.Create(r.ctx, author.Input{Name: "Jane Guest"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.srv.authors.Update(r.ctx, a.ID, author.Input{Name: "Jane Q. Guest"})
	if err != nil || got.Slug != "jane-guest" || got.Name != "Jane Q. Guest" {
		t.Fatalf("rename without slug: %+v %v", got, err)
	}
	got, err = r.srv.authors.Update(r.ctx, a.ID, author.Input{Name: "Jane Q. Guest", Slug: "jane-q"})
	if err != nil || got.Slug != "jane-q" {
		t.Fatalf("explicit slug: %+v %v", got, err)
	}
}

// A time zone change reaches the URL registry at once.
func TestTimezoneChangeRefreshesURLRegistry_Integration(t *testing.T) {
	r := newNestedRig(t)
	zone := "UTC"
	r.svc.URLRegistry().SetTimezone(func(context.Context) string { return zone })
	snap, err := r.svc.URLRegistry().Snapshot(r.ctx)
	if err != nil || snap.Location().String() != "UTC" {
		t.Fatalf("snapshot zone = %v %v", snap.Location(), err)
	}
	zone = "Australia/Sydney"
	if snap, _ = r.svc.URLRegistry().Snapshot(r.ctx); snap.Location().String() != "UTC" {
		t.Fatal("the registry should still serve its cached snapshot")
	}
	r.srv.invalidateSiteSettings(r.ctx)
	if snap, _ = r.svc.URLRegistry().Snapshot(r.ctx); snap.Location().String() != "Australia/Sydney" {
		t.Errorf("zone after settings save = %v", snap.Location())
	}
}
