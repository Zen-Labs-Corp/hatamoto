package admin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
)

// fakeOwnership is an EntityOwnership with one answer for every entity.
type fakeOwnership struct {
	own    *changeset.Ownership
	getErr error
	plugin string
	err    error
	handed []uuid.UUID
}

func (f *fakeOwnership) Get(context.Context, uuid.UUID) (*changeset.Ownership, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.own == nil {
		return &changeset.Ownership{}, nil
	}
	return f.own, nil
}

func (f *fakeOwnership) HandBack(_ context.Context, id uuid.UUID) (string, error) {
	f.handed = append(f.handed, id)
	return f.plugin, f.err
}

func ownershipRig(t *testing.T) (*contentRig, *fakeOwnership) {
	t.Helper()
	c := newContentRig(t)
	f := &fakeOwnership{plugin: "demo"}
	c.a.deps.Content.Ownership = f
	return c, f
}

func handBackPath(n int) string { return "/admin/entities/" + fid(n).String() + "/origin" }

func TestEntityHandBack(t *testing.T) {
	t.Parallel()
	c, f := ownershipRig(t)
	rows := c.record()

	rec := c.post(handBackPath(11), url.Values{})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities/"+fid(11).String() {
		t.Fatalf("hand-back: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(f.handed) != 1 || f.handed[0] != fid(11) {
		t.Errorf("handed back %v", f.handed)
	}
	wantAudit(t, rows(), "entity.hand_back")
	if got := rows()[0]; got.Target != fid(11).String() || got.Detail["plugin"] != "demo" || got.Detail["slug"] != "alpha" {
		t.Errorf("audit = %+v", got)
	}
	if body := c.next(rec, "/admin/entities/"+fid(11).String()); !strings.Contains(body, "demo will track this page again.") {
		t.Error("success flash is missing")
	}
}

func TestEntityHandBackRefusals(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err   error
		flash string
	}{
		"already tracked": {changeset.ErrNotHandedOver, "A plugin already tracks this page."},
		"no mapping":      {changeset.ErrNoMapping, "No plugin imported this page"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, f := ownershipRig(t)
			f.err = tc.err
			rows := c.record()
			rec := c.post(handBackPath(11), url.Values{})
			if rec.Code != 303 {
				t.Fatalf("%d", rec.Code)
			}
			if got := rows(); len(got) != 0 {
				t.Errorf("a refused hand-back was audited: %v", got)
			}
			if body := c.next(rec, "/admin/entities/"+fid(11).String()); !strings.Contains(body, tc.flash) {
				t.Errorf("flash %q is missing", tc.flash)
			}
		})
	}

	t.Run("service failure", func(t *testing.T) {
		t.Parallel()
		c, f := ownershipRig(t)
		f.err = errors.New("db down")
		if rec := c.post(handBackPath(11), url.Values{}); rec.Code != 500 {
			t.Errorf("%d", rec.Code)
		}
	})

	t.Run("no ownership dep", func(t *testing.T) {
		t.Parallel()
		c := newContentRig(t)
		if rec := c.post(handBackPath(11), url.Values{}); rec.Code != 404 {
			t.Errorf("%d", rec.Code)
		}
	})

	t.Run("unknown entity", func(t *testing.T) {
		t.Parallel()
		c, f := ownershipRig(t)
		if rec := c.post(handBackPath(999), url.Values{}); rec.Code != 404 || len(f.handed) != 0 {
			t.Errorf("%d %v", rec.Code, f.handed)
		}
	})

	t.Run("csrf", func(t *testing.T) {
		t.Parallel()
		c, f := ownershipRig(t)
		rec := c.do("POST", handBackPath(11), "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""})
		if rec.Code != 403 || len(f.handed) != 0 {
			t.Errorf("%d %v", rec.Code, f.handed)
		}
	})

	t.Run("viewer and anonymous", func(t *testing.T) {
		t.Parallel()
		c, f := ownershipRig(t)
		c.editor.Role = "viewer"
		if rec := c.do("POST", handBackPath(11), "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("viewer: %d", rec.Code)
		}
		if rec := c.do("POST", handBackPath(11), "", url.Values{}, nil); rec.Code == 303 && strings.HasSuffix(rec.Header().Get("Location"), fid(11).String()) {
			t.Errorf("anonymous reached the handler")
		}
		if len(f.handed) != 0 {
			t.Errorf("handed back %v", f.handed)
		}
	})
}

// A save that took a plugin's page over says so in its audit row.
func TestEntitySaveAuditsTakeOver(t *testing.T) {
	t.Parallel()
	for _, took := range []bool{true, false} {
		c := newContentRig(t)
		c.ents.tookOwnership = took
		rows := c.record()
		form := url.Values{"name": {"Beta renamed"}, "slug": {"beta"}, "status": {"draft"}, "attr.summary": {"New"}}
		if rec := c.post("/admin/entities/"+fid(12).String(), form); rec.Code != 303 {
			t.Fatalf("save: %d", rec.Code)
		}
		wantAudit(t, rows(), "entity.update")
		if got := rows()[0].Detail["took_ownership"] == true; got != took {
			t.Errorf("took_ownership = %v, want %v", got, took)
		}
	}
}

// A save that took a plugin's page over tells the person in the flash.
func TestSaveFlashSaysWhenPageIsYoursNow(t *testing.T) {
	t.Parallel()
	const note = "Saved. This page is yours now, so Demo Import no longer updates it."
	plugins := &fakePlugins{list: []PluginInfo{{Name: "demo", Runs: []RunDecl{{Name: "sync", Title: "Demo Import"}}}}}

	t.Run("entity form", func(t *testing.T) {
		t.Parallel()
		c, _ := ownershipRig(t)
		c.a.deps.Growth.Plugins = plugins
		c.ents.tookOwnership = true
		c.a.deps.Content.Ownership.(*fakeOwnership).own = yoursOwnership
		path := "/admin/entities/" + fid(12).String()
		rec := c.post(path, url.Values{"name": {"Beta renamed"}, "slug": {"beta"}, "status": {"draft"}, "attr.summary": {"New"}})
		if rec.Code != 303 {
			t.Fatalf("save: %d", rec.Code)
		}
		has(t, c.next(rec, path), note)
	})

	t.Run("blog editor", func(t *testing.T) {
		t.Parallel()
		r := newBlogRig(t)
		f := &fakeOwnership{own: yoursOwnership}
		r.a.deps.Content.Ownership = f
		r.a.deps.Growth.Plugins = plugins
		r.ents.tookOwnership = true
		form := r.form(t)
		form.Set("_op", "save")
		rec := r.post(r.path(), form)
		if rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		has(t, r.next(rec, r.path()), note)
	})

	t.Run("no take-over, no sentence", func(t *testing.T) {
		t.Parallel()
		c, _ := ownershipRig(t)
		path := "/admin/entities/" + fid(12).String()
		rec := c.post(path, url.Values{"name": {"Beta renamed"}, "slug": {"beta"}, "status": {"draft"}, "attr.summary": {"New"}})
		lacks(t, c.next(rec, path), "yours now")
	})
}
