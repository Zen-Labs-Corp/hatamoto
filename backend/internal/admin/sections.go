package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/sectiontemplate"
)

const sectionsPerPage = 25

// Section templates are saved from the page editor ("Save as section template",
// v1-12); these screens list, rename and delete them.
func (a *Admin) mountSections(authed *httpx.Group) {
	authed.HandleFunc("GET /theme/templates", a.handleSections)
	authed.HandleFunc("GET /theme/templates/{id}", a.handleSectionEdit)
	authed.HandleFunc("POST /theme/templates/{id}", a.handleSectionSave)
	authed.HandleFunc("GET /theme/templates/{id}/delete", a.handleSectionDeleteForm)
	authed.HandleFunc("POST /theme/templates/{id}/delete", a.handleSectionDelete)
}

func (a *Admin) sectionStore(w http.ResponseWriter, r *http.Request) (SectionStore, bool) {
	if a.deps.Growth.Sections == nil {
		a.unavailable(w, r, "Section templates")
		return nil, false
	}
	return a.deps.Growth.Sections, true
}

func (a *Admin) handleSections(w http.ResponseWriter, r *http.Request) {
	ss, ok := a.sectionStore(w, r)
	if !ok {
		return
	}
	q, typ := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("type")
	page := pageParam(r)
	res, err := ss.List(r.Context(), &sectiontemplate.ListFilter{Search: q, SectionType: typ}, sectionsPerPage, (page-1)*sectionsPerPage)
	if err != nil {
		a.logger().Error("list section templates failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The section templates could not be loaded.")
		return
	}
	// The type filter offers the types that exist.
	types := map[string]bool{}
	if all, err := ss.List(r.Context(), &sectiontemplate.ListFilter{}, 100, 0); err == nil {
		for _, t := range all.Items {
			types[t.SectionType] = true
		}
	}
	typeNames := make([]string, 0, len(types))
	for t := range types {
		typeNames = append(typeNames, t)
	}
	sort.Strings(typeNames)
	opts := []ui.Option{{Value: "", Label: "All kinds", Selected: typ == ""}}
	for _, t := range typeNames {
		opts = append(opts, ui.Option{Value: t, Label: templateKind(t), Selected: t == typ})
	}
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if typ != "" {
		query.Set("type", typ)
	}
	t := ui.Table{
		ID: "sections", Caption: "Templates", BaseURL: a.adminPath + "/theme/templates", CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "type", Label: "Kind"}, {Key: "tags", Label: "Tags"}, {Key: "used", Label: "Times used", Align: "right"}, {Key: "updated", Label: "Updated"}},
		Query:   q, SearchLabel: "Search templates", Filters: []ui.Filter{{Name: "type", Label: "Kind", Options: opts}},
		Page: ui.Pagination{Page: page, PerPage: sectionsPerPage, Total: res.Total, BaseURL: a.adminPath + "/theme/templates", Query: query},
		Empty: ui.Empty{Icon: "file-text", Title: "No templates", Body: "Save a selection in the builder (\"Save as template…\") to reuse it on other pages.",
			Actions: []ui.Button{{Label: "Open a page to start", Href: a.adminPath + "/entities", Variant: "primary"}}},
	}
	for _, s := range res.Items {
		id := s.ID.String()
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: s.Name, Cells: []ui.Cell{
			{Text: s.Name, Link: a.adminPath + "/theme/templates/" + id}, {Text: templateKind(s.SectionType)}, {Text: strings.Join(s.Tags, ", ")}, {Text: strconv.Itoa(s.UsageCount)}, {Text: fmtDate(s.UpdatedAt)},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: a.adminPath + "/theme/templates/" + id},
			{Label: "Delete", Href: a.adminPath + "/theme/templates/" + id + "/delete", Post: a.adminPath + "/theme/templates/" + id + "/delete", Danger: true, ConfirmLabel: "Delete template",
				Confirm: "The template " + s.Name + " is deleted. Sections already added to pages from it stay as they are."},
		}})
	}
	v := a.NewView(w, r, "Templates")
	v.Data = t
	a.Render(w, r, http.StatusOK, "sections/list", v)
}

// templateKind is what the Templates list shows as a template's kind: "Layout"
// for a builder template, the section type for an older section template.
func templateKind(sectionType string) string {
	if sectionType == treeTemplateType {
		return "Layout"
	}
	return sectionType
}

type sectionForm struct {
	ID, Name, Description, Tags, Type, Content string
	Layout                                     bool
	Used                                       int
	Errors                                     map[string]string
	Summary                                    []ui.FieldError
}

