//go:build integration

package entity

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const demoOrigin = "plugin:demo"

func (f *csFixture) origin(id uuid.UUID) string {
	f.t.Helper()
	var o string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM hatamoto_entities WHERE id=$1`, id).Scan(&o); err != nil {
		f.t.Fatal(err)
	}
	return o
}

// demoEntity creates a plugin-owned entity the way a changeset import does.
func (f *csFixture) demoEntity(slug string) *Entity {
	f.t.Helper()
	res, err := f.apply(TxOp{Name: "Imported " + slug, Slug: slug, Attributes: json.RawMessage(`{"content":"one"}`), Origin: demoOrigin})
	if err != nil {
		f.t.Fatal(err)
	}
	e, err := f.repo.GetByID(f.ctx, res.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if got := f.origin(e.ID); got != demoOrigin {
		f.t.Fatalf("setup origin = %q", got)
	}
	return e
}

func ptr[T any](v T) *T { return &v }

func TestOwnershipHandOver(t *testing.T) {
	f := newCSFixture(t)

	// Any attribute change hands over: the body, and every SEO key. A key like
	// "featured" counts too (accepted: one blob, no key list).
	attrCases := map[string]string{
		"body":             `{"content":"two"}`,
		"meta_title":       `{"content":"one","meta_title":"T"}`,
		"meta_description": `{"content":"one","meta_description":"D"}`,
		"canonical_url":    `{"content":"one","canonical_url":"https://example.com/x"}`,
		"og_image":         `{"content":"one","og_image":"https://example.com/i.png"}`,
		"no_index":         `{"content":"one","no_index":true}`,
		"seo.focus":        `{"content":"one","seo":{"focus_keyword":"k"}}`,
		"featured":         `{"content":"one","featured":true}`,
	}
	for name, attrs := range attrCases {
		t.Run("attrs/"+name, func(t *testing.T) {
			e := f.demoEntity("a-" + uuid.NewString()[:8])
			out, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Attributes: ptr(json.RawMessage(attrs))})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.origin(e.ID); got != "owner" {
				t.Errorf("origin = %q, want owner", got)
			}
			if !out.TookOwnership {
				t.Error("TookOwnership not set")
			}
			// A second structural edit is not a new hand-over.
			again, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Attributes: ptr(json.RawMessage(`{"content":"three"}`))})
			if err != nil {
				t.Fatal(err)
			}
			if again.TookOwnership {
				t.Error("TookOwnership set for an entity that was already the owner's")
			}
		})
	}

	t.Run("slug", func(t *testing.T) {
		e := f.demoEntity("slug-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Slug: ptr("moved-" + uuid.NewString()[:8])}); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != "owner" {
			t.Errorf("origin = %q", got)
		}
	})

	t.Run("parent set and clear", func(t *testing.T) {
		parent := f.demoEntity("par-" + uuid.NewString()[:8])
		e := f.demoEntity("kid-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{ParentID: &parent.ID}); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != "owner" {
			t.Errorf("set parent: origin = %q", got)
		}
		// Hand it back by hand, then clear the parent.
		if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET origin=$2 WHERE id=$1`, e.ID, demoOrigin); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{ClearParent: true}); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != "owner" {
			t.Errorf("clear parent: origin = %q", got)
		}
	})

	t.Run("restore version with a changed body", func(t *testing.T) {
		e := f.demoEntity("rv-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Attributes: ptr(json.RawMessage(`{"content":"two"}`))}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET origin=$2 WHERE id=$1`, e.ID, demoOrigin); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.RestoreVersion(f.ctx, e.ID, nil, e.Version); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != "owner" {
			t.Errorf("origin = %q", got)
		}
	})

	// Saves that change nothing structural leave the origin alone.
	t.Run("no change", func(t *testing.T) {
		e := f.demoEntity("same-" + uuid.NewString()[:8])
		in := &UpdateEntityInput{
			Name:       ptr(e.Name),
			Slug:       ptr(e.Slug),
			Locale:     ptr(e.Locale),
			Attributes: ptr(json.RawMessage(e.Attributes)),
		}
		out, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, in)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != demoOrigin || out.TookOwnership {
			t.Errorf("identical re-save: origin = %q, took = %v", got, out.TookOwnership)
		}
	})

	t.Run("name only", func(t *testing.T) {
		e := f.demoEntity("name-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Name: ptr("Renamed")}); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != demoOrigin {
			t.Errorf("origin = %q", got)
		}
	})

	t.Run("locale only", func(t *testing.T) {
		e := f.demoEntity("loc-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{Locale: ptr("sv-SE")}); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != demoOrigin {
			t.Errorf("origin = %q", got)
		}
	})

	t.Run("machine write keeps origin", func(t *testing.T) {
		e := f.demoEntity("keep-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntity(f.ctx, e.ID, nil, &UpdateEntityInput{
			Attributes: ptr(json.RawMessage(`{"content":"converted"}`)), Slug: ptr("keep-moved-" + uuid.NewString()[:8]), KeepUpdatedAt: true,
		}); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != demoOrigin {
			t.Errorf("origin = %q", got)
		}
	})

	t.Run("status, schedule and terms", func(t *testing.T) {
		e := f.demoEntity("st-" + uuid.NewString()[:8])
		if _, err := f.svc.UpdateEntityStatus(f.ctx, e.ID, nil, StatusPublished); err != nil {
			t.Fatal(err)
		}
		f.setTerms(e.ID, 2)
		f.setTerms(e.ID, 1)
		if got := f.origin(e.ID); got != demoOrigin {
			t.Errorf("origin = %q", got)
		}
	})

	t.Run("bulk status over 500 rows", func(t *testing.T) {
		ids := make([]uuid.UUID, 0, 500)
		err := f.inTx(func(tx pgx.Tx) error {
			for i := 0; i < 500; i++ {
				res, err := f.svc.ApplyChangesetTx(f.ctx, tx, TxOp{
					Schema: f.schema.Slug, Name: fmt.Sprintf("Bulk %d", i), Slug: fmt.Sprintf("bulk-%d-%s", i, uuid.NewString()[:6]),
					Attributes: json.RawMessage(`{"content":"b"}`), Origin: demoOrigin,
				})
				if err != nil {
					return err
				}
				ids = append(ids, res.ID)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if _, err := f.svc.UpdateEntityStatus(f.ctx, id, nil, StatusPublished); err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := f.db.Pool().QueryRow(f.ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id=$1 AND origin=$2`, f.schema.ID, demoOrigin).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n < 500 {
			t.Errorf("%d rows still plugin-owned, want >= 500", n)
		}
	})

	// Publish and revert never hand over (ISC-9): the changeset path does not
	// go through UpdateEntity.
	t.Run("changeset apply and restore", func(t *testing.T) {
		e := f.demoEntity("cs-" + uuid.NewString()[:8])
		up, err := f.apply(TxOp{ID: &e.ID, Name: "Changed", Slug: "cs-moved-" + uuid.NewString()[:8], Attributes: json.RawMessage(`{"content":"by plugin"}`), Origin: demoOrigin})
		if err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != demoOrigin {
			t.Errorf("after apply: origin = %q", got)
		}
		if err := f.inTx(func(tx pgx.Tx) error { return f.svc.RestoreChangesetTx(f.ctx, tx, e.ID, up.Before) }); err != nil {
			t.Fatal(err)
		}
		if got := f.origin(e.ID); got != demoOrigin {
			t.Errorf("after restore: origin = %q", got)
		}
	})
}

// setTerms replaces the entity's tag set with n fresh tags (raw SQL: the
// taxonomy service sits above this package).
func (f *csFixture) setTerms(id uuid.UUID, n int) {
	f.t.Helper()
	ctx := f.ctx
	if _, err := f.db.Pool().Exec(ctx, `DELETE FROM hatamoto_entity_terms WHERE entity_id=$1`, id); err != nil {
		f.t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		var tid uuid.UUID
		s := uuid.NewString()[:10]
		if err := f.db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_terms (kind, slug, name) VALUES ('tag',$1,$1) RETURNING id`, s).Scan(&tid); err != nil {
			f.t.Fatal(err)
		}
		f.t.Cleanup(func() { _, _ = f.db.Pool().Exec(ctx, `DELETE FROM hatamoto_terms WHERE id=$1`, tid) })
		if _, err := f.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_terms (entity_id, term_id) VALUES ($1,$2)`, id, tid); err != nil {
			f.t.Fatal(err)
		}
	}
}
