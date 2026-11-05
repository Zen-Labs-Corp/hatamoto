//go:build integration

package taxonomy

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

type orderFixture struct {
	t     *testing.T
	ctx   context.Context
	db    *database.DB
	repo  *PostgresRepository
	svc   *Service
	kind  string
	moves []PathMove
	purge int
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 6, MinConnections: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := &orderFixture{t: t, ctx: ctx, db: db, repo: NewPostgresRepository(db), kind: "zo" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]}
	f.svc = NewService(f.repo, nil)
	f.svc.SetMoveObserver(func(_ context.Context, m []PathMove) { f.moves = append(f.moves, m...) })
	f.svc.SetChangeObserver(func(context.Context) { f.purge++ })
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE kind = $1`, f.kind) })
	return f
}

func (f *orderFixture) term(name string, parent *Term) *Term {
	f.t.Helper()
	in := CreateTermInput{Kind: f.kind, Name: name, Slug: strings.ToLower(name)}
	if parent != nil {
		in.ParentID = &parent.ID
	}
	tm, err := f.svc.CreateTerm(f.ctx, in)
	if err != nil {
		f.t.Fatal(err)
	}
	return tm
}

// names lists the kind's terms in the order the admin list and trees read them.
func (f *orderFixture) names(parent *Term) []string {
	f.t.Helper()
	res, err := f.svc.ListTerms(f.ctx, f.kind, "", 1, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, it := range res.Items {
		if (it.ParentID == nil) == (parent == nil) && (parent == nil || *it.ParentID == parent.ID) {
			out = append(out, it.Name)
		}
	}
	return out
}

func same(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestTermsStayAlphabeticalWhileUnordered(t *testing.T) {
	f := newOrderFixture(t)
	f.term("Cc", nil)
	f.term("Aa", nil)
	f.term("Bb", nil)
	if got := f.names(nil); !same(got, []string{"Aa", "Bb", "Cc"}) {
		t.Errorf("order = %v", got)
	}
}

func TestMoveTermWithinKindAndParent(t *testing.T) {
	f := newOrderFixture(t)
	a, c := f.term("Aa", nil), f.term("Cc", nil)
	f.term("Bb", nil)
	kid := f.term("Kid", a)

	res, err := f.svc.MoveTerm(f.ctx, c.ID, MoveInput{Position: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Position != 1 || res.Total != 3 || res.Term.SortOrder != 1 {
		t.Errorf("result = %d of %d (sort_order %d)", res.Position, res.Total, res.Term.SortOrder)
	}
	if got := f.names(nil); !same(got, []string{"Cc", "Aa", "Bb"}) {
		t.Errorf("roots = %v", got)
	}
	if f.purge < 2 { // create x4 plus the move: at least one notification for the move
		t.Errorf("change observer ran %d times", f.purge)
	}
	// The child set is its own sibling set and keeps its default.
	if got := f.names(a); !same(got, []string{"Kid"}) {
		t.Errorf("children = %v", got)
	}
	if k, _ := f.repo.GetTerm(f.ctx, kid.ID); k.SortOrder != 0 {
		t.Errorf("child sort_order = %d", k.SortOrder)
	}
	// A new term joins an ordered set last.
	f.term("Dd", nil)
	if got := f.names(nil); !same(got, []string{"Cc", "Aa", "Bb", "Dd"}) {
		t.Errorf("roots after create = %v", got)
	}
}

func TestMoveTermReparentRefusals(t *testing.T) {
	f := newOrderFixture(t)
	a := f.term("Aa", nil)
	kid := f.term("Kid", a)
	other, err := f.svc.CreateTerm(f.ctx, CreateTermInput{Kind: "tag", Name: "zo", Slug: "zo-" + uuid.NewString()[:6]})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, other.ID) })

	if _, err := f.svc.MoveTerm(f.ctx, a.ID, MoveInput{ParentID: &kid.ID, Position: 1}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("cycle err = %v", err)
	}
	if _, err := f.svc.MoveTerm(f.ctx, kid.ID, MoveInput{ParentID: &other.ID, Position: 1}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("foreign kind err = %v", err)
	}
	got, _ := f.repo.GetTerm(f.ctx, a.ID)
	if got.ParentID != nil {
		t.Error("a refused move changed the row")
	}
	if k, _ := f.repo.GetTerm(f.ctx, kid.ID); k.ParentID == nil || *k.ParentID != a.ID {
		t.Error("a refused move changed the child")
	}
}

func TestMoveTermReparentLandsLastAndReportsArchivePaths(t *testing.T) {
	f := newOrderFixture(t)
	a, b := f.term("Aa", nil), f.term("Bb", nil)
	f.term("K1", a)
	if _, err := f.svc.MoveTerm(f.ctx, b.ID, MoveInput{ParentID: &a.ID, Position: 0}); err != nil {
		t.Fatal(err)
	}
	if got := f.names(a); !same(got, []string{"Bb", "K1"}) && !same(got, []string{"K1", "Bb"}) {
		t.Errorf("children = %v", got)
	}
	got, _ := f.repo.GetTerm(f.ctx, b.ID)
	if got.ParentID == nil || *got.ParentID != a.ID {
		t.Fatalf("parent = %v", got.ParentID)
	}
	if len(f.moves) != 1 || !strings.HasSuffix(f.moves[0].From, "/bb") || !strings.HasSuffix(f.moves[0].To, "/aa/bb") {
		t.Errorf("archive moves = %+v", f.moves)
	}
	// And back to the root, in a given slot.
	if _, err := f.svc.MoveTerm(f.ctx, b.ID, MoveInput{ClearParent: true, Position: 1}); err != nil {
		t.Fatal(err)
	}
	if got := f.names(nil); !same(got, []string{"Bb", "Aa"}) {
		t.Errorf("roots = %v", got)
	}
}

func TestMoveTermKeepsUpdatedAtAndSlug(t *testing.T) {
	f := newOrderFixture(t)
	f.term("Aa", nil)
	b := f.term("Bb", nil)
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_terms SET updated_at = now() - interval '48 hours' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := f.repo.GetTerm(f.ctx, b.ID)
	if _, err := f.svc.MoveTerm(f.ctx, b.ID, MoveInput{Position: 1}); err != nil {
		t.Fatal(err)
	}
	after, _ := f.repo.GetTerm(f.ctx, b.ID)
	if !after.UpdatedAt.Equal(before.UpdatedAt) || after.Slug != before.Slug || after.Name != before.Name {
		t.Errorf("reorder changed the row: %+v -> %+v", before, after)
	}
}

func TestTermReadsHonourManualOrder(t *testing.T) {
	f := newOrderFixture(t)
	f.term("Aa", nil)
	b := f.term("Bb", nil)
	if _, err := f.svc.MoveTerm(f.ctx, b.ID, MoveInput{Position: 1}); err != nil {
		t.Fatal(err)
	}
	// ListTermsForEntity orders by kind, sort_order, name too: attach both to an entity.
	var schemaID, entityID uuid.UUID
	if err := f.db.Pool().QueryRow(f.ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema) VALUES ('zo', $1, '{"type":"object"}') RETURNING id`, f.kind).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	if err := f.db.Pool().QueryRow(f.ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at) VALUES ($1, 'e', 'e', 'published', now()) RETURNING id`, schemaID).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{}
	res, _ := f.svc.ListTerms(f.ctx, f.kind, "", 1, 10)
	for _, it := range res.Items {
		ids = append(ids, it.ID)
	}
	if err := f.svc.ReplaceEntityTerms(f.ctx, entityID, ids); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.ListTermsForEntity(f.ctx, entityID)
	if err != nil || len(got) != 2 || got[0].Name != "Bb" || got[1].Name != "Aa" {
		t.Errorf("entity terms = %+v (%v)", got, err)
	}
	// The public list: most used first by default, the manual order on request.
	for manual, want := range map[bool]string{true: "Bb", false: "Aa"} {
		pub, err := f.repo.ListPublicTerms(f.ctx, f.kind, 10, manual)
		if err != nil || len(pub) != 2 || pub[0].Name != want {
			t.Errorf("ListPublicTerms(manual=%v) first = %v (%v), want %s", manual, pub, err, want)
		}
	}
}
