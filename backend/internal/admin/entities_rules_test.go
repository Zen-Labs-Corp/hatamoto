package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// entitiesRulesRow returns the list row markup of the entity named name, or fails.
func entitiesRulesRow(t *testing.T, body, name string) string {
	t.Helper()
	i := strings.Index(body, name)
	if i < 0 {
		t.Fatalf("entity %q not listed", name)
	}
	return body[i:min(len(body), i+3000)]
}

func TestEntitiesRulesAuthorRowActions(t *testing.T) {
	c, us := newRolesRig(t)
	mine := c.entityBy(us[user.RoleAuthor], entity.StatusDraft)
	theirs := c.entityBy(us[user.RoleReviewer], entity.StatusDraft)
	tok := c.freshToken(us[user.RoleAuthor].ID)
	body := c.do("GET", "/admin/entities?type=service", tok, nil, nil).Body.String()
	if !strings.Contains(entitiesRulesRow(t, body, mine.Name), "/status") {
		t.Error("author lost Publish on own draft")
	}
	other := entitiesRulesRow(t, body, theirs.Name)
	if end := strings.Index(other[len(theirs.Name):], "</tr>"); end > 0 {
		other = other[:end]
	}
	if strings.Contains(other, "/status") {
		t.Errorf("author sees Publish on another user's entity:\n%s", other)
	}
}

func TestEntitiesRulesContributorAndReviewerList(t *testing.T) {
	c, us := newRolesRig(t)
	own := c.entityBy(us[user.RoleContributor], entity.StatusDraft)
	tok := c.freshToken(us[user.RoleContributor].ID)
	body := c.do("GET", "/admin/entities?type=service", tok, nil, nil).Body.String()
	if strings.Contains(body, `value="publish"`) {
		t.Error("contributor sees bulk Publish")
	}
	row := entitiesRulesRow(t, body, own.Name)
	if end := strings.Index(row[len(own.Name):], "</tr>"); end > 0 {
		row = row[:end]
	}
	if strings.Contains(row, `"status"`) && strings.Contains(row, "published") {
		t.Errorf("contributor sees row Publish:\n%s", row)
	}

	rv := c.freshToken(us[user.RoleReviewer].ID)
	all := c.do("GET", "/admin/entities", rv, nil, nil).Body.String()
	if strings.Contains(all, "create-menu") {
		t.Error("reviewer sees the Create menu")
	}
	if !strings.Contains(c.do("GET", "/admin/entities", c.freshToken(us[user.RoleAuthor].ID), nil, nil).Body.String(), "create-menu") {
		t.Error("author lost the Create menu")
	}
}

func TestEntitiesRulesStatusAndDeleteRefused(t *testing.T) {
	c, us := newRolesRig(t)
	theirs := c.entityBy(us[user.RoleReviewer], entity.StatusDraft)
	mine := c.entityBy(us[user.RoleContributor], entity.StatusDraft)
	authorTok := c.freshToken(us[user.RoleAuthor].ID)
	contribTok := c.freshToken(us[user.RoleContributor].ID)

	pub := url.Values{"status": {"published"}}
	if code := c.do("POST", "/admin/entities/"+theirs.ID.String()+"/status", authorTok, pub, nil).Code; code != 403 {
		t.Errorf("author status on another's entity = %d, want 403", code)
	}
	if code := c.do("POST", "/admin/entities/"+mine.ID.String()+"/status", contribTok, pub, nil).Code; code != 403 {
		t.Errorf("contributor publish on own = %d, want 403", code)
	}
	if code := c.do("POST", "/admin/entities/"+theirs.ID.String()+"/delete", authorTok, url.Values{}, nil).Code; code != 403 {
		t.Errorf("author delete of another's entity = %d, want 403", code)
	}
}

func TestEntitiesRulesBulkSkipsOthers(t *testing.T) {
	c, us := newRolesRig(t)
	own := c.entityBy(us[user.RoleAuthor], entity.StatusDraft)
	other := c.entityBy(us[user.RoleReviewer], entity.StatusDraft)
	tok := c.freshToken(us[user.RoleAuthor].ID)

	rec := c.do("POST", "/admin/entities/bulk", tok, url.Values{"action": {"publish"}, "id": {own.ID.String(), other.ID.String()}}, nil)
	if rec.Code >= 500 || rec.Code == 403 {
		t.Fatalf("bulk = %d, want a redirect", rec.Code)
	}
	if got := c.ents.ents[own.ID].Status; got != entity.StatusPublished {
		t.Errorf("own entity status = %s, want published", got)
	}
	if got := c.ents.ents[other.ID].Status; got != entity.StatusDraft {
		t.Errorf("other's entity status = %s, want draft", got)
	}
	flash := c.next(rec, "/admin/entities")
	if !strings.Contains(flash, "1 entry was skipped: you cannot change them.") {
		t.Errorf("flash lacks the skipped message")
	}
}
