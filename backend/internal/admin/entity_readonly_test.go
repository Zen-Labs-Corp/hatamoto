package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// readOnlyOthers stores a draft by the editor-less owner in each editor kind: details form, blog editor, builder.
func readOnlyOthers(c *contentRig, owner *user.User) map[string]*entity.Entity {
	post := c.ents.addSchema(20, "Post", "post", `{"type":"object","properties":{"content":{"type":"string"}}}`, `{"url_pattern":"/blog/{slug}"}`, true)
	mk := func(sc *entity.EntitySchema, attrs string) *entity.Entity {
		c.ents.mu.Lock()
		defer c.ents.mu.Unlock()
		c.ents.nextID++
		e := c.ents.addEntity(c.ents.nextID, sc, "Other "+sc.Slug, "other-"+sc.Slug, entity.StatusDraft, attrs, t0)
		e.CreatedBy = &owner.ID
		return e
	}
	return map[string]*entity.Entity{
		"service": mk(c.service, `{"summary":"s"}`),
		"post":    mk(post, `{"content":"hello"}`),
		"page":    mk(c.page, `{"body":"<p>Hi</p>"}`),
	}
}

func readOnlyNoControls(t *testing.T, path, body string) {
	t.Helper()
	if strings.Contains(body, `action="/admin/entities`) || strings.Contains(body, `data-hm-post="/admin/entities`) {
		t.Errorf("%s: read-only page posts to an entity", path)
	}
	for _, bad := range []string{"Save", "Publish"} {
		if strings.Contains(body, bad) {
			t.Errorf("%s: read-only page contains %q", path, bad)
		}
	}
}

func TestReadOnlyOthersContent(t *testing.T) {
	c, users := newRolesRig(t)
	author := users[user.RoleAuthor]
	tok := c.freshToken(author.ID)
	for kind, e := range readOnlyOthers(c, users[user.RoleContributor]) {
		for _, suffix := range []string{"", "/edit"} {
			path := "/admin/entities/" + e.ID.String() + suffix
			body := c.get(t, tok, path, "")
			has(t, body, "Written by contributor Sam", "Authors edit their own content.", e.Name)
			readOnlyNoControls(t, kind+" "+path, body)
		}
	}
}

func TestReadOnlyOwnPublished(t *testing.T) {
	c, users := newRolesRig(t)
	contrib := users[user.RoleContributor]
	e := c.entityBy(contrib, entity.StatusPublished)
	body := c.get(t, c.freshToken(contrib.ID), "/admin/entities/"+e.ID.String(), "")
	has(t, body, "You cannot change this entry", "It is no longer a draft. An editor or reviewer can change it.")
	readOnlyNoControls(t, "own", body)
}

func TestReadOnlyUnknownWriter(t *testing.T) {
	c, users := newRolesRig(t)
	e := c.entityBy(nil, entity.StatusDraft)
	e.Name, e.Slug = "Orphan entry", "orphan-entry" // the golden needs a stable name
	body := c.get(t, c.freshToken(users[user.RoleAuthor].ID), "/admin/entities/"+e.ID.String(), "entity-readonly")
	has(t, body, "Written by another user")
	if strings.Contains(body, "@example.com") {
		t.Error("the read-only page shows an email")
	}
}

func TestReadOnlyEditorKeepsForm(t *testing.T) {
	c, users := newRolesRig(t)
	e := c.entityBy(users[user.RoleContributor], entity.StatusDraft)
	body := c.get(t, "owner-token", "/admin/entities/"+e.ID.String(), "")
	has(t, body, "<form")
}
