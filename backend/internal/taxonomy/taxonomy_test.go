package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for service-level unit tests.
type fakeRepo struct {
	mu          sync.Mutex
	terms       map[uuid.UUID]*Term
	bySlug      map[string]*Term // key: kind|slug
	entityTerms map[uuid.UUID]map[uuid.UUID]struct{}
	publishedBy map[uuid.UUID]*EntityRef // entity_id -> projection
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		terms:       map[uuid.UUID]*Term{},
		bySlug:      map[string]*Term{},
		entityTerms: map[uuid.UUID]map[uuid.UUID]struct{}{},
		publishedBy: map[uuid.UUID]*EntityRef{},
	}
}

func (f *fakeRepo) CreateTerm(_ context.Context, t *Term) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := t.Kind + "|" + t.Slug
	if _, exists := f.bySlug[key]; exists {
		return ErrTermSlugConflict
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	cp := *t
	f.terms[t.ID] = &cp
	f.bySlug[key] = &cp
	return nil
}

func (f *fakeRepo) UpdateTerm(_ context.Context, t *Term) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.terms[t.ID]
	if !ok {
		return ErrTermNotFound
	}
	newKey := existing.Kind + "|" + t.Slug
	if other, ok := f.bySlug[newKey]; ok && other.ID != t.ID {
		return ErrTermSlugConflict
	}
	oldKey := existing.Kind + "|" + existing.Slug
	delete(f.bySlug, oldKey)
	existing.Name = t.Name
	existing.Slug = t.Slug
	existing.Description = t.Description
	existing.ParentID = t.ParentID
	existing.UpdatedAt = time.Now()
	f.bySlug[newKey] = existing
	return nil
}

func (f *fakeRepo) Chains(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]*Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID][]*Term{}
	for _, id := range ids {
		var chain []*Term
		for cur, n := f.terms[id], 0; cur != nil && n <= MaxDepth; n++ {
			cp := *cur
			chain = append([]*Term{&cp}, chain...)
			if cur.ParentID == nil {
				break
			}
			cur = f.terms[*cur.ParentID]
		}
		if chain != nil {
			out[id] = chain
		}
	}
	return out, nil
}

func (f *fakeRepo) KindExists(_ context.Context, kind string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.terms {
		if t.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) DeleteTerm(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.terms[id]; ok {
		delete(f.bySlug, t.Kind+"|"+t.Slug)
		delete(f.terms, id)
	}
	for _, attached := range f.entityTerms {
		delete(attached, id)
	}
	return nil
}

func (f *fakeRepo) GetTerm(_ context.Context, id uuid.UUID) (*Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.terms[id]
	if !ok {
		return nil, ErrTermNotFound
	}
	cp := *t
	return &cp, nil
}

func (f *fakeRepo) GetTermBySlug(_ context.Context, kind, slug string) (*Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.bySlug[kind+"|"+slug]
	if !ok {
		return nil, ErrTermNotFound
	}
	cp := *t
	return &cp, nil
}

func (f *fakeRepo) ListTerms(_ context.Context, kind, search string, page, perPage int) (*TermListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	var filtered []*Term
	for _, t := range f.terms {
		if kind != "" && t.Kind != kind {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(search)) {
			continue
		}
		cp := *t
		filtered = append(filtered, &cp)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Kind != filtered[j].Kind {
			return filtered[i].Kind < filtered[j].Kind
		}
		return filtered[i].Name < filtered[j].Name
	})
	total := len(filtered)
	start := min((page-1)*perPage, total)
	end := min(start+perPage, total)
	out := make([]*TermWithCount, 0, end-start)
	for _, t := range filtered[start:end] {
		count := 0
		for _, attached := range f.entityTerms {
			if _, ok := attached[t.ID]; ok {
				count++
			}
		}
		out = append(out, &TermWithCount{Term: *t, EntityCount: count})
	}
	return &TermListResult{Items: out, Total: total, Page: page, PerPage: perPage}, nil
}

