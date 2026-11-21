package admin

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/user"
)

const builderRulesDoc = `{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2}}]}]}`

// builderRulesPage stores a builder page created by owner in the given status.
func builderRulesPage(c *contentRig, owner *user.User, status entity.EntityStatus) *entity.Entity {
	c.ents.mu.Lock()
	defer c.ents.mu.Unlock()
	c.ents.nextID++
	e := c.ents.addEntity(c.ents.nextID, c.page, "Page "+uuid.NewString()[:6], "pg-"+uuid.NewString()[:8], status,
		`{"title":"T","page_doc":`+builderRulesDoc+`}`, t0)
	e.CreatedBy = &owner.ID
	return e
}

func builderRulesStatus(c *contentRig, e *entity.Entity) entity.EntityStatus {
	c.ents.mu.Lock()
	defer c.ents.mu.Unlock()
	return c.ents.ents[e.ID].Status
}

func builderRulesVersion(c *contentRig, e *entity.Entity) int {
	c.ents.mu.Lock()
	defer c.ents.mu.Unlock()
	return c.ents.ents[e.ID].Version
}

func builderRulesSave(c *contentRig, token string, e *entity.Entity, op string) *httptest.ResponseRecorder {
	body := `{"version":` + strconv.Itoa(e.Version) + `,"doc":` + builderRulesDoc + `,"op":"` + op + `","name":"Named page"}`
	return c.postJSON("/admin/entities/"+e.ID.String()+"/builder/save", token, body, nil)
}

func TestBuilderRulesScreenForContributor(t *testing.T) {
	t.Parallel()

	c, users := newRolesRig(t)
	own := builderRulesPage(c, users[user.RoleContributor], entity.StatusDraft)
	tok := c.freshToken(users[user.RoleContributor].ID)
	path := "/admin/entities/" + own.ID.String() + "/edit"

	for name, q := range map[string]string{"shell": "", "outline": "?outline=1"} {
		body := c.do("GET", path+q, tok, nil, nil).Body.String()
		if strings.Contains(body, ">Publish<") {
			t.Errorf("%s: contributor sees Publish", name)
		}
		has(t, body, "Send for review", "data-hm-review-placeholder", `aria-disabled="true"`, "Save draft")
	}

	owner := c.get(t, "owner-token", path+"?outline=1", "")
	has(t, owner, ">Publish<")
	lacks(t, owner, "Send for review")
}

func TestBuilderRulesPublishRefusedAfterSave(t *testing.T) {
	t.Parallel()

	c, users := newRolesRig(t)
	own := builderRulesPage(c, users[user.RoleContributor], entity.StatusDraft)
	tok := c.freshToken(users[user.RoleContributor].ID)

	rec := builderRulesSave(c, tok, own, "publish")
	var out saveOut
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if rec.Code != http.StatusForbidden || out.Code != "not_allowed" || !strings.Contains(out.Error, "cannot publish") || out.Version != builderRulesVersion(c, own) {
		t.Errorf("publish: %d %+v", rec.Code, out)
	}
	if st := builderRulesStatus(c, own); st != entity.StatusDraft {
		t.Errorf("status %s, want draft", st)
	}
	if rec := builderRulesSave(c, tok, own, "save"); rec.Code == http.StatusForbidden {
		t.Errorf("a plain save of an own draft was refused: %s", rec.Body)
	}
}

func TestBuilderRulesOutlinePublishRefusedAfterSave(t *testing.T) {
	t.Parallel()

	c, users := newRolesRig(t)
	own := builderRulesPage(c, users[user.RoleContributor], entity.StatusDraft)
	tok := c.freshToken(users[user.RoleContributor].ID)
	path := "/admin/entities/" + own.ID.String() + "/edit"

	f := outlineForm(t, c.do("GET", path+"?outline=1", tok, nil, nil).Body.String())
	f.Set("_op", "publish")
	f.Set("name", "Named page")
	rec := c.do("POST", path, tok, f, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cannot publish") {
		t.Fatalf("outline publish: %d %.400s", rec.Code, rec.Body)
	}
	if st := builderRulesStatus(c, own); st != entity.StatusDraft {
		t.Errorf("status %s, want draft", st)
	}
}

func TestBuilderRulesAuthorCannotSaveOthersPage(t *testing.T) {
	t.Parallel()

	c, users := newRolesRig(t)
	other := builderRulesPage(c, c.owner, entity.StatusDraft)
	tok := c.freshToken(users[user.RoleAuthor].ID)

	rec := builderRulesSave(c, tok, other, "save")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not_allowed") {
		t.Fatalf("author save of another's page: %d %s", rec.Code, rec.Body)
	}
	if version := builderRulesVersion(c, other); version != other.Version {
		t.Errorf("version moved to %d", version)
	}

	own := builderRulesPage(c, users[user.RoleAuthor], entity.StatusDraft)
	if rec := builderRulesSave(c, tok, own, "publish"); rec.Code != http.StatusOK {
		t.Errorf("author publish of own draft: %d %s", rec.Code, rec.Body)
	}
}
