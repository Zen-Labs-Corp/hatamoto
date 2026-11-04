package admin

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// pluginOwned runs UpdateEntity through the real entity.Service rules, over
// the in-memory fake, for the entities it is told a plugin owns. It records
// the hand-over the way the repository does: once, on the first structural
// edit of a plugin-owned row.
type pluginOwned struct {
	*fakeEntities
	plugin map[uuid.UUID]bool
}

func (p *pluginOwned) UpdateEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	return entity.NewService(nil, &ownershipRepo{p: p}, slog.New(slog.NewTextHandler(io.Discard, nil))).UpdateEntity(ctx, id, by, in)
}

func (p *pluginOwned) owned(id uuid.UUID) bool { return p.plugin[id] }

// ownershipRepo is the slice of entity.EntityRepository UpdateEntity uses;
// the embedded nil interface panics if an update reaches for anything else.
type ownershipRepo struct {
	entity.EntityRepository
	p *pluginOwned
}

func (r *ownershipRepo) GetByID(ctx context.Context, id uuid.UUID) (*entity.Entity, error) {
	return r.p.GetEntity(ctx, id)
}

func (r *ownershipRepo) Update(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	out, err := r.p.fakeEntities.UpdateEntity(ctx, id, by, in)
	if err != nil {
		return nil, err
	}
	out.TookOwnership = in.TakeOwnership && r.p.plugin[id]
	if in.TakeOwnership {
		delete(r.p.plugin, id)
	}
	return out, nil
}

// pluginRig makes the given entities plugin-owned and routes saves through
// the real service rules.
func pluginRig(c *contentRig, ids ...uuid.UUID) *pluginOwned {
	p := &pluginOwned{fakeEntities: c.ents, plugin: map[uuid.UUID]bool{}}
	for _, id := range ids {
		p.plugin[id] = true
	}
	c.a.deps.Content.Entities = p
	return p
}

// setAttrs stores attributes in the shape an importer leaves them: empty
// strings, false flags and empty objects that the editors drop when they save.
func (c *contentRig) setAttrs(id uuid.UUID, attrs string) {
	c.ents.ents[id].Attributes = []byte(attrs)
}

// Saving a plugin's page in the real editors with nothing structural changed
// (here: one ticked term, or nothing at all) leaves it with the plugin.
func TestEditorsKeepPluginOwnershipOnNonStructuralSave(t *testing.T) {
	t.Parallel()

	t.Run("entity form", func(t *testing.T) {
		t.Parallel()
		c := newContentRig(t)
		p := pluginRig(c, fid(12))
		c.setAttrs(fid(12), `{"summary":"The second ","meta_title":"","no_index":false,"seo":{"focus_keyword":""},"featured":false}`)
		path := "/admin/entities/" + fid(12).String()
		f := parseForm(t, c.get(t, "owner-token", path, ""))
		f.Add("term", fid(33).String())
		f.Set("_op", "save")
		if rec := c.post(path, f); rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if !p.owned(fid(12)) {
			t.Error("a save with only a ticked term took the page from its plugin")
		}
	})

	t.Run("blog editor", func(t *testing.T) {
		t.Parallel()
		r := newBlogRig(t)
		p := pluginRig(r.contentRig, r.ent.ID)
		r.setAttrs(r.ent.ID, `{"content":`+jsonString(postBody)+`,"excerpt":"","featured_image":"","meta_description":"","no_index":false,"seo":{},"title":"Old title"}`)
		f := r.form(t)
		f.Add("term", fid(33).String())
		f.Set("_op", "save")
		if rec := r.post(r.path(), f); rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if !p.owned(r.ent.ID) {
			t.Error("a save with only a ticked term took the post from its plugin")
		}
	})
}

