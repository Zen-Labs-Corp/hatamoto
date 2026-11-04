package admin

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestEntityEditorHeaderOffersDuplicate(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities/"+fid(12).String(), "")
	has(t, body, `aria-label="More actions"`, `action="/admin/entities/`+fid(12).String()+`/duplicate"`, "Copy into a new draft")
	lacks(t, c.get(t, "owner-token", "/admin/entities/new?type=service", ""), "/duplicate")

	// The item posts: following it makes a draft and opens it.
	rec := c.post("/admin/entities/"+fid(12).String()+"/duplicate", url.Values{})
	if rec.Code != 303 || c.ents.ents[fid(101)] == nil {
		t.Errorf("post = %d", rec.Code)
	}

	c.a.deps.Content.Duplicator = nil
	lacks(t, c.get(t, "owner-token", "/admin/entities/"+fid(12).String(), ""), "/duplicate")
}

func TestVisualEditorsOfferDuplicate(t *testing.T) {
	t.Parallel()

	form := func(id string) string { return `<form method="post" action="/admin/entities/` + id + `/duplicate">` }

	page := newEditorRig(t)
	page.ent.Attributes = json.RawMessage(builderOnlyDoc)
	pageURL := page.path()
	has(t, page.do("GET", pageURL, "owner-token", nil, nil).Body.String(), form(page.ent.ID.String()), `role="menuitem">Duplicate</button>`)
	page.a.deps.Content.Duplicator = nil
	lacks(t, page.do("GET", pageURL, "owner-token", nil, nil).Body.String(), "/duplicate")

	post := newBlogRig(t)
	body := post.get(t, "owner-token", post.path(), "")
	has(t, body, form(post.ent.ID.String()), `role="menuitem">Duplicate</button>`)
	post.a.deps.Content.Duplicator = nil
	lacks(t, post.get(t, "owner-token", post.path(), ""), "/duplicate")
}