func (f *fakeRepo) ListTermsByIDs(_ context.Context, ids []uuid.UUID) ([]*Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Term{}
	for _, id := range ids {
		if t, ok := f.terms[id]; ok {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListTermsForEntity(_ context.Context, entityID uuid.UUID) ([]*Term, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	attached := f.entityTerms[entityID]
	out := []*Term{}
	for tid := range attached {
		if t, ok := f.terms[tid]; ok {
			cp := *t
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (f *fakeRepo) ReplaceEntityTerms(_ context.Context, entityID uuid.UUID, termIDs []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	set := make(map[uuid.UUID]struct{}, len(termIDs))
	for _, id := range termIDs {
		if id == uuid.Nil {
			continue
		}
		// Mirror the production repo's in-tx FOR SHARE existence check:
		// a missing term yields a typed unknown_term:<uuid> error the
		// handler maps to 400. Pre-fix this returned a generic
		// fk_violation that the handler 500'd as replace_failed.
		if _, ok := f.terms[id]; !ok {
			return fmt.Errorf("unknown_term:%s", id)
		}
		set[id] = struct{}{}
	}
	f.entityTerms[entityID] = set
	return nil
}

func (f *fakeRepo) ListEntitiesForTerm(_ context.Context, termID uuid.UUID, page, perPage int) ([]*EntityRef, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if perPage < 1 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	var matched []*EntityRef
	for entityID, attached := range f.entityTerms {
		if _, ok := attached[termID]; !ok {
			continue
		}
		ref, ok := f.publishedBy[entityID]
		if !ok {
			continue
		}
		cp := *ref
		matched = append(matched, &cp)
	}
	total := len(matched)
	start := min((page-1)*perPage, total)
	end := min(start+perPage, total)
	return matched[start:end], total, nil
}

// --- helpers for tests ---

func (f *fakeRepo) seedEntity(id uuid.UUID, ref *EntityRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref.ID = id
	f.publishedBy[id] = ref
}

// --- tests ---

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AI Visibility", "ai-visibility"},
		{"  Trim Me  ", "trim-me"},
		{"Already-Slug", "already-slug"},
		{"Multiple   Spaces", "multiple-spaces"},
		{"Symbols#$%@! Galore", "symbols-galore"},
		{"123 numbers ok", "123-numbers-ok"},
		{"-leading-and-trailing-", "leading-and-trailing"},
		{"日本語", ""},
	}
	for _, c := range cases {
		got := Slugify(c.in)
		if got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidSlug(t *testing.T) {
	good := []string{"ai", "ai-visibility", "abc-123", "z"}
	bad := []string{"", "Capitals", "with space", "trailing-", "-leading", "double--hyphen"}
	for _, s := range good {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true, want false", s)
		}
	}
}

func TestServiceCreateTerm_AutoSlug(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	term, err := svc.CreateTerm(context.Background(), CreateTermInput{
		Kind: "tag", Name: "AI Visibility",
	})
	if err != nil {
		t.Fatalf("CreateTerm: %v", err)
	}
	if term.Slug != "ai-visibility" {
		t.Errorf("expected auto-slug ai-visibility, got %q", term.Slug)
	}
	if term.Kind != "tag" {
		t.Errorf("expected kind tag, got %q", term.Kind)
	}
}

func TestServiceCreateTerm_RejectsInvalidKind(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	if _, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "Bad Kind", Name: "x"}); err == nil {
		t.Fatal("expected error for invalid kind")
	}
	if _, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "author", Name: "x"}); err == nil {
		t.Fatal("expected error for a kind that is a reserved URL segment")
	}
}

func TestServiceCreateTerm_SlugConflict(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	if _, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "AI"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "AI"})
	if !errors.Is(err, ErrTermSlugConflict) {
		t.Fatalf("expected ErrTermSlugConflict, got %v", err)
	}
}

func TestServiceCreateTerm_SameSlugAcrossKinds(t *testing.T) {
	// Composite (kind, slug) uniqueness — tag:ai and category:ai must coexist.
	svc := NewService(newFakeRepo(), nil)
	if _, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "AI"}); err != nil {
		t.Fatalf("tag create: %v", err)
	}
	if _, err := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "category", Name: "AI"}); err != nil {
		t.Fatalf("category create should succeed (different kind): %v", err)
	}
}

func TestServiceReplaceEntityTerms_UnknownTerm(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	entityID := uuid.New()
	if err := svc.ReplaceEntityTerms(context.Background(), entityID, []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("expected error for unknown term id")
	}
}

