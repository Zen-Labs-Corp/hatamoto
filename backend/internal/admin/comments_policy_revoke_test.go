package admin

import (
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestCommentPolicyRevoke(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	plugins := cpolPlugins("db", "comments.read", "comments.moderate")
	p.a.deps.Growth.Plugins = plugins
	body := csetGet(t, p, "owner-token")
	if notes := cpolCard(t, body, "notes"); !strings.Contains(notes, ">Revoke<") || !strings.Contains(notes, `data-hm-post="/admin/settings/comments/types/11111111-1111-1111-1111-111111111111/revoke"`) || !strings.Contains(notes, "/plugins/ranker/permissions") || !strings.Contains(notes, "data-hm-confirm") {
		t.Errorf("notes card has no confirmed Revoke button: %s", notes)
	}
	rec := csetPost(p, cpolNotesURL+"/revoke", "owner-token", url.Values{}, nil)
	if rec.Code != 303 {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if got := plugins.list[0].Granted; !slices.Equal(got, []string{"db", "comments.read"}) {
		t.Errorf("granted after revoke = %v", got)
	}
	if got := plugins.list[1].Granted; !slices.Equal(got, []string{"comments.moderate"}) {
		t.Errorf("another plugin's grants changed: %v", got)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.moderation-revoke" || p.events[0].Detail["plugin"] != "ranker" {
		t.Errorf("audit = %+v", p.events)
	}
}

func TestCommentPolicyRevokeRefusals(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	plugins := cpolPlugins("db", "comments.moderate")
	p.a.deps.Growth.Plugins = plugins
	if rec := csetPost(p, cpolNotesURL+"/revoke", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor revoke = %d", rec.Code)
	}
	if !slices.Contains(plugins.list[0].Granted, "comments.moderate") {
		t.Error("editor removed the grant")
	}
	if body := csetGet(t, p, "editor-token"); strings.Contains(body, ">Revoke<") {
		t.Error("editor sees Revoke")
	}
	// No plugin holds the grant for this type.
	if rec := csetPost(p, cpolArticlesURL+"/revoke", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("no holder = %d", rec.Code)
	}
	if rec := csetPost(p, "/types/99999999-9999-9999-9999-999999999999/revoke", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("unknown type = %d", rec.Code)
	}
	if len(p.events) != 0 {
		t.Errorf("audit on a refused revoke: %+v", p.events)
	}
}
