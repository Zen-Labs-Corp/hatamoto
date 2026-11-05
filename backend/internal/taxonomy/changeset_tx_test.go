package taxonomy

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func TestSuffixedSlugFitsColumn(t *testing.T) {
	long := strings.Repeat("a", MaxSlugLen)
	for n := 1; n <= 120; n++ {
		got := suffixedSlug(long, n)
		if len(got) > MaxSlugLen || !ValidSlug(got) {
			t.Fatalf("n=%d: %q (len %d)", n, got, len(got))
		}
	}
	if got := suffixedSlug("news", 2); got != "news-2" {
		t.Fatalf("got %q", got)
	}
	// A cut that lands on a hyphen drops it.
	if got := suffixedSlug(strings.Repeat("a", MaxSlugLen-3)+"-bb", 2); !ValidSlug(got) {
		t.Fatalf("invalid %q", got)
	}
}

type txEnv struct {
	db  *database.DB
	ctx context.Context
}

func newTxEnv(t *testing.T) *txEnv {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return &txEnv{db: db, ctx: ctx}
}

// run applies ops in one transaction and commits.
func (e *txEnv) run(t *testing.T, ops ...TermOp) []TermResult {
	t.Helper()
	tx, err := e.db.Pool().Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx) //nolint:errcheck
	var out []TermResult
	for _, op := range ops {
		r, err := ApplyChangesetTx(e.ctx, tx, op)
		if err != nil {
			t.Fatalf("apply %+v: %v", op, err)
		}
		out = append(out, r)
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *txEnv) apply(t *testing.T, op TermOp) (TermResult, error) {
	t.Helper()
	tx, err := e.db.Pool().Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx) //nolint:errcheck
	r, err := ApplyChangesetTx(e.ctx, tx, op)
	if err != nil {
		return r, err
	}
	return r, tx.Commit(e.ctx)
}

func (e *txEnv) cleanup(t *testing.T, ids ...uuid.UUID) {
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = e.db.Pool().Exec(e.ctx, `DELETE FROM hatamoto_terms WHERE id = $1`, id)
		}
	})
}

func TestApplyChangesetTxCreate(t *testing.T) {
	e := newTxEnv(t)
	slug := "cs-" + uuid.NewString()[:8]
	r := e.run(t, TermOp{Kind: "tag", Name: " Hello ", Slug: slug, Description: "d", Origin: "plug", SEO: jsontext.Value(`{"meta_title":"T"}`)})[0]
	e.cleanup(t, r.ID)
	if !r.Created || r.Slug != slug || r.Before != nil {
		t.Fatalf("result %+v", r)
	}
	var name, origin string
	var seo []byte
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT name, origin, seo FROM hatamoto_terms WHERE id = $1`, r.ID).Scan(&name, &origin, &seo); err != nil {
		t.Fatal(err)
	}
	if name != "Hello" || origin != "plug" || !strings.Contains(string(seo), "meta_title") {
		t.Fatalf("stored name=%q origin=%q seo=%s", name, origin, seo)
	}
	// Default origin is owner.
	r2 := e.run(t, TermOp{Kind: "tag", Name: "Other", Slug: slug + "-o"})[0]
	e.cleanup(t, r2.ID)
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT origin FROM hatamoto_terms WHERE id = $1`, r2.ID).Scan(&origin); err != nil || origin != "owner" {
		t.Fatalf("origin %q %v", origin, err)
	}
}

