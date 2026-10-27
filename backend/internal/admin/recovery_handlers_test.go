package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type recKey struct{ user, entity uuid.UUID }

type fakeRecovery struct {
	mu   sync.Mutex
	rows map[recKey]RecoveryCopy
}

func newFakeRecovery() *fakeRecovery { return &fakeRecovery{rows: map[recKey]RecoveryCopy{}} }

func (f *fakeRecovery) Put(_ context.Context, u, e uuid.UUID, c RecoveryCopy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.SavedAt = time.Now()
	f.rows[recKey{u, e}] = c
	return nil
}

func (f *fakeRecovery) Get(_ context.Context, u, e uuid.UUID) (*RecoveryCopy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.rows[recKey{u, e}]; ok {
		return &c, nil
	}
	return nil, nil
}

func (f *fakeRecovery) Delete(_ context.Context, u, e uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, recKey{u, e})
	return nil
}

func (f *fakeRecovery) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// noToken keeps the Sec-Fetch metadata and drops the token, so a 403 is the token's alone.
var noToken = map[string]string{"X-CSRF-Token": ""}

func TestRecoveryHandlers(t *testing.T) {
	t.Parallel()

	const blogBody = `{"content":"<p>mine</p>","version":4,"name":"T","excerpt":"E","featured_image":"/m/a.jpg"}`
	const builderBody = `{"content":"{\"v\":2,\"nodes\":[]}","version":1,"name":"P","excerpt":"ignored"}`

	type kind struct {
		name string
		rig  func(t *testing.T) (g *rig, base string, id uuid.UUID, setStore func(RecoveryStore))
		body string
	}
	kinds := []kind{
		{"blog", func(t *testing.T) (*rig, string, uuid.UUID, func(RecoveryStore)) {
			r := newBlogRig(t)
			return r.rig, "/admin/entities/" + r.ent.ID.String() + "/edit/recovery", r.ent.ID,
				func(s RecoveryStore) { r.a.deps.Content.Recovery = s }
		}, blogBody},
		{"builder", func(t *testing.T) (*rig, string, uuid.UUID, func(RecoveryStore)) {
			r := newBuilderRenderRig(t)
			return r.rig, "/admin/entities/" + r.page.ID.String() + "/builder/recovery", r.page.ID,
				func(s RecoveryStore) { r.a.deps.Content.Recovery = s }
		}, builderBody},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			t.Parallel()
			g, base, id, setStore := k.rig(t)
			g.realTokens() // per-session tokens, so a token from another session can be refused
			store := newFakeRecovery()
			setStore(store)
			owner := uuid.Nil
			// The owner's id is the session user: find it from the first write.
			if rec := g.postJSON(base, "owner-token", k.body, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("put: %d %s", rec.Code, rec.Body)
			}
			if store.count() != 1 {
				t.Fatalf("rows = %d, want 1", store.count())
			}
			var c RecoveryCopy
			for key, v := range store.rows {
				owner, c = key.user, v
				if key.entity != id {
					t.Errorf("stored for entity %v, want %v", key.entity, id)
				}
			}
			if c.BaseVersion != map[string]int{"blog": 4, "builder": 1}[k.name] || c.Name == "" {
				t.Errorf("stored copy = %+v", c)
			}
			if k.name == "builder" && (c.Excerpt != "" || c.FeaturedImage != "") {
				t.Errorf("builder copy kept blog fields: %+v", c)
			}

			// A user id in the body is ignored: the row stays the session user's.
			forged := strings.Replace(k.body, "{", `{"user_id":"`+uuid.NewString()+`","user":"`+uuid.NewString()+`",`, 1)
			if rec := g.postJSON(base, "owner-token", forged, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("forged put: %d %s", rec.Code, rec.Body)
			}
			if store.count() != 1 {
				t.Errorf("a posted user id created a row: %d", store.count())
			}
			// Another user's write is a separate row.
			if rec := g.postJSON(base, "editor-token", k.body, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("editor put: %d %s", rec.Code, rec.Body)
			}
			if store.count() != 2 {
				t.Fatalf("rows = %d, want 2", store.count())
			}

			// Refusals.
			if rec := g.postJSON(base, "owner-token", k.body, noToken); rec.Code != http.StatusForbidden {
				t.Errorf("no csrf: %d", rec.Code)
			}
			// The editor's session presenting the owner's token is refused.
			if rec := g.postJSON(base, "editor-token", k.body, map[string]string{"X-CSRF-Token": g.tokenFor("owner-token")}); rec.Code != http.StatusForbidden {
				t.Errorf("owner's token on the editor's session: %d", rec.Code)
			}
			if rec := g.postJSON(base+"/discard", "editor-token", ``, map[string]string{"X-CSRF-Token": g.tokenFor("owner-token")}); rec.Code != http.StatusForbidden {
				t.Errorf("owner's token on the editor's discard: %d", rec.Code)
			}
			if rec := g.postJSON(base, "", k.body, nil); rec.Code == http.StatusNoContent {
				t.Error("stored without a session")
			}
			if rec := g.postJSON(base, "owner-token", `{`, nil); rec.Code != http.StatusBadRequest {
				t.Errorf("bad json: %d", rec.Code)
			}
			if rec := g.postJSON(base, "owner-token", `{"content":"x","name":"`+strings.Repeat("n", 256)+`"}`, nil); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("long name: %d", rec.Code)
			}
			huge := `{"content":"` + strings.Repeat("a", 5<<20) + `"}`
			if rec := g.postJSON(base, "owner-token", huge, nil); rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("oversize: %d", rec.Code)
			}
			if rec := g.postJSON(strings.Replace(base, id.String(), uuid.NewString(), 1), "owner-token", k.body, nil); rec.Code != http.StatusNotFound {
				t.Errorf("unknown entity: %d", rec.Code)
			}
			if k.name == "builder" {
				for _, bad := range []string{`{"content":"not json"}`, `{"content":"[1]"}`, `{"content":"null"}`} {
					if rec := g.postJSON(base, "owner-token", bad, nil); rec.Code != http.StatusUnprocessableEntity {
						t.Errorf("builder content %s: %d", bad, rec.Code)
					}
				}
			}
			if store.count() != 2 {
				t.Errorf("a refused write changed rows: %d", store.count())
			}

			// Discard deletes the caller's row only.
			if rec := g.postJSON(base+"/discard", "owner-token", ``, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("discard: %d %s", rec.Code, rec.Body)
			}
			if got, _ := store.Get(context.Background(), owner, id); got != nil {
				t.Error("discard kept the caller's copy")
			}
			if store.count() != 1 {
				t.Errorf("rows after discard = %d, want the editor's", store.count())
			}
			if rec := g.postJSON(base+"/discard", "owner-token", ``, noToken); rec.Code != http.StatusForbidden {
				t.Errorf("discard no csrf: %d", rec.Code)
			}

			// No store: both routes answer 204 and keep nothing.
			setStore(nil)
			if rec := g.postJSON(base, "owner-token", k.body, nil); rec.Code != http.StatusNoContent {
				t.Errorf("nil store put: %d", rec.Code)
			}
			if rec := g.postJSON(base+"/discard", "owner-token", ``, nil); rec.Code != http.StatusNoContent {
				t.Errorf("nil store discard: %d", rec.Code)
			}
		})
	}
}