func TestServiceReplaceEntityTerms_RoundTrip(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	tag, _ := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "AI"})
	cat, _ := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "category", Name: "Research"})
	entityID := uuid.New()
	if err := svc.ReplaceEntityTerms(context.Background(), entityID, []uuid.UUID{tag.ID, cat.ID}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	terms, err := svc.ListTermsForEntity(context.Background(), entityID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(terms) != 2 {
		t.Fatalf("expected 2 attached terms, got %d", len(terms))
	}
	// Detach all
	if err := svc.ReplaceEntityTerms(context.Background(), entityID, []uuid.UUID{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	terms, _ = svc.ListTermsForEntity(context.Background(), entityID)
	if len(terms) != 0 {
		t.Fatalf("expected zero after clear, got %d", len(terms))
	}
}

func TestServiceListEntitiesForTerm_ListsPublished(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	tag, _ := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "AI"})
	a, b := uuid.New(), uuid.New()
	repo.seedEntity(a, &EntityRef{Name: "A", Slug: "a-1", SchemaSlug: "blog"})
	repo.seedEntity(b, &EntityRef{Name: "B", Slug: "b-1", SchemaSlug: "blog"})
	for _, id := range []uuid.UUID{a, b} {
		if err := repo.ReplaceEntityTerms(context.Background(), id, []uuid.UUID{tag.ID}); err != nil {
			t.Fatalf("attach %s: %v", id, err)
		}
	}
	res, err := svc.ListEntitiesForTerm(context.Background(), "tag", "ai", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("expected total 2, got %d", res.Total)
	}
}

// TestReplaceEntityTerms_TOCTOU_TermDeletedAfterPreFetch verifies
// Phase 2 review item #4: when a term is deleted between the service-
// layer pre-fetch and the repository tx, the repo must surface
// unknown_term:<uuid> (a 400 from the handler) instead of letting a
// generic FK violation bubble up as a 500. The fakeRepo's
// ReplaceEntityTerms mirrors the production repo's in-tx FOR SHARE
// existence check, so removing the term from the repo state before
// calling Replace simulates the exact TOCTOU race.
func TestReplaceEntityTerms_TOCTOU_TermDeletedAfterPreFetch(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	ctx := context.Background()

	// Create a term, then snapshot its ID — this represents what the
	// service's pre-fetch saw moments earlier.
	tag, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: "AI"})
	if err != nil {
		t.Fatalf("CreateTerm: %v", err)
	}

	// Race window: another admin deletes the term before our PUT
	// reaches the repo.
	if err := svc.DeleteTerm(ctx, tag.ID); err != nil {
		t.Fatalf("DeleteTerm: %v", err)
	}

	// Call the repo directly (bypassing the service's pre-fetch) to
	// prove the in-tx validation catches the missing term.
	entityID := uuid.New()
	err = repo.ReplaceEntityTerms(ctx, entityID, []uuid.UUID{tag.ID})
	if err == nil {
		t.Fatalf("expected error for deleted term, got nil")
	}
	wantPrefix := "unknown_term:" + tag.ID.String()
	if err.Error() != wantPrefix {
		t.Fatalf("expected error %q, got %q", wantPrefix, err.Error())
	}

	// The entity's join rows must remain untouched — the failed Replace
	// must not have left a partial state. (Belt-and-braces; the prod
	// tx rolls back on any returned error.)
	terms, err := svc.ListTermsForEntity(ctx, entityID)
	if err != nil {
		t.Fatalf("ListTermsForEntity: %v", err)
	}
	if len(terms) != 0 {
		t.Errorf("expected no attachments after failed Replace, got %d", len(terms))
	}
}

