package admin

import (
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
