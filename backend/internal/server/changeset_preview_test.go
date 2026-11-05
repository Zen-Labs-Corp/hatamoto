package server

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
)

var (
	testPageSchema = &entity.EntitySchema{ID: uuid.New(), Slug: "page"}
	testRunID      = uuid.MustParse("11111111-1111-1111-1111-111111111111")
)

func liveEntity(id uuid.UUID, name, slug string) *entity.Entity {
	return &entity.Entity{ID: id, SchemaID: testPageSchema.ID, Schema: testPageSchema, Name: name, Slug: slug,
		Status: entity.StatusPublished, Attributes: jsontext.Value(`{"meta_title":"Old"}`)}
}

func testSource(live map[uuid.UUID]*entity.Entity, mapped map[string]uuid.UUID) stagedSource {
	return stagedSource{
		snapshot: entity.DefaultURLSnapshot(),
		entity: func(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
			if e, ok := live[id]; ok {
				c := *e
				return &c, nil
			}
			return nil, entity.ErrEntityNotFound
		},
		schema: func(_ context.Context, slug string) (*entity.EntitySchema, error) {
			if slug == "page" {
				return testPageSchema, nil
			}
			return nil, entity.ErrEntityNotFound
		},
		mapped: func(_ context.Context, _ string, _ changeset.Kind, key string) (uuid.UUID, bool, error) {
			id, ok := mapped[key]
			return id, ok, nil
		},
	}
}

func item(seq int, op, target, key, payload string) changeset.StoredItem {
	return changeset.StoredItem{Seq: seq, Item: changeset.Item{Kind: changeset.KindEntity, Op: op, TargetID: target, SourceKey: key, Payload: jsontext.Value(payload)}}
}

func TestChangesetPreviewStagedBuild(t *testing.T) {
	liveID, goneID := uuid.New(), uuid.New()
	live := map[uuid.UUID]*entity.Entity{liveID: liveEntity(liveID, "About", "about"), goneID: liveEntity(goneID, "Old page", "old")}
	h := changeset.Header{ID: 42, RunID: testRunID, Plugin: "seo"}
	o := newStagedChangeset(context.Background(), h, []changeset.StoredItem{
		item(1, changeset.OpUpsert, liveID.String(), "", `{"name":"About us","attributes":{"meta_title":"New"}}`),
		item(2, changeset.OpUpsert, "", "new-1", `{"schema":"page","name":"Pricing","slug":"pricing","attributes":{}}`),
		item(3, changeset.OpDelete, goneID.String(), "", `{}`),
		{Seq: 4, Item: changeset.Item{Kind: changeset.KindTerm, Op: changeset.OpUpsert, Payload: jsontext.Value(`{}`)}},
		item(5, changeset.OpUpsert, "", "bad", `{"schema":"nope","name":"X"}`),
	}, testSource(live, nil))

	if len(o.items) != 3 {
		t.Fatalf("previewable items = %d, want 3 (term and unknown schema are left out)", len(o.items))
	}
	if se := o.byPath["/about"]; se == nil || se.Ent.Name != "About us" || se.Live == nil {
		t.Fatalf("update not staged at its path: %+v", se)
	}
	created := o.byPath["/pricing"]
	if created == nil || created.Live != nil || created.Ent.ID != stagedID(testRunID, "new-1") {
		t.Fatalf("create not staged: %+v", created)
	}
	if !o.deleted[goneID] || o.items[2].Path != "/old" {
		t.Errorf("delete not recorded: %v %q", o.deleted, o.items[2].Path)
	}
	if live[liveID].Name != "About" {
		t.Error("staged set modified the live row")
	}
	idA, idAAgain := stagedID(testRunID, "a"), stagedID(testRunID, "a")
	if idA != idAAgain {
		t.Error("synthetic id is not stable per key")
	}
	if idA == stagedID(testRunID, "b") {
		t.Error("synthetic id collides across keys")
	}
}