// seedBoth stores a copy for the owner and the editor of entity id.
func seedBoth(t *testing.T, s *fakeRecovery, owner, editor, id uuid.UUID) {
	t.Helper()
	for _, u := range []uuid.UUID{owner, editor} {
		if err := s.Put(context.Background(), u, id, RecoveryCopy{Content: "x"}); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fakeRecovery) has(u, e uuid.UUID) bool {
	c, _ := f.Get(context.Background(), u, e)
	return c != nil
}

func TestBlogSaveDropsOnlyThePublishersCopy(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	store := newFakeRecovery()
	r.a.deps.Content.Recovery = store
	seedBoth(t, store, r.owner.ID, r.editor.ID, r.ent.ID)

	// A refused save (validation or stale version) keeps the copy.
	stale := r.form(t)
	stale.Set("version", "99")
	stale.Set("_op", "save")
	if rec := r.post(r.path(), stale); rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d", rec.Code)
	}
	if !store.has(r.owner.ID, r.ent.ID) {
		t.Fatal("a refused save dropped the copy")
	}

	f := r.form(t)
	f.Set("_op", "save")
	if rec := r.post(r.path(), f); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	if store.has(r.owner.ID, r.ent.ID) {
		t.Error("the publisher's own copy survived a save")
	}
	if !store.has(r.editor.ID, r.ent.ID) {
		t.Error("another user's copy was deleted")
	}
}

