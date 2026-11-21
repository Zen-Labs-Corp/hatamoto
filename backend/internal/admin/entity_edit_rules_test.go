package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

func editRulesForm(e *entity.Entity, status string, extra url.Values) url.Values {
	f := url.Values{"name": {e.Name}, "slug": {e.Slug}, "status": {status}, "attr.summary": {"s"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestEntityEditRulesContributorForm(t *testing.T) {
	c, users := newRolesRig(t)
	contrib := users[user.RoleContributor]
	e := c.entityBy(contrib, entity.StatusDraft)
	body := c.get(t, c.freshToken(contrib.ID), "/admin/entities/"+e.ID.String(), "")
	for _, bad := range []string{`value="published"`, `value="scheduled"`, `name="schedule"`, `name="unpublish_at"`} {
		if strings.Contains(body, bad) {
			t.Errorf("contributor form contains %s", bad)
		}
	}
	has(t, body, `value="draft"`, "An editor or reviewer publishes drafts.")
}

func TestEntityEditRulesOwnerFormKeepsSchedule(t *testing.T) {
	c, users := newRolesRig(t)
	e := c.entityBy(users[user.RoleContributor], entity.StatusDraft)
	body := c.get(t, "owner-token", "/admin/entities/"+e.ID.String(), "")
	has(t, body, `value="published"`, `value="scheduled"`, `name="schedule"`)
}

func TestEntityEditRulesContributorCannotPublish(t *testing.T) {
	c, users := newRolesRig(t)
	contrib := users[user.RoleContributor]
	tok := c.freshToken(contrib.ID)
	for name, form := range map[string]func(*entity.Entity) url.Values{
		"published": func(e *entity.Entity) url.Values { return editRulesForm(e, "published", nil) },
		"scheduled": func(e *entity.Entity) url.Values {
			return editRulesForm(e, "scheduled", url.Values{"schedule": {"2099-01-02T10:00"}})
		},
	} {
		e := c.entityBy(contrib, entity.StatusDraft)
		path := "/admin/entities/" + e.ID.String()
		rec := c.do("POST", path, tok, form(e), nil)
		if rec.Code != 303 {
			t.Fatalf("%s: POST = %d\n%s", name, rec.Code, rec.Body.String())
		}
		if got := c.ents.ents[e.ID].Status; got != entity.StatusDraft {
			t.Errorf("%s: status = %s, want draft", name, got)
		}
		cookies := rec.Result().Cookies()
		req := c.do("GET", path, tok, nil, map[string]string{"Cookie": "hatamoto_session=" + tok + "; " + cookies[len(cookies)-1].Name + "=" + cookies[len(cookies)-1].Value})
		has(t, req.Body.String(), "The status could not be set: you cannot publish this entry")
	}
}

func TestEntityEditRulesAuthor(t *testing.T) {
	c, users := newRolesRig(t)
	author := users[user.RoleAuthor]
	tok := c.freshToken(author.ID)
	other := c.entityBy(users[user.RoleContributor], entity.StatusDraft)
	if rec := c.do("POST", "/admin/entities/"+other.ID.String(), tok, editRulesForm(other, "draft", nil), nil); rec.Code != 403 {
		t.Errorf("author save on another's entry = %d, want 403", rec.Code)
	}
	own := c.entityBy(author, entity.StatusDraft)
	if rec := c.do("POST", "/admin/entities/"+own.ID.String(), tok, editRulesForm(own, "published", nil), nil); rec.Code != 303 {
		t.Fatalf("author publish own = %d", rec.Code)
	}
	if got := c.ents.ents[own.ID].Status; got != entity.StatusPublished {
		t.Errorf("status = %s, want published", got)
	}
}