func TestServiceUpdateTerm_PreservesKind(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	t1, _ := svc.CreateTerm(context.Background(), CreateTermInput{Kind: "tag", Name: "AI"})
	updated, err := svc.UpdateTerm(context.Background(), UpdateTermInput{ID: t1.ID, Name: "AI Visibility"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Kind != "tag" {
		t.Errorf("expected kind unchanged, got %q", updated.Kind)
	}
	if updated.Slug != "ai-visibility" {
		t.Errorf("expected auto-slug refresh on name-only update, got %q", updated.Slug)
	}
}

func TestValidKind(t *testing.T) {
	for _, k := range []string{"tag", "category", "genre", "series-2", "topic_x"} {
		if !ValidKind(k) {
			t.Errorf("ValidKind(%q) = false", k)
		}
	}
	for _, k := range []string{"", "Genre", "9lives", "has space", "way-too-long-kind-name", "author", "search", "api", "admin", "theme", "forms", "auth", "authors", "auth-x"} {
		if ValidKind(k) {
			t.Errorf("ValidKind(%q) = true", k)
		}
	}
}

func TestServiceHierarchy(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), nil)
	mk := func(kind, name string, parent *uuid.UUID) *Term {
		t.Helper()
		term, err := svc.CreateTerm(ctx, CreateTermInput{Kind: kind, Name: name, ParentID: parent, Description: " about " + name + " "})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return term
	}
	tut := mk("category", "Tutorials", nil)
	goT := mk("category", "Go", &tut.ID)
	web := mk("category", "Web", &goT.ID)
	if web.ParentID == nil || *web.ParentID != goT.ID || web.Description != "about Web" {
		t.Fatalf("child = %+v", web)
	}
	other := mk("genre", "Noir", nil)

	if _, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "category", Name: "X", ParentID: &other.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("parent of another kind: %v", err)
	}
	missing := uuid.New()
	if _, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "category", Name: "X", ParentID: &missing}); !errors.Is(err, ErrTermParentNotFound) {
		t.Errorf("missing parent: %v", err)
	}
	// A cycle: Tutorials under its own grandchild, and under itself.
	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: tut.ID, ParentID: &web.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("cycle: %v", err)
	}
	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: tut.ID, ParentID: &tut.ID}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("self parent: %v", err)
	}
	// Moving and clearing.
	moved, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: web.ID, ParentID: &tut.ID})
	if err != nil || moved.ParentID == nil || *moved.ParentID != tut.ID {
		t.Fatalf("move: %+v %v", moved, err)
	}
	cleared, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: web.ID, ClearParent: true})
	if err != nil || cleared.ParentID != nil {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	// Depth cap: a chain of MaxDepth terms takes no more children.
	parent := &tut.ID
	for i := 1; i < MaxDepth; i++ {
		c, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "category", Name: fmt.Sprintf("Deep %d", i), ParentID: parent})
		if err != nil {
			t.Fatalf("depth %d: %v", i+1, err)
		}
		parent = &c.ID
	}
	if _, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "category", Name: "Too deep", ParentID: parent}); !errors.Is(err, ErrTermParentInvalid) {
		t.Errorf("past MaxDepth: %v", err)
	}
}

func TestFillPaths(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	a, _ := svc.CreateTerm(ctx, CreateTermInput{Kind: "genre", Name: "Fiction"})
	b, _ := svc.CreateTerm(ctx, CreateTermInput{Kind: "genre", Name: "Noir", ParentID: &a.ID})
	c, _ := svc.CreateTerm(ctx, CreateTermInput{Kind: "genre", Name: "Neo Noir", ParentID: &b.ID})
	if err := FillPaths(ctx, repo, a, c); err != nil {
		t.Fatal(err)
	}
	if a.Path != "fiction" || c.Path != "fiction/noir/neo-noir" {
		t.Errorf("paths = %q, %q", a.Path, c.Path)
	}
}

func TestHeight(t *testing.T) {
	root, a, b, c := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	desc := []*Term{{ID: a, ParentID: &root}, {ID: b, ParentID: &a}, {ID: c, ParentID: &b}}
	if got := height(root, desc); got != 3 {
		t.Errorf("height = %d, want 3", got)
	}
	if got := height(root, nil); got != 0 {
		t.Errorf("leaf height = %d", got)
	}
}

// Terms are part of rendered pages (names, archive links), so every write
// must tell the change observer, which purges the page cache.
func TestServiceWritesNotifyChangeObserver(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	calls := 0
	svc.SetChangeObserver(func(context.Context) { calls++ })
	ctx := context.Background()

	tag, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: "AI"})
	if err != nil || calls != 1 {
		t.Fatalf("create: err=%v calls=%d", err, calls)
	}
	if _, err := svc.UpdateTerm(ctx, UpdateTermInput{ID: tag.ID, Name: "Artificial"}); err != nil || calls != 2 {
		t.Fatalf("update: err=%v calls=%d", err, calls)
	}
	if err := svc.ReplaceEntityTerms(ctx, uuid.New(), []uuid.UUID{tag.ID}); err != nil || calls != 3 {
		t.Fatalf("replace entity terms: err=%v calls=%d", err, calls)
	}
	if err := svc.DeleteTerm(ctx, tag.ID); err != nil || calls != 4 {
		t.Fatalf("delete: err=%v calls=%d", err, calls)
	}
	// A refused write must not purge.
	if _, err := svc.CreateTerm(ctx, CreateTermInput{Kind: "tag", Name: ""}); err == nil || calls != 4 {
		t.Fatalf("invalid create: err=%v calls=%d", err, calls)
	}
}
