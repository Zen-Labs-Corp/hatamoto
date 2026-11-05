package admin

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The versions panel states what the daily prune keeps, with the saved value,
// as plain HTML; owners get the link to change it.
func TestVersionsPanelRule(t *testing.T) {
	t.Parallel()

	for _, keep := range []int{50, 80} {
		svc := &fakeVersionRetention{keep: keep}
		c := newContentRigWith(t, func(d *Deps) { d.VersionRetention = svc })
		eid := fid(11).String()
		path := "/admin/entities/" + eid + "/versions"
		rule := "Keeps the last " + strconv.Itoa(keep) + " versions of each item, and every version that was live in the last 30 days. Older ones are removed daily."

		owner := c.get(t, "owner-token", path, "")
		if !strings.Contains(owner, rule) {
			t.Errorf("keep %d: owner page lacks the rule", keep)
		}
		if !strings.Contains(owner, `href="/admin/settings/versions"`) {
			t.Errorf("keep %d: owner page lacks the settings link", keep)
		}
		editor := c.get(t, "editor-token", path, "")
		if !strings.Contains(editor, rule) {
			t.Errorf("keep %d: editor page lacks the rule", keep)
		}
		if strings.Contains(editor, `/settings/versions`) {
			t.Errorf("keep %d: editor page has the settings link", keep)
		}
	}
}

// Without the service the panel states the default.
func TestVersionsPanelRuleDefault(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String()+"/versions", "")
	if !strings.Contains(body, "Keeps the last 50 versions") || strings.Contains(body, `/settings/versions`) {
		t.Error("the default rule or link is wrong")
	}
}

func TestVersionsPanelLiveBadge(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	for _, s := range []string{"Second summary", "Third summary"} {
		c.post("/admin/entities/"+id, url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {s}, "attr.featured": {"true"}, "_present.attr.featured": {"true"}})
	}
	vs := c.ents.versions[fid(11)]
	if len(vs) < 2 {
		t.Fatalf("versions = %d", len(vs))
	}
	recent, old := time.Now().Add(-24*time.Hour), time.Now().Add(-40*24*time.Hour)
	vs[0].LiveAt, vs[1].LiveAt = &old, &recent
	body := c.get(t, "owner-token", "/admin/entities/"+id+"/versions", "")
	if n := strings.Count(body, ">Live<"); n != 1 {
		t.Errorf("Live badges = %d, want 1 (the version live 40 days ago is not)", n)
	}
}
