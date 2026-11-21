package admin

import (
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// Terms stay with owners and editors (entity.terms.update): an author saving the form changes none, and the
// picker is not offered.
func TestAuthorSaveLeavesTermsAlone(t *testing.T) {
	c, users := newRolesRig(t)
	author := users[user.RoleAuthor]
	e := c.entityBy(author, entity.StatusDraft)
	path := "/admin/entities/" + e.ID.String()

	lacks(t, c.get(t, c.freshToken(author.ID), path, ""), `name="_terms"`, `name="term"`)

	form := url.Values{"name": {e.Name}, "slug": {e.Slug}, "status": {"draft"}, "attr.summary": {"s"}, "_terms": {"1"}, "term": {fid(31).String()}}
	if rec := c.do("POST", path, c.freshToken(author.ID), form, nil); rec.Code != 303 {
		t.Fatalf("author save = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := c.terms.attach[e.ID]; len(got) != 0 {
		t.Errorf("an author set terms: %v", got)
	}

	if rec := c.do("POST", path, "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("owner save = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := c.terms.attach[e.ID]; len(got) != 1 || got[0] != fid(31) {
		t.Errorf("owner terms = %v, want the ticked one", got)
	}
}