// A real SEO or body edit through the same forms takes the page over.
func TestEditorsTakePluginPageOnStructuralSave(t *testing.T) {
	t.Parallel()

	t.Run("entity form SEO", func(t *testing.T) {
		t.Parallel()
		c := newContentRig(t)
		p := pluginRig(c, fid(12))
		path := "/admin/entities/" + fid(12).String()
		f := parseForm(t, c.get(t, "owner-token", path, ""))
		f.Set("seo_title", "A better title")
		f.Set("_op", "save")
		if rec := c.post(path, f); rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if p.owned(fid(12)) {
			t.Error("an SEO edit left the page with its plugin")
		}
	})

	t.Run("entity form body", func(t *testing.T) {
		t.Parallel()
		c := newContentRig(t)
		p := pluginRig(c, fid(14))
		path := "/admin/entities/" + fid(14).String()
		f := parseForm(t, c.get(t, "owner-token", path, ""))
		f.Set("attr.body", "<p>Rewritten</p>")
		f.Set("_op", "save")
		if rec := c.post(path, f); rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if p.owned(fid(14)) {
			t.Error("a body edit left the page with its plugin")
		}
	})

	t.Run("blog body", func(t *testing.T) {
		t.Parallel()
		r := newBlogRig(t)
		p := pluginRig(r.contentRig, r.ent.ID)
		f := r.form(t)
		f.Set("content", "<p>Rewritten</p>")
		f.Set("_op", "save")
		if rec := r.post(r.path(), f); rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if p.owned(r.ent.ID) {
			t.Error("a body edit left the post with its plugin")
		}
	})

	t.Run("blog SEO", func(t *testing.T) {
		t.Parallel()
		r := newBlogRig(t)
		p := pluginRig(r.contentRig, r.ent.ID)
		f := r.form(t)
		f.Set("seo_keyword", "go servers")
		f.Set("_op", "save")
		if rec := r.post(r.path(), f); rec.Code != 303 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		if p.owned(r.ent.ID) {
			t.Error("an SEO edit left the post with its plugin")
		}
	})
}

// Every admin path that can take a plugin's page over leaves a trace in the
// audit log: the explicit saves and the SEO and restore actions carry
// took_ownership in their detail, and an autosave writes entity.take_over.
func TestTakeOverIsAudited(t *testing.T) {
	t.Parallel()

	t.Run("inline SEO", func(t *testing.T) {
		t.Parallel()
		c := newContentRig(t)
		c.ents.tookOwnership = true
		rows := c.record()
		form := url.Values{"title": {"Alpha for search"}, "orig_title": {""}, "description": {""}, "orig_description": {""}, "orig_noindex": {""}}
		if rec := c.seoPost(11, "editor-token", form, true); rec.Code != 200 {
			t.Fatalf("save: %d %s", rec.Code, rec.Body)
		}
		if got := rows(); len(got) != 1 || got[0].Action != "entity.update" || got[0].Detail["took_ownership"] != true {
			t.Errorf("audit = %+v", got)
		}
	})

	t.Run("version restore", func(t *testing.T) {
		t.Parallel()
		c := newContentRig(t)
		c.ents.versions[fid(11)] = []*entity.EntityVersion{{ID: fid(900), EntityID: fid(11), Version: 1, Attributes: []byte(`{"summary":"Old"}`), CreatedAt: t0}}
		c.ents.tookOwnership = true
		rows := c.record()
		if rec := c.post("/admin/entities/"+fid(11).String()+"/versions/1/restore", url.Values{}); rec.Code != 303 {
			t.Fatalf("restore: %d %s", rec.Code, rec.Body)
		}
		wantAudit(t, rows(), "entity.restore_version")
		if rows()[0].Detail["took_ownership"] != true {
			t.Errorf("detail = %v", rows()[0].Detail)
		}
	})

	t.Run("autosave", func(t *testing.T) {
		t.Parallel()
		for _, took := range []bool{true, false} {
			r := newBlogRig(t)
			r.ents.tookOwnership = took
			rows := r.record()
			rec := r.post(r.path()+"/autosave", url.Values{"version": {"1"}, "name": {"Go servers"}, "content": {"<p>Typing</p>"}})
			if rec.Code != 200 {
				t.Fatalf("autosave: %d %s", rec.Code, rec.Body)
			}
			if !took {
				wantAudit(t, rows())
				continue
			}
			wantAudit(t, rows(), "entity.take_over")
			if rows()[0].Detail["autosave"] != true {
				t.Errorf("detail = %v", rows()[0].Detail)
			}
		}
	})
}
