package admin

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

func TestBlogEditorReviewButtons(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author, contrib := users[user.RoleAuthor], users[user.RoleContributor]

	t.Run("an author's draft sends for review instead of publishing", func(t *testing.T) {
		_, path := blogRulesPost(c, author, entity.StatusDraft)
		body := c.get(t, c.freshToken(author.ID), path, "")
		has(t, body, `<span>Save draft</span>`, `<span>Send for review</span>`, `value="publish"`, `data-hm-review="1"`)
		lacks(t, body, `<span>Publish</span>`, `data-hm-review-placeholder`)
		// Secondary before primary: Save draft comes first in source order.
		if strings.Index(body, `<span>Save draft</span>`) > strings.Index(body, `<span>Send for review</span>`) {
			t.Error("Save draft must precede Send for review")
		}
	})

	t.Run("a published post sends for review instead of updating", func(t *testing.T) {
		_, path := blogRulesPost(c, author, entity.StatusPublished)
		body := c.get(t, c.freshToken(author.ID), path, "")
		has(t, body, `<span>Send for review</span>`, `data-hm-review="1"`)
		lacks(t, body, `<span>Update</span>`)
	})

	t.Run("a contributor's own draft gets a real button while review applies", func(t *testing.T) {
		_, path := blogRulesPost(c, contrib, entity.StatusDraft)
		body := c.get(t, c.freshToken(contrib.ID), path, "")
		has(t, body, `<span>Send for review</span>`, `value="publish"`)
		lacks(t, body, `data-hm-review-placeholder`)
	})

	t.Run("saving a published post stages it and the next page shows the banner", func(t *testing.T) {
		e, path := blogRulesPost(c, author, entity.StatusPublished)
		tok := c.freshToken(author.ID)
		form := parseForm(t, c.get(t, tok, path, ""))
		form.Set("name", "Edited title")
		form.Set("_op", "save")
		rec := c.do("POST", path, tok, form, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != path {
			t.Fatalf("POST = %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		has(t, c.next(rec, path), "Sent for review. It goes live when a reviewer approves it.")
		if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Name != "Rules post" {
			t.Errorf("name = %q, the live row changed", got.Name)
		}

		rv.pending = map[string]PendingEdit{e.ID.String(): {
			ID: 9, EntityID: e.ID.String(), Requester: author.ID.String(), Since: time.Now(),
			Payload: &entity.Entity{Name: "Edited title", Slug: e.Slug, Attributes: e.Attributes},
		}}
		body := c.get(t, tok, path, "")
		has(t, body, "Your change goes live when approved.", `>Edited title</textarea>`)
	})

	t.Run("publishing a draft stages it with the note", func(t *testing.T) {
		e, path := blogRulesPost(c, author, entity.StatusDraft)
		tok := c.freshToken(author.ID)
		form := parseForm(t, c.get(t, tok, path, ""))
		form.Set("_op", "publish")
		rec := c.do("POST", path, tok, form, nil)
		if rec.Code != 303 {
			t.Fatalf("POST = %d\n%s", rec.Code, rec.Body.String())
		}
		next := c.next(rec, path)
		has(t, next, "Saved. Sent for review.")
		lacks(t, next, "could not be set")
		if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Status != entity.StatusDraft {
			t.Errorf("status = %s, want draft", got.Status)
		}
	})
}

func TestBlogEditorReviewOffIsUnchanged(t *testing.T) {
	c, users := newRolesRig(t)
	for _, role := range []user.Role{user.RoleAuthor, user.RoleContributor} {
		for _, status := range []entity.EntityStatus{entity.StatusDraft, entity.StatusPublished} {
			_, path := blogRulesPost(c, users[role], status)
			tok := c.freshToken(users[role].ID)
			c.a.deps.Review = nil
			want := c.get(t, tok, path, "")
			c.a.deps.Review = &fakeReviewFlow{}
			if got := c.get(t, tok, path, ""); got != want {
				t.Errorf("%s %s: the editor differs with review off", role, status)
			}
		}
	}
}