func TestApplyChangesetTxValidation(t *testing.T) {
	e := newTxEnv(t)
	for name, tc := range map[string]struct {
		op   TermOp
		want error
	}{
		"kind":        {TermOp{Kind: "Bad Kind", Name: "x"}, ErrInvalidKind},
		"reserved":    {TermOp{Kind: "admin", Name: "x"}, ErrInvalidKind},
		"name":        {TermOp{Kind: "tag", Name: " "}, ErrInvalidName},
		"slug":        {TermOp{Kind: "tag", Name: "x", Slug: "Not Valid!"}, ErrInvalidSlug},
		"description": {TermOp{Kind: "tag", Name: "x", Description: strings.Repeat("d", MaxDescriptionLen+1)}, ErrInvalidDescription},
		"seo":         {TermOp{Kind: "tag", Name: "x", SEO: jsontext.Value(`{"meta_title":"` + strings.Repeat("t", 500) + `"}`)}, nil},
		"parent":      {TermOp{Kind: "tag", Name: "x", ParentID: ptr(uuid.New())}, ErrTermParentNotFound},
		"locale":      {TermOp{Kind: "tag", Name: "x", Locale: "und"}, ErrTermGroupInvalid},
	} {
		_, err := e.apply(t, tc.op)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: err %v, want %v", name, err, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestApplyChangesetTxSlugSuffix(t *testing.T) {
	e := newTxEnv(t)
	slug := "cs-" + uuid.NewString()[:8]
	first := e.run(t, TermOp{Kind: "tag", Name: "A", Slug: slug})[0]
	// Both clash in one transaction: the second one is probed past the first.
	rs := e.run(t, TermOp{Kind: "tag", Name: "B", Slug: slug}, TermOp{Kind: "tag", Name: "C", Slug: slug})
	e.cleanup(t, first.ID, rs[0].ID, rs[1].ID)
	if rs[0].Slug != slug+"-2" || rs[1].Slug != slug+"-3" {
		t.Fatalf("slugs %q %q", rs[0].Slug, rs[1].Slug)
	}
	// Another kind may reuse the slug.
	other := e.run(t, TermOp{Kind: "category", Name: "D", Slug: slug})[0]
	e.cleanup(t, other.ID)
	if other.Slug != slug {
		t.Fatalf("category slug %q", other.Slug)
	}
	// A full-length slug stays inside the column.
	long := strings.Repeat("z", MaxSlugLen)
	l1 := e.run(t, TermOp{Kind: "tag", Name: "L1", Slug: long})[0]
	l2 := e.run(t, TermOp{Kind: "tag", Name: "L2", Slug: long})[0]
	e.cleanup(t, l1.ID, l2.ID)
	if len(l2.Slug) > MaxSlugLen || l2.Slug == long || !strings.HasSuffix(l2.Slug, "-2") {
		t.Fatalf("long slug %q", l2.Slug)
	}
	// Renaming onto a taken slug is suffixed too, and a no-op rename is not.
	up := e.run(t, TermOp{ID: &rs[0].ID, Name: "B", Slug: slug})[0]
	if up.Slug != slug+"-2" {
		t.Fatalf("self rename %q", up.Slug)
	}
	up = e.run(t, TermOp{ID: &rs[0].ID, Name: "B", Slug: slug + "-9"})[0]
	if up.Slug != slug+"-9" {
		t.Fatalf("rename %q", up.Slug)
	}
}

func TestApplyChangesetTxConflict(t *testing.T) {
	e := newTxEnv(t)
	slug := "cs-" + uuid.NewString()[:8]
	r := e.run(t, TermOp{Kind: "tag", Name: "A", Slug: slug})[0]
	e.cleanup(t, r.ID)

	// The base token the result gave matches.
	base := r.UpdatedAt
	up := e.run(t, TermOp{ID: &r.ID, Name: "A2", BaseUpdatedAt: &base})[0]
	if up.Slug != slug || up.UpdatedAt.Equal(base) {
		t.Fatalf("update %+v", up)
	}
	// The old token is now stale, even though only an unrelated column moved.
	res, err := e.apply(t, TermOp{ID: &r.ID, Name: "A3", BaseUpdatedAt: &base})
	if !errors.Is(err, ErrTermConflict) || !res.Conflict {
		t.Fatalf("got %+v, %v", res, err)
	}
	if _, err := e.apply(t, TermOp{ID: &r.ID, Delete: true, BaseUpdatedAt: &base}); !errors.Is(err, ErrTermConflict) {
		t.Fatalf("delete: %v", err)
	}
	var name string
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT name FROM hatamoto_terms WHERE id = $1`, r.ID).Scan(&name); err != nil || name != "A2" {
		t.Fatalf("name %q %v", name, err)
	}
	missing := uuid.New()
	if _, err := e.apply(t, TermOp{ID: &missing, Name: "x"}); !errors.Is(err, ErrTermNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// A failing op leaves the surrounding transaction usable and its earlier
// writes intact.
func TestApplyChangesetTxSavepoint(t *testing.T) {
	e := newTxEnv(t)
	slug := "cs-" + uuid.NewString()[:8]
	tx, err := e.db.Pool().Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx) //nolint:errcheck
	ok, err := ApplyChangesetTx(e.ctx, tx, TermOp{Kind: "tag", Name: "A", Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	e.cleanup(t, ok.ID)
	if _, err := ApplyChangesetTx(e.ctx, tx, TermOp{Kind: "tag", Name: "B", ParentID: ptr(uuid.New())}); !errors.Is(err, ErrTermParentNotFound) {
		t.Fatalf("got %v", err)
	}
	var n int
	if err := tx.QueryRow(e.ctx, `SELECT count(*) FROM hatamoto_terms WHERE id = $1`, ok.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tx unusable or lost write: %d %v", n, err)
	}
	_ = tx.Rollback(e.ctx)
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT count(*) FROM hatamoto_terms WHERE id = $1`, ok.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled back write is visible: %d %v", n, err)
	}
}

