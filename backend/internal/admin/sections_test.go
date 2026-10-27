package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/sectiontemplate"
)

type fakeSections struct {
	m map[uuid.UUID]*sectiontemplate.SectionTemplate
}

func (f *fakeSections) List(_ context.Context, fl *sectiontemplate.ListFilter, limit, offset int) (*sectiontemplate.ListResult, error) {
	var all []sectiontemplate.SectionTemplate
	for _, s := range f.m {
		if fl != nil && fl.SectionType != "" && s.SectionType != fl.SectionType {
			continue
		}
		if fl != nil && fl.Search != "" && !strings.Contains(strings.ToLower(s.Name), strings.ToLower(fl.Search)) {
			continue
		}
		all = append(all, *s)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	total := len(all)
	off := min(offset, total)
	return &sectiontemplate.ListResult{Items: all[off:min(off+limit, total)], Total: total}, nil
}
func (f *fakeSections) Get(_ context.Context, id uuid.UUID) (*sectiontemplate.SectionTemplate, error) {
	if s, ok := f.m[id]; ok {
		c := *s
		return &c, nil
	}
	return nil, sectiontemplate.ErrTemplateNotFound
}
func (f *fakeSections) Update(_ context.Context, id uuid.UUID, in *sectiontemplate.UpdateInput) (*sectiontemplate.SectionTemplate, error) {
	s, ok := f.m[id]
	if !ok {
		return nil, sectiontemplate.ErrTemplateNotFound
	}
	s.Name, s.Description, s.Tags = in.Name, in.Description, in.Tags
	return s, nil
}
func (f *fakeSections) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.m[id]; !ok {
		return sectiontemplate.ErrTemplateNotFound
	}
	delete(f.m, id)
	return nil
}

func TestSectionTemplates(t *testing.T) {
	t.Parallel()

	fs := &fakeSections{m: map[uuid.UUID]*sectiontemplate.SectionTemplate{}}
	tmpl := &sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Hero with CTA", SectionType: "hero", Content: json.RawMessage(`{"heading":"<b>Hi</b>"}`), Tags: []string{"landing"}, UsageCount: 3, UpdatedAt: time.Now()}
	fs.m[tmpl.ID] = tmpl
	g := newRig(t, func(d *Deps) { d.Growth.Sections = fs })
	list := g.do("GET", "/admin/theme/templates", "owner-token", nil, nil).Body.String()
	if !strings.Contains(list, "Hero with CTA") || !strings.Contains(list, "landing") {
		t.Fatalf("list: %s", list)
	}
	if issues := a11y.Check(list, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("list a11y: %v", issues)
	}
	if body := g.do("GET", "/admin/theme/templates?type=faq", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "No templates") {
		t.Error("type filter")
	}
	// A builder template is listed as a Layout and says so on its page.
	tree := &sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Pricing row", SectionType: treeTemplateType, Content: json.RawMessage(`{"v":1,"nodes":[]}`), UpdatedAt: time.Now()}
	fs.m[tree.ID] = tree
	if body := g.do("GET", "/admin/theme/templates", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Pricing row") || !strings.Contains(body, "Layout") {
		t.Errorf("tree template row: %s", body)
	}
	if body := g.do("GET", "/admin/theme/templates/"+tree.ID.String(), "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Layout template from the builder") {
		t.Error("tree template page")
	}
	delete(fs.m, tree.ID)
	u := "/admin/theme/templates/" + tmpl.ID.String()
	edit := g.do("GET", u, "owner-token", nil, nil).Body.String()
	if !strings.Contains(edit, "&lt;b&gt;Hi") || strings.Contains(edit, "<b>Hi</b>") {
		t.Error("saved content must be shown escaped")
	}
	if issues := a11y.Check(edit, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("edit a11y: %v", issues)
	}
	if rec := g.do("POST", u, "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a name") {
		t.Errorf("blank name: %d", rec.Code)
	}
	if rec := g.do("POST", u, "owner-token", url.Values{"name": {"Renamed"}, "description": {"d"}, "tags": {"a, b, A,"}}, nil); rec.Code != 303 || fs.m[tmpl.ID].Name != "Renamed" || len(fs.m[tmpl.ID].Tags) != 2 {
		t.Fatalf("save: %d %+v", rec.Code, fs.m[tmpl.ID])
	}
	if g.do("GET", u+"/delete", "owner-token", nil, nil).Code != 200 || len(fs.m) != 1 {
		t.Fatal("confirmation page must not delete")
	}
	if rec := g.do("POST", u+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || len(fs.m) != 0 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := g.do("GET", u, "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("gone: %d", rec.Code)
	}
}
