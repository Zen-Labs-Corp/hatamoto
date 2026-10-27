package entity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/builder"
)

func parseTestDoc(t *testing.T, s string) *builder.Doc {
	t.Helper()
	d, err := builder.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCheckReuseDoc(t *testing.T) {
	slotDoc := parseTestDoc(t, `{"v":2,"nodes":[{"id":"s1","t":"slot"}]}`)
	plain := parseTestDoc(t, `{"v":2,"nodes":[{"id":"h1","t":"heading","p":{"text":"Hi"}}]}`)
	noLookup := func(string) (*builder.Doc, bool) { return nil, false }

	t.Run("slot on a page", func(t *testing.T) {
		p := CheckReuseDoc(RolePage, &Entity{ID: uuid.New()}, slotDoc, noLookup)
		if p == nil || p.Code != "part_slot" || p.Status != http.StatusUnprocessableEntity {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("slot in a block", func(t *testing.T) {
		if p := CheckReuseDoc(RoleBlock, &Entity{ID: uuid.New()}, slotDoc, noLookup); p == nil || p.Code != "part_slot" {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("single part needs one slot", func(t *testing.T) {
		e := &Entity{ID: uuid.New(), Attributes: json.RawMessage(`{"slot":"single"}`)}
		if p := CheckReuseDoc(RolePart, e, plain, noLookup); p == nil || !strings.Contains(p.Msg, "exactly one") {
			t.Fatalf("got %+v", p)
		}
		if p := CheckReuseDoc(RolePart, e, slotDoc, noLookup); p != nil {
			t.Fatalf("one slot is fine: %+v", p)
		}
	})
	t.Run("header part has no slot", func(t *testing.T) {
		e := &Entity{ID: uuid.New(), Attributes: json.RawMessage(`{"slot":"header"}`)}
		if p := CheckReuseDoc(RolePart, e, slotDoc, noLookup); p == nil || !strings.Contains(p.Msg, "no Content slot") {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("ref cycle in a block", func(t *testing.T) {
		self := uuid.New()
		doc := parseTestDoc(t, `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"`+self.String()+`"}}]}`)
		p := CheckReuseDoc(RoleBlock, &Entity{ID: self}, doc, noLookup)
		if p == nil || p.Code != "ref_cycle" {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("page needs no ref check", func(t *testing.T) {
		doc := parseTestDoc(t, `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"`+uuid.NewString()+`"}}]}`)
		if p := CheckReuseDoc(RolePage, &Entity{ID: uuid.New()}, doc, noLookup); p != nil {
			t.Fatalf("got %+v", p)
		}
	})
}

type fakeBuilderStore struct {
	ents      map[uuid.UUID]*Entity
	schemas   map[uuid.UUID]*EntitySchema
	updated   *UpdateEntityInput
	statusSet EntityStatus
}

func (f *fakeBuilderStore) GetEntity(_ context.Context, id uuid.UUID) (*Entity, error) {
	if e, ok := f.ents[id]; ok {
		return e, nil
	}
	return nil, ErrEntityNotFound
}

func (f *fakeBuilderStore) GetSchema(_ context.Context, id uuid.UUID) (*EntitySchema, error) {
	if s, ok := f.schemas[id]; ok {
		return s, nil
	}
	return nil, ErrSchemaNotFound
}

func (f *fakeBuilderStore) UpdateEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *UpdateEntityInput) (*Entity, error) {
	f.updated = in
	e := *f.ents[id]
	e.Attributes = *in.Attributes
	e.Version++
	f.ents[id] = &e
	return &e, nil
}

func (f *fakeBuilderStore) UpdateEntityStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, st EntityStatus) (*Entity, error) {
	f.statusSet = st
	e := *f.ents[id]
	e.Status = st
	return &e, nil
}

func TestSaveBuilderDoc(t *testing.T) {
	pageSchema := &EntitySchema{ID: uuid.New(), Slug: "page"}
	page := &Entity{ID: uuid.New(), SchemaID: pageSchema.ID, Status: StatusDraft, Attributes: json.RawMessage(`{"keep":"me"}`)}
	st := &fakeBuilderStore{ents: map[uuid.UUID]*Entity{page.ID: page}, schemas: map[uuid.UUID]*EntitySchema{pageSchema.ID: pageSchema}}
	ctx := context.Background()

	good := json.RawMessage(`{"v":2,"nodes":[{"id":"h1","t":"heading","p":{"text":"Hi"}}]}`)
	saved, p, err := SaveBuilderDoc(ctx, st, nil, page, good, SaveOpts{ChangeSummary: "by test", Publish: true})
	if err != nil || p != nil {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	var attrs map[string]json.RawMessage
	_ = json.Unmarshal(saved.Entity.Attributes, &attrs)
	if string(attrs["keep"]) != `"me"` || len(attrs["page_doc"]) == 0 {
		t.Errorf("attributes not merged: %s", saved.Entity.Attributes)
	}
	if *st.updated.ChangeSummary != "by test" || st.statusSet != StatusPublished {
		t.Errorf("summary %q status %q", *st.updated.ChangeSummary, st.statusSet)
	}

	st.updated = nil
	_, p, _ = SaveBuilderDoc(ctx, st, nil, page, json.RawMessage(`{"v":2,"nodes":[{"id":"s1","t":"slot"}]}`), SaveOpts{})
	if p == nil || p.Code != "part_slot" || st.updated != nil {
		t.Fatalf("slot on page: %+v, wrote %v", p, st.updated != nil)
	}
	_, p, _ = SaveBuilderDoc(ctx, st, nil, page, json.RawMessage(`{"v":2,"nodes":[{"id":"x","t":"nope"}]}`), SaveOpts{})
	if p == nil || p.Code != "invalid_page_doc" {
		t.Fatalf("invalid doc: %+v", p)
	}
}

func TestSaveBuilderDocHooksAndOutcomes(t *testing.T) {
	pageSchema := &EntitySchema{ID: uuid.New(), Slug: "page"}
	newStore := func() (*fakeBuilderStore, *Entity) {
		page := &Entity{ID: uuid.New(), SchemaID: pageSchema.ID, Status: StatusDraft, Version: 4, Attributes: json.RawMessage(`{}`)}
		return &fakeBuilderStore{ents: map[uuid.UUID]*Entity{page.ID: page}, schemas: map[uuid.UUID]*EntitySchema{pageSchema.ID: pageSchema}}, page
	}
	ctx := context.Background()
	good := json.RawMessage(`{"v":2,"nodes":[{"id":"h1","t":"heading","p":{"text":"Hi"}}]}`)
	slotted := json.RawMessage(`{"v":2,"nodes":[{"id":"s1","t":"slot"}]}`)
	refuse := func(code string) func(...string) *DocProblem {
		return func(...string) *DocProblem { return &DocProblem{Code: code} }
	}

	t.Run("hooks run in order and refuse before any write", func(t *testing.T) {
		st, page := newStore()
		_, p, _ := SaveBuilderDoc(ctx, st, nil, page, slotted, SaveOpts{
			AfterParse:  func(string) *DocProblem { return refuse("after_parse")() },
			BeforeWrite: func() *DocProblem { return refuse("before_write")() },
		})
		if p == nil || p.Code != "after_parse" {
			t.Fatalf("AfterParse comes before the reuse rules: %+v", p)
		}
		_, p, _ = SaveBuilderDoc(ctx, st, nil, page, slotted, SaveOpts{BeforeWrite: func() *DocProblem { return refuse("before_write")() }})
		if p == nil || p.Code != "part_slot" {
			t.Fatalf("the reuse rules come before BeforeWrite: %+v", p)
		}
		_, p, _ = SaveBuilderDoc(ctx, st, nil, page, good, SaveOpts{BeforeWrite: func() *DocProblem { return refuse("before_write")() }})
		if p == nil || p.Code != "before_write" || st.updated != nil {
			t.Fatalf("BeforeWrite: %+v, wrote %v", p, st.updated != nil)
		}
	})
	t.Run("expected version and autosave reach the update", func(t *testing.T) {
		st, page := newStore()
		v := 4
		if _, p, err := SaveBuilderDoc(ctx, st, nil, page, good, SaveOpts{ExpectedVersion: &v, Autosave: true}); p != nil || err != nil {
			t.Fatal(p, err)
		}
		if st.updated.ExpectedVersion == nil || *st.updated.ExpectedVersion != 4 || !st.updated.Autosave || *st.updated.ChangeSummary != AutosaveSummary {
			t.Errorf("update: %+v", st.updated)
		}
	})
	t.Run("a publish that fails still reports the stored doc", func(t *testing.T) {
		st, page := newStore()
		failing := &failingStatusStore{fakeBuilderStore: st}
		saved, p, err := SaveBuilderDoc(ctx, failing, nil, page, good, SaveOpts{Publish: true})
		if p != nil || err != nil || saved == nil || saved.PublishErr == nil || saved.Entity.Version != 5 {
			t.Fatalf("saved=%+v p=%v err=%v", saved, p, err)
		}
	})
	t.Run("the publish gate skips the status change", func(t *testing.T) {
		st, page := newStore()
		saved, _, _ := SaveBuilderDoc(ctx, st, nil, page, good, SaveOpts{Publish: true, PublishGate: func(*Entity) string { return "why" }})
		if saved.PublishSkipped != "why" || st.statusSet != "" {
			t.Errorf("skipped=%q status=%q", saved.PublishSkipped, st.statusSet)
		}
	})
	t.Run("a taken address is dropped and reported", func(t *testing.T) {
		st, page := newStore()
		taken := &takenSlugStore{fakeBuilderStore: st}
		slug := "news"
		saved, p, err := SaveBuilderDoc(ctx, taken, nil, page, good, SaveOpts{Tune: func(in *UpdateEntityInput) { in.Slug = &slug }})
		if p != nil || err != nil || saved.TakenSlug != "news" || taken.calls != 2 {
			t.Fatalf("saved=%+v p=%v err=%v calls=%d", saved, p, err, taken.calls)
		}
	})
	t.Run("a version conflict is the store's error", func(t *testing.T) {
		st, page := newStore()
		conflict := &conflictStore{fakeBuilderStore: st}
		_, p, err := SaveBuilderDoc(ctx, conflict, nil, page, good, SaveOpts{})
		var vc *VersionConflictError
		if p != nil || !errors.As(err, &vc) || vc.Current != 9 {
			t.Fatalf("p=%v err=%v", p, err)
		}
	})
}

type failingStatusStore struct{ *fakeBuilderStore }

func (f *failingStatusStore) UpdateEntityStatus(context.Context, uuid.UUID, *uuid.UUID, EntityStatus) (*Entity, error) {
	return nil, errors.New("db down")
}

type takenSlugStore struct {
	*fakeBuilderStore
	calls int
}

func (f *takenSlugStore) UpdateEntity(ctx context.Context, id uuid.UUID, u *uuid.UUID, in *UpdateEntityInput) (*Entity, error) {
	f.calls++
	if in.Slug != nil {
		return nil, ErrSlugAlreadyExists
	}
	return f.fakeBuilderStore.UpdateEntity(ctx, id, u, in)
}

type conflictStore struct{ *fakeBuilderStore }

func (f *conflictStore) UpdateEntity(context.Context, uuid.UUID, *uuid.UUID, *UpdateEntityInput) (*Entity, error) {
	return nil, &VersionConflictError{Current: 9}
}