func (a *Admin) loadSection(w http.ResponseWriter, r *http.Request) (*sectiontemplate.SectionTemplate, bool) {
	ss, ok := a.sectionStore(w, r)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		var s *sectiontemplate.SectionTemplate
		if s, err = ss.Get(r.Context(), id); err == nil {
			return s, true
		}
		if !errors.Is(err, sectiontemplate.ErrTemplateNotFound) {
			a.logger().Error("load section template failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The template could not be loaded.")
			return nil, false
		}
	}
	a.ErrorBack(w, r, http.StatusNotFound, "Template not found", "That section template does not exist.", a.adminPath+"/theme/templates", "Back to section templates")
	return nil, false
}

func sectionFormFrom(s *sectiontemplate.SectionTemplate) sectionForm {
	var pretty bytes.Buffer
	if json.Indent(&pretty, s.Content, "", "  ") != nil {
		pretty.Write(s.Content)
	}
	return sectionForm{ID: s.ID.String(), Name: s.Name, Description: s.Description, Tags: strings.Join(s.Tags, ", "), Type: s.SectionType, Layout: s.SectionType == treeTemplateType, Content: pretty.String(), Used: s.UsageCount}
}

func (a *Admin) renderSectionForm(w http.ResponseWriter, r *http.Request, status int, f sectionForm) {
	f.Summary = nil
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"description", "Description"}, {"tags", "Tags"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "Edit section template "+f.Name)
	v.Crumbs = a.areaCrumbs("templates", Crumb{Label: f.Name})
	v.Data = f
	v.Width = "narrow"
	a.Render(w, r, status, "sections/edit", v)
}

func (a *Admin) handleSectionEdit(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSection(w, r)
	if !ok {
		return
	}
	a.renderSectionForm(w, r, http.StatusOK, sectionFormFrom(s))
}

func parseTags(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	return out
}

func (a *Admin) handleSectionSave(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSection(w, r)
	if !ok {
		return
	}
	f := sectionFormFrom(s)
	f.Name, f.Description, f.Tags = strings.TrimSpace(r.PostFormValue("name")), strings.TrimSpace(r.PostFormValue("description")), strings.TrimSpace(r.PostFormValue("tags"))
	f.Errors = map[string]string{}
	tags := parseTags(f.Tags)
	switch {
	case f.Name == "":
		f.Errors["name"] = "Enter a name"
	case utf8.RuneCountInString(f.Name) > 255:
		f.Errors["name"] = "Use at most 255 characters"
	}
	if utf8.RuneCountInString(f.Description) > 1000 {
		f.Errors["description"] = "Use at most 1000 characters"
	}
	if len(tags) > 20 {
		f.Errors["tags"] = "Use at most 20 tags"
	}
	for _, t := range tags {
		if utf8.RuneCountInString(t) > 40 {
			f.Errors["tags"] = "Each tag is at most 40 characters"
		}
	}
	if len(f.Errors) > 0 {
		a.renderSectionForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	if _, err := a.deps.Growth.Sections.Update(r.Context(), s.ID, &sectiontemplate.UpdateInput{Name: f.Name, Description: f.Description, Tags: tags}); err != nil {
		var ve *sectiontemplate.ValidationError
		if errors.As(err, &ve) {
			f.Errors["name"] = ve.Message
			a.renderSectionForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		a.logger().Error("save section template failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The template could not be saved.")
		return
	}
	a.SetFlash(w, r, "success", "Section template saved.")
	http.Redirect(w, r, a.adminPath+"/theme/templates", http.StatusSeeOther)
}

func (a *Admin) handleSectionDeleteForm(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSection(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete the section template " + s.Name + "?", Message: "Sections already added to pages from it stay as they are.",
		Post: a.adminPath + "/theme/templates/" + s.ID.String() + "/delete", Label: "Delete template", Back: a.adminPath + "/theme/templates", Danger: true})
}

func (a *Admin) handleSectionDelete(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSection(w, r)
	if !ok {
		return
	}
	if err := a.deps.Growth.Sections.Delete(r.Context(), s.ID); err != nil && !errors.Is(err, sectiontemplate.ErrTemplateNotFound) {
		a.logger().Error("delete section template failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The template could not be deleted.")
		return
	}
	a.SetFlash(w, r, "success", "Section template deleted.")
	Redirect(w, r, a.adminPath+"/theme/templates")
}