func TestBuilderSaveDropsCopyButAutosaveKeepsIt(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	store := newFakeRecovery()
	r.a.deps.Content.Recovery = store
	seedBoth(t, store, r.owner.ID, r.editor.ID, r.page.ID)

	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "autosave", "")); code != 200 || !out.OK {
		t.Fatalf("autosave: %d %+v", code, out)
	}
	if !store.has(r.owner.ID, r.page.ID) {
		t.Fatal("a draft autosave dropped the recovery copy")
	}
	if code, out := r.save(t, r.page, saveBody(2, saveDoc, "save", "")); code != 200 || !out.OK {
		t.Fatalf("save: %d %+v", code, out)
	}
	if store.has(r.owner.ID, r.page.ID) {
		t.Error("the saver's own copy survived a builder save")
	}
	if !store.has(r.editor.ID, r.page.ID) {
		t.Error("another user's copy was deleted")
	}

	// A refused save (stale version) keeps the copy.
	if err := store.Put(context.Background(), r.owner.ID, r.page.ID, RecoveryCopy{Content: "y"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := r.save(t, r.page, saveBody(1, saveDoc, "save", "")); code != http.StatusConflict {
		t.Fatalf("stale save: %d", code)
	}
	if !store.has(r.owner.ID, r.page.ID) {
		t.Error("a refused save dropped the copy")
	}
}

func TestBlogEditorDeliversTheCallersCopyOnly(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	store := newFakeRecovery()
	r.a.deps.Content.Recovery = store
	ctx := context.Background()
	_ = store.Put(ctx, r.editor.ID, r.ent.ID, RecoveryCopy{Content: "<p>EDITORS</p>", BaseVersion: 1})

	body := r.get(t, "owner-token", r.path(), "")
	if strings.Contains(body, `id="hm-be-recovery"`) || strings.Contains(body, "EDITORS") {
		t.Fatal("another user's copy reached the owner's document")
	}
	has(t, body, `data-hm-recovery-url="`+r.path()+`/recovery"`, `data-hm-recovery-discard-url="`+r.path()+`/recovery/discard"`)

	_ = store.Put(ctx, r.owner.ID, r.ent.ID, RecoveryCopy{Content: `</script><b>MINE</b>`, Name: "N", Excerpt: "E", FeaturedImage: "/m/a.jpg", BaseVersion: 3})
	body = r.get(t, "owner-token", r.path(), "")
	m := regexp.MustCompile(`(?s)<script type="application/json" id="hm-be-recovery">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no recovery block for the owner's copy")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatalf("recovery block is not JSON: %v", err)
	}
	if got["content"] != `</script><b>MINE</b>` || got["version"] != float64(3) || got["name"] != "N" ||
		got["excerpt"] != "E" || got["featured_image"] != "/m/a.jpg" {
		t.Errorf("recovery = %v", got)
	}
	if _, err := time.Parse(time.RFC3339, got[recoveryAtKey].(string)); err != nil {
		t.Errorf("save time = %v", got[recoveryAtKey])
	}
	if strings.Count(body, `id="hm-be-recovery"`) != 1 {
		t.Error("recovery block repeated")
	}

	// A save that fails validation re-renders without the copy: delivery is the GET only.
	f := r.form(t)
	f.Set("name", "")
	f.Set("_op", "publish")
	if rec := r.post(r.path(), f); strings.Contains(rec.Body.String(), `id="hm-be-recovery"`) {
		t.Error("a POST re-render carried the copy")
	}
}

func TestBuilderBootCarriesTheCallersCopy(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	store := newFakeRecovery()
	r.a.deps.Content.Recovery = store
	ctx := context.Background()
	_ = store.Put(ctx, r.editor.ID, r.ent.ID, RecoveryCopy{Content: `{"v":2,"nodes":[]}`})

	boot := func() (m struct {
		Recovery map[string]any    `json:"recovery"`
		URLs     map[string]string `json:"urls"`
	}) {
		body := builderPage(t, r, builderOnlyDoc)
		if err := json.Unmarshal([]byte(bootRx.FindStringSubmatch(body)[1]), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := boot(); m.Recovery != nil {
		t.Fatalf("another user's copy in the boot: %v", m.Recovery)
	}
	base := "/admin/entities/" + r.ent.ID.String() + "/builder/recovery"
	if m := boot(); m.URLs["recovery"] != base || m.URLs["recoveryDiscard"] != base+"/discard" {
		t.Errorf("urls = %v", m.URLs)
	}

	_ = store.Put(ctx, r.owner.ID, r.ent.ID, RecoveryCopy{Content: `{"v":2,"nodes":[]}`, Name: "P", Excerpt: "no", BaseVersion: 5})
	m := boot()
	if m.Recovery["content"] != `{"v":2,"nodes":[]}` || m.Recovery["version"] != float64(5) || m.Recovery["name"] != "P" {
		t.Errorf("recovery = %v", m.Recovery)
	}
	if _, has := m.Recovery["excerpt"]; has {
		t.Error("builder copy carries excerpt")
	}
	if _, err := time.Parse(time.RFC3339, m.Recovery[recoveryAtKey].(string)); err != nil {
		t.Errorf("save time = %v", m.Recovery[recoveryAtKey])
	}
}