func TestChangesetPreviewStagedParentKeyAndMappedUpdate(t *testing.T) {
	liveID := uuid.New()
	live := map[uuid.UUID]*entity.Entity{liveID: liveEntity(liveID, "Docs", "docs")}
	o := newStagedChangeset(context.Background(), changeset.Header{ID: 1, RunID: testRunID, Plugin: "p"}, []changeset.StoredItem{
		item(1, changeset.OpUpsert, "", "docs-key", `{"name":"Docs v2"}`), // maps to the live row
		item(2, changeset.OpUpsert, "", "child", `{"schema":"page","name":"Child","parent_key":"docs-key"}`),
		item(3, changeset.OpUpsert, "", "orphan", `{"schema":"page","name":"Orphan","parent_key":"missing"}`),
	}, testSource(live, map[string]uuid.UUID{"docs-key": liveID}))
	if len(o.items) != 2 {
		t.Fatalf("items = %d, want 2 (orphan parent_key left out)", len(o.items))
	}
	if o.items[0].Live == nil || o.items[0].Ent.Name != "Docs v2" {
		t.Errorf("source-key update did not merge into the live row: %+v", o.items[0].Ent)
	}
	if p := o.items[1].Ent.ParentID; p == nil || *p != liveID {
		t.Errorf("parent_key not resolved to the mapped entity: %v", p)
	}
}

type noEntityRepo struct{}

func (noEntityRepo) GetPublishedBySlugAndSchemas(context.Context, string, []string) (*entity.Entity, error) {
	return nil, entity.ErrEntityNotFound
}
func (noEntityRepo) GetEntityPath(context.Context, uuid.UUID) ([]entity.BreadcrumbPathItem, error) {
	return nil, nil
}
func (noEntityRepo) GetPublishedByPath(context.Context, []string, []string) (*entity.Entity, error) {
	return nil, entity.ErrEntityNotFound
}

// liveRepo serves one published page at its slug.
type liveRepo struct {
	noEntityRepo
	ent *entity.Entity
}

func (r liveRepo) GetPublishedBySlugAndSchemas(_ context.Context, slug string, _ []string) (*entity.Entity, error) {
	if slug == r.ent.Slug {
		c := *r.ent
		return &c, nil
	}
	return nil, entity.ErrEntityNotFound
}

func TestChangesetPreviewResolve(t *testing.T) {
	liveID, goneID := uuid.New(), uuid.New()
	about := liveEntity(liveID, "About", "about")
	snap := entity.DefaultURLSnapshot()
	live := map[uuid.UUID]*entity.Entity{liveID: about, goneID: liveEntity(goneID, "Old", "old")}
	o := newStagedChangeset(context.Background(), changeset.Header{ID: 7, RunID: testRunID}, []changeset.StoredItem{
		item(1, changeset.OpUpsert, liveID.String(), "", `{"name":"About us"}`),
		item(2, changeset.OpUpsert, "", "k", `{"schema":"page","name":"Pricing","slug":"pricing"}`),
		item(3, changeset.OpDelete, goneID.String(), "", `{}`),
	}, testSource(live, nil))
	ctx := withStaged(context.Background(), o)

	// The same server code, no staged set: live data only.
	if res, err := resolveStagedOrLive(context.Background(), noEntityRepo{}, snap, "/pricing", "/pricing"); err != nil || res != nil {
		t.Fatalf("plain request resolved a staged path: %v %v", res, err)
	}
	if res, _ := resolveStagedOrLive(context.Background(), liveRepo{ent: about}, snap, "/about", "/about"); res == nil || res.Entity.Name != "About" {
		t.Fatalf("plain request lost the live page: %+v", res)
	}
	// In the preview: the create answers, the update shows the staged name, the delete 404s.
	if res, err := resolveStagedOrLive(ctx, noEntityRepo{}, snap, "/pricing", "/pricing"); err != nil || res == nil || res.Entity.Name != "Pricing" {
		t.Fatalf("staged create not resolved: %+v %v", res, err)
	}
	if res, _ := resolveStagedOrLive(ctx, liveRepo{ent: about}, snap, "/about", "/about"); res == nil || res.Entity.Name != "About us" {
		t.Fatalf("staged update not resolved: %+v", res)
	}
	if res, _ := resolveStagedOrLive(ctx, liveRepo{ent: live[goneID]}, snap, "/old", "/old"); res != nil {
		t.Fatalf("deleted page still resolves: %+v", res)
	}
}