func TestApplyChangesetTxParents(t *testing.T) {
	e := newTxEnv(t)
	p := "cs-" + uuid.NewString()[:8]
	root := e.run(t, TermOp{Kind: "category", Name: "R", Slug: p + "-r"})[0]
	child := e.run(t, TermOp{Kind: "category", Name: "C", Slug: p + "-c", ParentID: &root.ID})[0]
	tag := e.run(t, TermOp{Kind: "tag", Name: "T", Slug: p + "-t"})[0]
	e.cleanup(t, child.ID, root.ID, tag.ID)

	if _, err := e.apply(t, TermOp{Kind: "tag", Name: "X", ParentID: &root.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Fatalf("other kind parent: %v", err)
	}
	if _, err := e.apply(t, TermOp{ID: &root.ID, ParentID: &child.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := e.apply(t, TermOp{ID: &root.ID, ParentID: &root.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Fatalf("self parent: %v", err)
	}
	// Depth: build a chain of MaxDepth terms (root, child, ...).
	tip := child
	var ids []uuid.UUID
	for i := 2; i < MaxDepth; i++ {
		tip = e.run(t, TermOp{Kind: "category", Name: "D", Slug: p + "-d" + string(rune('a'+i)), ParentID: &tip.ID})[0]
		ids = append(ids, tip.ID)
	}
	e.cleanup(t, ids...)
	if _, err := e.apply(t, TermOp{Kind: "category", Name: "TooDeep", ParentID: &tip.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Fatalf("create below depth %d: %v", MaxDepth, err)
	}
	other := e.run(t, TermOp{Kind: "category", Name: "O", Slug: p + "-o"})[0]
	e.cleanup(t, other.ID)
	// The whole subtree moves with a term: the long chain cannot go under other's child.
	oc := e.run(t, TermOp{Kind: "category", Name: "OC", Slug: p + "-oc", ParentID: &other.ID})[0]
	e.cleanup(t, oc.ID)
	if _, err := e.apply(t, TermOp{ID: &root.ID, ParentID: &oc.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Fatalf("subtree too deep: %v", err)
	}
	lone := e.run(t, TermOp{Kind: "category", Name: "L", Slug: p + "-l"})[0]
	e.cleanup(t, lone.ID)
	if _, err := e.apply(t, TermOp{ID: &lone.ID, ParentID: &other.ID}); err != nil {
		t.Fatalf("leaf fits: %v", err)
	}
}

func TestApplyChangesetTxDeleteAndRestore(t *testing.T) {
	e := newTxEnv(t)
	slug := "cs-" + uuid.NewString()[:8]
	parent := e.run(t, TermOp{Kind: "category", Name: "P", Slug: slug + "-p"})[0]
	r := e.run(t, TermOp{Kind: "category", Name: "Gone", Slug: slug, Description: "keep me", ParentID: &parent.ID,
		Origin: "plug", SEO: jsontext.Value(`{"meta_title":"T","no_index":true}`)})[0]
	e.cleanup(t, r.ID, parent.ID)
	_, err := e.db.Pool().Exec(e.ctx, `UPDATE hatamoto_terms SET locale = 'xx' WHERE id = $1`, r.ID)
	if err != nil {
		t.Fatal(err)
	}

	del := e.run(t, TermOp{ID: &r.ID, Delete: true})[0]
	if !del.Deleted || len(del.Before) == 0 {
		t.Fatalf("delete %+v", del)
	}
	var n int
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT count(*) FROM hatamoto_terms WHERE id = $1`, r.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("still there: %d %v", n, err)
	}
	restore(t, e, del.Before)
	got := snapshotOf(t, e, r.ID)
	if got.Slug != slug || got.Name != "Gone" || got.Description != "keep me" || got.Origin != "plug" ||
		got.Locale != "xx" || got.ParentID == nil || *got.ParentID != parent.ID ||
		!strings.Contains(string(got.SEO), `"no_index": true`) && !strings.Contains(string(got.SEO), `"no_index":true`) {
		t.Fatalf("restored %+v seo=%s", got, got.SEO)
	}

	// Restore also undoes an update, keeping the row's identity.
	up := e.run(t, TermOp{ID: &r.ID, Name: "Renamed", Slug: slug + "-new", Description: "x"})[0]
	restore(t, e, up.Before)
	got = snapshotOf(t, e, r.ID)
	if got.Slug != slug || got.Name != "Gone" || got.Description != "keep me" {
		t.Fatalf("after undo %+v", got)
	}
}

func restore(t *testing.T, e *txEnv, before jsontext.Value) {
	t.Helper()
	tx, err := e.db.Pool().Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx) //nolint:errcheck
	if err := RestoreChangesetTx(e.ctx, tx, before); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
}

func snapshotOf(t *testing.T, e *txEnv, id uuid.UUID) termSnapshot {
	t.Helper()
	var s termSnapshot
	if err := e.db.Pool().QueryRow(e.ctx, `SELECT `+snapshotCols+` FROM hatamoto_terms WHERE id = $1`, id).Scan(s.dest()...); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRestoreChangesetTxRejectsGarbage(t *testing.T) {
	e := newTxEnv(t)
	tx, err := e.db.Pool().Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx) //nolint:errcheck
	if err := RestoreChangesetTx(e.ctx, tx, jsontext.Value(`{}`)); err == nil {
		t.Fatal("empty pre-image accepted")
	}
}

func TestNotifyChangedNilSafe(t *testing.T) {
	var s *Service
	s.NotifyChanged(context.Background())
	calls := 0
	s = &Service{}
	s.SetChangeObserver(func(context.Context) { calls++ })
	s.NotifyChanged(context.Background())
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}

// A term inserted, re-parented or restored by a changeset joins an ordered
// sibling set last instead of jumping to the top at 0.
func TestChangesetTermsJoinAnOrderedSetLast(t *testing.T) {
	e := newTxEnv(t)
	tag := "cs-" + uuid.NewString()[:8]
	parent := e.run(t, TermOp{Kind: "tag", Name: "P", Slug: tag + "-p"})[0]
	other := e.run(t, TermOp{Kind: "tag", Name: "O", Slug: tag + "-o"})[0]
	k1 := e.run(t, TermOp{Kind: "tag", Name: "K1", Slug: tag + "-1", ParentID: &parent.ID})[0]
	k2 := e.run(t, TermOp{Kind: "tag", Name: "K2", Slug: tag + "-2", ParentID: &parent.ID})[0]
	e.cleanup(t, k1.ID, k2.ID, parent.ID, other.ID)
	if _, err := e.db.Pool().Exec(e.ctx, `UPDATE hatamoto_terms SET sort_order = CASE id WHEN $1 THEN 1 ELSE 2 END WHERE id = ANY($2)`,
		k1.ID, []uuid.UUID{k1.ID, k2.ID}); err != nil {
		t.Fatal(err)
	}
	order := func() string {
		var out []string
		rows, err := e.db.Pool().Query(e.ctx, `SELECT name FROM hatamoto_terms WHERE parent_id = $1 ORDER BY sort_order, name`, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		return strings.Join(out, ",")
	}
	k3 := e.run(t, TermOp{Kind: "tag", Name: "K3", Slug: tag + "-3", ParentID: &parent.ID})[0]
	e.cleanup(t, k3.ID)
	if got := order(); got != "K1,K2,K3" {
		t.Errorf("after insert = %s", got)
	}
	// Re-parent another term under the set: last.
	e.run(t, TermOp{ID: &other.ID, Name: "O", ParentID: &parent.ID})
	if got := order(); got != "K1,K2,K3,O" {
		t.Errorf("after re-parent = %s", got)
	}
	// Delete then restore: last.
	del := e.run(t, TermOp{ID: &k1.ID, Delete: true})[0]
	tx, err := e.db.Pool().Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx) //nolint:errcheck
	if err := RestoreChangesetTx(e.ctx, tx, del.Before); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := order(); got != "K2,K3,O,K1" {
		t.Errorf("after restore = %s", got)
	}
}
