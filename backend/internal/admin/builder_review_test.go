package admin

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/user"
)

func decodeSave(t *testing.T, body string) saveOut {
	t.Helper()
	var out saveOut
	if err := jsonx.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("reply %q: %v", body, err)
	}
	return out
}

func TestBuilderReviewSaveIsStaged(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author := users[user.RoleAuthor]
	tok := c.freshToken(author.ID)

	t.Run("publishing a draft saves it and sends it for review", func(t *testing.T) {
		e := builderRulesPage(c, author, entity.StatusDraft)
		rec := builderRulesSave(c, tok, e, "publish")
		out := decodeSave(t, rec.Body.String())
		if rec.Code != http.StatusOK || out.Status != "pending_review" || !strings.Contains(out.Note, "Sent for review") {
			t.Fatalf("publish: %d %s", rec.Code, rec.Body)
		}
		if st := builderRulesStatus(c, e); st != entity.StatusDraft {
			t.Errorf("status %s, want draft", st)
		}
		if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Name != "Named page" {
			t.Errorf("name %q: the draft was not saved before it was sent", got.Name)
		}
	})

	t.Run("saving a published page stages it and writes nothing", func(t *testing.T) {
		e := builderRulesPage(c, author, entity.StatusPublished)
		before := builderRulesVersion(c, e)
		rec := builderRulesSave(c, tok, e, "save")
		out := decodeSave(t, rec.Body.String())
		if rec.Code != http.StatusOK || out.Status != "pending_review" || !strings.Contains(out.Note, "Sent for review") {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		if got := builderRulesVersion(c, e); got != before {
			t.Errorf("version %d, want %d: the live row changed", got, before)
		}
	})

	t.Run("the no-JS outline answers with a flash", func(t *testing.T) {
		e := builderRulesPage(c, author, entity.StatusDraft)
		path := "/admin/entities/" + e.ID.String() + "/edit"
		f := outlineForm(t, c.do("GET", path+"?outline=1", tok, nil, nil).Body.String())
		f.Set("_op", "publish")
		f.Set("name", "Named page")
		rec := c.do("POST", path, tok, f, nil)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("outline publish: %d %.300s", rec.Code, rec.Body)
		}
		has(t, c.next(rec, path), "Saved. Sent for review. It goes live when a reviewer approves it.")
		if st := builderRulesStatus(c, e); st != entity.StatusDraft {
			t.Errorf("status %s, want draft", st)
		}
	})
}

func TestBuilderReviewShell(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, _ := newReviewEditorRig(t, rv)
	author := users[user.RoleAuthor]
	tok := c.freshToken(author.ID)

	draft := builderRulesPage(c, author, entity.StatusDraft)
	for name, q := range map[string]string{"shell": "", "outline": "?outline=1"} {
		body := c.do("GET", "/admin/entities/"+draft.ID.String()+"/edit"+q, tok, nil, nil).Body.String()
		has(t, body, "Send for review", "Save draft")
		lacks(t, body, ">Publish<", "data-hm-review-placeholder")
		_ = name
	}

	pub := builderRulesPage(c, author, entity.StatusPublished)
	body := c.do("GET", "/admin/entities/"+pub.ID.String()+"/edit", tok, nil, nil).Body.String()
	has(t, body, "Send for review")
	lacks(t, body, ">Update<")

	// The requester's waiting change is shown over the live page, with the banner.
	rv.pending = map[string]PendingEdit{pub.ID.String(): {
		ID: 5, EntityID: pub.ID.String(), Requester: author.ID.String(), Since: time.Now(),
		Payload: &entity.Entity{Name: pub.Name, Slug: pub.Slug, Attributes: []byte(`{"title":"T","page_doc":{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Waiting heading","level":2}}]}]}}`)},
	}}
	body = c.do("GET", "/admin/entities/"+pub.ID.String()+"/edit?outline=1", tok, nil, nil).Body.String()
	has(t, body, "Your change goes live when approved.", "Waiting heading")
	body = c.do("GET", "/admin/entities/"+pub.ID.String()+"/edit", tok, nil, nil).Body.String()
	has(t, body, `data-banner="review"`, "Your change goes live when approved.", "Waiting heading")
}

func TestBuilderReviewOffIsUnchanged(t *testing.T) {
	c, users := newRolesRig(t)
	for _, role := range []user.Role{user.RoleAuthor, user.RoleContributor} {
		for _, status := range []entity.EntityStatus{entity.StatusDraft, entity.StatusPublished} {
			e := builderRulesPage(c, users[role], status)
			// The page picker lists the other pages in map order: keep only this one so the renders are comparable.
			c.ents.mu.Lock()
			for id, x := range c.ents.ents {
				if id != e.ID && x.SchemaID == c.page.ID {
					delete(c.ents.ents, id)
				}
			}
			c.ents.mu.Unlock()
			tok := c.freshToken(users[role].ID)
			for _, q := range []string{"", "?outline=1"} {
				path := "/admin/entities/" + e.ID.String() + "/edit" + q
				c.a.deps.Review = nil
				want := normalizePage(c.do("GET", path, tok, nil, nil).Body.String())
				c.a.deps.Review = &fakeReviewFlow{}
				if got := normalizePage(c.do("GET", path, tok, nil, nil).Body.String()); got != want {
					t.Errorf("%s %s %s: the builder differs with review off", role, status, q)
				}
			}
		}
	}
}

func TestBuilderReviewOwnerPublishesDirectly(t *testing.T) {
	rv := &fakeReviewFlow{}
	c, users, _ := newReviewEditorRig(t, rv)
	owner := users[user.RoleAuthor] // the store is not holding: the person publishes
	e := builderRulesPage(c, owner, entity.StatusDraft)
	rec := builderRulesSave(c, c.freshToken(owner.ID), e, "publish")
	out := decodeSave(t, rec.Body.String())
	if rec.Code != http.StatusOK || out.Status != "published" {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body)
	}
}