func TestChangesetPreviewLoaderSeamsAreInertWithoutStaged(t *testing.T) {
	id := uuid.New()
	ent := liveEntity(id, "About", "about")
	applyStagedEntity(context.Background(), ent)
	if ent.Name != "About" {
		t.Error("applyStagedEntity changed an entity without a staged set")
	}
	if got := stagedByScope(context.Background(), publicScope{EntityID: id}, true); got != nil {
		t.Errorf("stagedByScope = %v without a staged set", got)
	}
	o := newStagedChangeset(context.Background(), changeset.Header{ID: 1, RunID: testRunID}, []changeset.StoredItem{
		item(1, changeset.OpUpsert, id.String(), "", `{"name":"Renamed","status":"draft"}`),
	}, testSource(map[uuid.UUID]*entity.Entity{id: ent}, nil))
	ctx := withStaged(context.Background(), o)
	other := liveEntity(uuid.New(), "Other", "other")
	applyStagedEntity(ctx, other)
	if other.Name != "Other" {
		t.Error("an untouched entity was changed")
	}
	applyStagedEntity(ctx, ent)
	if ent.Name != "Renamed" || ent.Status != entity.StatusPublished {
		t.Errorf("staged entity not applied: %q %q", ent.Name, ent.Status)
	}
}

func TestChangesetPreviewChanges(t *testing.T) {
	withDoc := func(nodes string) jsontext.Value {
		return jsontext.Value(`{"page_doc":{"v":2,"nodes":[` + nodes + `]}}`)
	}
	node := func(id, text string) string {
		return `{"id":"` + id + `","t":"text","p":{"text":"` + text + `"}}`
	}
	live := liveEntity(uuid.New(), "A", "a")
	live.Attributes = withDoc(node("a", "one") + "," + node("b", "two"))
	o := &stagedChangeset{header: changeset.Header{ID: 42}}

	if c := o.changesFor(&stagedEntity{Ent: live}); c.Page != "new" || c.Label != "#42" {
		t.Errorf("create = %+v", c)
	}
	if c := o.changesFor(&stagedEntity{Ent: live, Live: live, Delete: true}); c.Page != "" || len(c.Nodes) != 0 {
		t.Errorf("delete = %+v", c)
	}
	staged := *live
	staged.Attributes = withDoc(node("a", "one") + "," + node("b", "TWO") + "," + node("c", "three"))
	c := o.changesFor(&stagedEntity{Ent: &staged, Live: live})
	if c.Page != "" || c.Nodes["b"] != "changed" || c.Nodes["c"] != "new" || len(c.Nodes) != 2 {
		t.Errorf("doc update = %+v, want b changed and c new", c)
	}
	sameDoc := *live
	sameDoc.Attributes = withDoc(node("a", "one") + "," + node("b", "two"))
	if c := o.changesFor(&stagedEntity{Ent: &sameDoc, Live: live}); c.Page != "changed" {
		t.Errorf("update without node changes = %+v, want the whole page changed", c)
	}
}

func TestChangesetPreviewNoticeIsScriptless(t *testing.T) {
	body := string(previewNotice("<b>x</b>"))
	if strings.Contains(body, "<script") || strings.Contains(body, "<b>x") {
		t.Errorf("notice not inert: %s", body)
	}
}

