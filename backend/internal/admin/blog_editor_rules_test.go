package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// blogRulesPost adds a post owned by owner and returns it with the editor path.
func blogRulesPost(c *contentRig, owner *user.User, status entity.EntityStatus) (*entity.Entity, string) {
	sc := c.ents.addSchema(20, "Post", "post", postSchema, `{"url_pattern":"/blog/{slug}"}`, true)
	c.ents.mu.Lock()
	defer c.ents.mu.Unlock()
	c.ents.nextID++
	e := c.ents.addEntity(c.ents.nextID, sc, "Rules post", "rules-post", status, `{"content":"<p>Hello</p>"}`, t0)
	e.CreatedBy = &owner.ID
	return e, "/admin/entities/" + e.ID.String() + "/edit"
}

func TestBlogEditorRules(t *testing.T) {
	c, users := newRolesRig(t)
	contrib, author := users[user.RoleContributor], users[user.RoleAuthor]

	t.Run("a contributor on their own draft gets a placeholder instead of Publish", func(t *testing.T) {
		_, path := blogRulesPost(c, contrib, entity.StatusDraft)
		body := c.get(t, c.freshToken(contrib.ID), path, "")
		has(t, body, `<span>Save draft</span>`, `<span>Send for review</span>`, `aria-disabled="true"`, `data-hm-review-placeholder`, `id="be-review-why"`, `An editor or reviewer publishes drafts.`)
		lacks(t, body, `value="publish"`, `data-hm-be-schedule`, `data-hm-be-proxy="schedule"`, `data-hm-be-sched`, `name="schedule"`, `value="scheduled"`)
	})

	t.Run("an author on their own draft keeps Publish and Schedule", func(t *testing.T) {
		_, path := blogRulesPost(c, author, entity.StatusDraft)
		body := c.get(t, c.freshToken(author.ID), path, "")
		has(t, body, `value="publish"`, `data-hm-be-schedule`, `name="schedule"`)
		lacks(t, body, `Send for review`)
	})

	t.Run("a contributor publishing is saved as a draft with a note", func(t *testing.T) {
		e, path := blogRulesPost(c, contrib, entity.StatusDraft)
		form := parseForm(t, c.get(t, c.freshToken(contrib.ID), path, ""))
		form.Set("_op", "publish")
		form.Set("status", "draft")
		rec := c.do("POST", path, c.freshToken(contrib.ID), form, nil)
		if rec.Code != 303 && rec.Code != 302 {
			t.Fatalf("POST = %d\n%s", rec.Code, rec.Body.String())
		}
		got, _ := c.ents.GetEntity(t.Context(), e.ID)
		if got.Status != entity.StatusDraft {
			t.Errorf("status = %s, want draft", got.Status)
		}
		has(t, c.next(rec, path), "The status could not be set")
	})

	t.Run("an author cannot save or autosave another user's post", func(t *testing.T) {
		e, path := blogRulesPost(c, contrib, entity.StatusDraft)
		tok := c.freshToken(author.ID)
		form := url.Values{"name": {"Changed"}, "version": {"1"}, "_op": {"save"}}
		if rec := c.do("POST", path, tok, form, nil); rec.Code != 403 {
			t.Errorf("save = %d, want 403", rec.Code)
		}
		rec := c.do("POST", path+"/autosave", tok, url.Values{"name": {"Changed"}, "version": {"1"}}, nil)
		if rec.Code != 403 || !strings.Contains(rec.Header().Get("Content-Type"), "json") || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("autosave = %d %q, want JSON 403", rec.Code, rec.Body.String())
		}
		if got, _ := c.ents.GetEntity(t.Context(), e.ID); got.Name != "Rules post" {
			t.Errorf("name = %q, the refused save changed it", got.Name)
		}
	})
}
