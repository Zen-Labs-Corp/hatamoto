package admin

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/changeset"
)

var (
	trackedOwnership = &changeset.Ownership{State: changeset.OwnershipTracked, Plugin: "demo", SourceKey: "old.example.com, post 123", ChangesetID: 42, PublishedAt: ptr(t0)}
	yoursOwnership   = &changeset.Ownership{State: changeset.OwnershipYours, Plugin: "demo", SourceKey: "old.example.com, page 8", ChangesetID: 42, PublishedAt: ptr(t0)}
)

func ownershipPage(t *testing.T, own *changeset.Ownership, golden string) (string, *contentRig) {
	t.Helper()
	c, f := ownershipRig(t)
	f.own = own
	return c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), golden), c
}

func TestEntityEditOwnershipTracked(t *testing.T) {
	t.Parallel()
	body, _ := ownershipPage(t, trackedOwnership, "entity-edit-imported")
	has(t, body,
		`Who owns this page`, `Imported, tracked by demo`, `hm-badge--warning`,
		`<code>old.example.com, post 123</code>`, `Changeset #42, 1 Mar 2026`,
		`/admin/plugins/demo/changesets/42`, `Things a plugin can never own`, `hm-callout--info`)
	// Nothing to hand back while the plugin tracks the page.
	lacks(t, body, `Let demo track this again`, `id="hm-ownform"`)
}

func TestEntityEditOwnershipYours(t *testing.T) {
	t.Parallel()
	body, _ := ownershipPage(t, yoursOwnership, "entity-edit-yours")
	has(t, body,
		`>Yours</span>`, `hm-badge--info`, `imports no longer change it.`, `<code>old.example.com, page 8</code>`,
		`<footer class="hm-card__foot">`, `<span>Let demo track this again</span>`, `form="hm-ownform"`,
		`<form method="post" action="/admin/entities/`+fid(11).String()+`/origin" id="hm-ownform">`, `Things a plugin can never own`)
	// The hand-back form sits after the editor's form: forms never nest.
	i, j := strings.Index(body, `id="hm-entityform"`), strings.Index(body, `id="hm-ownform"`)
	if end := strings.Index(body[i:], "</form>"); i < 0 || j < i+end {
		t.Errorf("hand-back form is inside the editor form (%d, %d)", i, j)
	}
}

func TestEntityEditOwnershipHidden(t *testing.T) {
	t.Parallel()
	for name, own := range map[string]*changeset.Ownership{"no state": nil, "none": {State: changeset.OwnershipNone}} {
		body, _ := ownershipPage(t, own, "")
		lacks(t, body, `Who owns this page`, `Things a plugin can never own`, `hm-ownform`)
		_ = name
	}
	// No reader wired: no card, no form.
	c := newContentRig(t)
	lacks(t, c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), ""), `Who owns this page`, `hm-ownform`)
	// A failed lookup hides the card, not the editor.
	c, f := ownershipRig(t)
	f.getErr = errors.New("db down")
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, `id="hm-entityform"`)
	lacks(t, body, `Who owns this page`)
}

func TestEntityEditOwnershipPluginLabel(t *testing.T) {
	t.Parallel()
	c, f := ownershipRig(t)
	f.own = yoursOwnership
	c.a.deps.Growth.Plugins = &fakePlugins{list: []PluginInfo{{Name: "demo", Runs: []RunDecl{{Name: "sync", Title: "Demo Import"}}}}}
	body := c.get(t, "owner-token", "/admin/entities/"+fid(11).String(), "")
	has(t, body, `Let Demo Import track this again`)
	lacks(t, body, `Let demo track this again`)
}

func TestEntityEditOwnershipYoursDate(t *testing.T) {
	t.Parallel()
	body, c := ownershipPage(t, yoursOwnership, "")
	want := fmtDate(c.ents.ents[fid(11)].UpdatedAt.In(time.UTC))
	has(t, body, "You edited this page on "+want)
}

func TestBlogEditorOwnership(t *testing.T) {
	t.Parallel()
	r := newBlogRig(t)
	f := &fakeOwnership{plugin: "demo", own: yoursOwnership}
	r.a.deps.Content.Ownership = f
	body := r.get(t, "owner-token", r.path(), "blogedit-yours")
	has(t, body, `id="be-own"`, `id="be-own-never"`, `>Yours</span>`, `<span>Let demo track this again</span>`, `form="hm-ownform"`,
		`action="/admin/entities/`+r.ent.ID.String()+`/origin" id="hm-ownform"`)
	// The card is in the post tab, before the SEO tab.
	if p, s := strings.Index(body, `id="be-own"`), strings.Index(body, `id="be-tabpanel-seo"`); p < 0 || p > s {
		t.Errorf("card at %d, SEO tab at %d", p, s)
	}
	f.own = trackedOwnership
	body = r.get(t, "owner-token", r.path(), "")
	has(t, body, `Imported, tracked by demo`)
	lacks(t, body, `hm-ownform`)
	f.own = nil
	lacks(t, r.get(t, "owner-token", r.path(), ""), `be-own`, `hm-ownform`)
}