// A parent published by an earlier run is found through the source-key table,
// as the publish does, and an item that cannot be shown never previews as
// another page.
func TestChangesetPreviewParentFromEarlierRunAndDroppedItem(t *testing.T) {
	parentID := uuid.New()
	live := map[uuid.UUID]*entity.Entity{parentID: liveEntity(parentID, "Docs", "docs")}
	o := newStagedChangeset(context.Background(), changeset.Header{ID: 1, RunID: testRunID, Plugin: "p"}, []changeset.StoredItem{
		item(1, changeset.OpUpsert, "", "first", `{"schema":"page","name":"First"}`),
		item(2, changeset.OpUpsert, "", "install", `{"schema":"page","name":"Install","parent_key":"docs"}`),
		item(3, changeset.OpUpsert, "", "lost", `{"schema":"page","name":"Lost","parent_key":"nowhere"}`),
	}, testSource(live, map[string]uuid.UUID{"docs": parentID}))
	se := o.pick(2)
	if se == nil || se.Seq != 2 || se.Ent.ParentID == nil || *se.Ent.ParentID != parentID {
		t.Fatalf("pick(2) = %+v, want the child under the earlier run's parent", se)
	}
	if got := o.pick(3); got != nil {
		t.Errorf("pick(3) = seq %d, a dropped item must not show another page", got.Seq)
	}
	if got := o.pick(0); got == nil || got.Seq != 1 {
		t.Errorf("pick(0) = %+v, want the first item", got)
	}
}

// The source-key lookups for N items are batched: one read per kind, none per
// item.
func TestChangesetPreviewBatchesMappingLookups(t *testing.T) {
	parentID := uuid.New()
	live := map[uuid.UUID]*entity.Entity{parentID: liveEntity(parentID, "Docs", "docs")}
	for _, n := range []int{10, 300} {
		var batches, singles int
		src := testSource(live, map[string]uuid.UUID{"docs": parentID})
		src.mapped = func(context.Context, string, changeset.Kind, string) (uuid.UUID, bool, error) {
			singles++
			return uuid.Nil, false, nil
		}
		src.mappedMany = func(_ context.Context, _ string, _ changeset.Kind, keys []string) (map[string]uuid.UUID, error) {
			batches++
			out := map[string]uuid.UUID{}
			for _, k := range keys {
				if k == "docs" {
					out[k] = parentID
				}
			}
			return out, nil
		}
		var items []changeset.StoredItem
		for i := 1; i <= n; i++ {
			items = append(items, item(i, changeset.OpUpsert, "", fmt.Sprintf("k%d", i), `{"schema":"page","name":"P","parent_key":"docs"}`))
		}
		o := newStagedChangeset(context.Background(), changeset.Header{ID: 1, RunID: testRunID, Plugin: "p"}, items, src)
		if len(o.items) != n || batches != 1 || singles != 0 {
			t.Errorf("n=%d: items=%d batches=%d singles=%d, want all items, 1 batch, 0 single lookups", n, len(o.items), batches, singles)
		}
	}
}

// Stepping through items reuses the built set for the same changeset version.
func TestChangesetPreviewCachesStagedSet(t *testing.T) {
	var c stagedCache
	h := changeset.Header{ID: 5, RunID: testRunID, ItemCount: 3, ExpiresAt: time.Now().Add(time.Hour)}
	builds := 0
	build := func() (*stagedChangeset, error) { builds++; return &stagedChangeset{header: h}, nil }
	a, _ := c.get(h, build)
	b, _ := c.get(h, build)
	if builds != 1 || a != b {
		t.Fatalf("builds = %d, want the second load served from the cache", builds)
	}
	h.ItemCount = 4 // more items staged: a new version
	if _, _ = c.get(h, build); builds != 2 {
		t.Errorf("builds = %d, a changed item count must rebuild", builds)
	}
	for id := int64(10); id < 30; id++ {
		hh := h
		hh.ID = id
		_, _ = c.get(hh, build)
	}
	if len(c.entries) > stagedCacheEntries {
		t.Errorf("cache holds %d entries, want at most %d", len(c.entries), stagedCacheEntries)
	}
}
