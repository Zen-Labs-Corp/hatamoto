package server

import (
	"context"
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
)

func stagedOthersFixture(t *testing.T) (*stagedChangeset, uuid.UUID) {
	t.Helper()
	menuID := uuid.New()
	src := testSource(map[uuid.UUID]*entity.Entity{}, nil)
	src.menuBySlug = func(_ context.Context, slug string) (uuid.UUID, bool) { return menuID, slug == "main" }
	items := []changeset.StoredItem{
		item(1, changeset.OpUpsert, "", "pricing", `{"schema":"page","name":"Pricing","slug":"pricing"}`),
		{Seq: 2, Item: changeset.Item{Kind: changeset.KindMenu, Op: changeset.OpUpsert, Payload: jsontext.Value(
			`{"slug":"main","items":[{"Label":"Plans","LinkType":"entity","Parent":-1},{"Label":"Docs","LinkType":"external","URL":"https://example.com/d","Parent":0}],"entity_keys":{"0":"pricing"}}`)}},
		{Seq: 3, Item: changeset.Item{Kind: changeset.KindRedirect, Op: changeset.OpUpsert, Payload: jsontext.Value(
			`{"source_path":"/old","target":"/new","status":301}`)}},
		{Seq: 4, Item: changeset.Item{Kind: changeset.KindMedia, Op: changeset.OpUpsert, Payload: jsontext.Value(`{"filename":"hero.jpg"}`)}},
		{Seq: 5, Item: changeset.Item{Kind: changeset.KindRedirect, Op: changeset.OpDelete, Payload: jsontext.Value(`{"source_path":"/gone"}`)}},
	}
	return newStagedChangeset(context.Background(), changeset.Header{ID: 9, RunID: testRunID}, items, src), menuID
}

func TestChangesetPreviewMenuStaged(t *testing.T) {
	o, menuID := stagedOthersFixture(t)
	links := &navLinks{snap: entity.DefaultURLSnapshot()}
	tree, ok := o.menuItems(context.Background(), links, menuID)
	if !ok || len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("staged menu tree = %+v ok=%v", tree, ok)
	}
	nav := links.items(tree)
	if nav[0].Label != "Plans" || nav[0].Href != "/pricing" {
		t.Errorf("entity link did not resolve through the staged entity: %+v", nav[0])
	}
	if nav[0].Children[0].Href != "https://example.com/d" {
		t.Errorf("child = %+v", nav[0].Children[0])
	}
	if _, ok := o.menuItems(context.Background(), links, uuid.New()); ok {
		t.Error("a menu the changeset does not touch got staged items")
	}
	if again, _ := o.menuItems(context.Background(), links, menuID); again[0] == tree[0] {
		t.Error("menuItems must return a fresh tree each time")
	}
}

func TestChangesetPreviewRedirectIsShownNotFollowed(t *testing.T) {
	o, _ := stagedOthersFixture(t)
	if rd := o.matchRedirect("/old"); rd == nil || rd.TargetURL != "/new" || rd.StatusCode != 301 {
		t.Fatalf("staged redirect = %+v", rd)
	}
	if o.matchRedirect("/gone") != nil || o.matchRedirect("/other") != nil {
		t.Error("a delete or an unrelated path matched a staged redirect")
	}
	if r := o.redirectBySeq(3); r == nil || r.Source != "/old" {
		t.Errorf("redirectBySeq = %+v", r)
	}
	body := string(redirectNotice("/old", `/x"><script>`, 301))
	if !strings.Contains(body, "Redirects to") || strings.Contains(body, "<script>") || strings.Contains(strings.ToLower(body), "location") {
		t.Errorf("notice: %s", body)
	}
}

func TestChangesetPreviewMediaPlaceholder(t *testing.T) {
	o, _ := stagedOthersFixture(t)
	in := `<img src="/uploads/2026/hero.jpg?x=1"><img src="/uploads/other.jpg"><a href="/hero.jpg">x</a>`
	out := string(o.maskMedia([]byte(in)))
	if strings.Contains(out, `src="/uploads/2026/hero.jpg`) || !strings.Contains(out, "data:image/svg+xml") {
		t.Errorf("staged media not masked: %s", out)
	}
	if !strings.Contains(out, `src="/uploads/other.jpg"`) || !strings.Contains(out, `href="/hero.jpg"`) {
		t.Errorf("unrelated references changed: %s", out)
	}
	if got := (&stagedChangeset{}).maskMedia([]byte(in)); string(got) != in {
		t.Error("a changeset without media must leave the page alone")
	}
}

func TestChangesetPreviewMenuLinksEarlierRunEntity(t *testing.T) {
	menuID, pageID := uuid.New(), uuid.New()
	src := testSource(map[uuid.UUID]*entity.Entity{pageID: liveEntity(pageID, "About", "about")}, map[string]uuid.UUID{"about": pageID})
	src.menuBySlug = func(_ context.Context, slug string) (uuid.UUID, bool) { return menuID, slug == "main" }
	o := newStagedChangeset(context.Background(), changeset.Header{ID: 9, RunID: testRunID}, []changeset.StoredItem{
		{Seq: 1, Item: changeset.Item{Kind: changeset.KindMenu, Op: changeset.OpUpsert, Payload: jsontext.Value(
			`{"slug":"main","items":[{"Label":"About","LinkType":"entity","Parent":-1}],"entity_keys":{"0":"about"}}`)}},
	}, src)
	tree, ok := o.menuItems(context.Background(), &navLinks{snap: entity.DefaultURLSnapshot()}, menuID)
	if !ok || len(tree) != 1 || tree[0].EntityID == nil || *tree[0].EntityID != pageID {
		t.Fatalf("menu entity key from an earlier run not resolved: %+v", tree)
	}
}
