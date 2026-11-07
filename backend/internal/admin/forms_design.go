package admin

import (
	"html/template"
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
)

// formDesignTypeGroup is the palette group of a field type, in the designer's order.
func formDesignTypeGroup(t string) string {
	switch t {
	case form.FieldTypeSelect, form.FieldTypeRadio, form.FieldTypeCheckbox:
		return "choices"
	case form.FieldTypeHeading, form.FieldTypeParagraph, form.FieldTypeDivider:
		return "layout"
	case form.FieldTypeHidden, form.FieldTypeConsent:
		return "advanced"
	}
	return "fields"
}

// formDesignUIIcons are the icons the designer script draws besides the type icons.
var formDesignUIIcons = []string{"arrow-up", "arrow-down", "chevron-down", "chevron-right", "copy", "eye", "eye-off", "grip", "more", "plus", "search", "trash", "x"}

type formDesignType struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	Group string `json:"group"`
	Icon  string `json:"icon"`
}

// formDesignData is the Build tab of the form designer, and the shell the
// Settings and Share tabs reuse.
type formDesignData struct {
	ID, Name, Slug, Status string
	Tabs                   []ui.Tab
	Statuses               []ui.Option
	Icons                  []string
	Boot                   template.HTML
}

// formDesignTabs is the tab strip across the designer. active is "build",
// "settings" or "share".
func (a *Admin) formDesignTabs(id, active string) []ui.Tab {
	base := a.adminPath + "/forms/design/" + id
	return []ui.Tab{
		{Label: "Build", Href: base, Active: active == "build"},
		{Label: "Settings", Href: base + "/settings", Active: active == "settings"},
		{Label: "Submissions", Href: a.adminPath + "/forms/submissions?form=" + id},
		{Label: "Share and embed", Href: base + "/share", Active: active == "share"},
	}
}

// handleFormEditRedirect sends the old edit URL to the designer.
func (a *Admin) handleFormEditRedirect(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, a.adminPath+"/forms/design/"+f.ID.String(), http.StatusSeeOther)
}

func (a *Admin) handleFormDesign(w http.ResponseWriter, r *http.Request) {
	f, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	id := f.ID.String()
	types := make([]formDesignType, 0, len(form.GetFieldTypes()))
	icons := append([]string(nil), formDesignUIIcons...)
	for _, t := range form.GetFieldTypes() {
		types = append(types, formDesignType{Type: t, Label: fieldTypeLabels[t], Group: formDesignTypeGroup(t), Icon: fieldTypeIcon(t)})
		icons = append(icons, fieldTypeIcon(t))
	}
	fields := f.Fields
	if fields == nil {
		fields = []form.FormField{}
	}
	boot, err := jsonx.Marshal(map[string]any{
		"id": id, "version": f.Version(), "name": f.Name, "description": f.Description, "status": f.Status,
		"fields": fields, "settings": f.Settings, "freeEmail": form.FreeEmailDomains(),
		"saveUrl": a.adminPath + "/forms/" + id + "/definition", "endpoint": a.formEndpoint(f.Slug),
		"captchaConfigured": !a.captchaUnconfigured(r.Context()), "types": types,
	}, jsonx.HTMLSafe)
	if err != nil {
		a.logger().Error("form designer boot failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be loaded.")
		return
	}
	v := a.NewView(w, r, "Design form: "+f.Name)
	v.Data = formDesignData{
		ID: id, Name: f.Name, Slug: f.Slug, Status: f.Status,
		Tabs: a.formDesignTabs(id, "build"), Statuses: statusOptions(f.Status), Icons: dedupe(icons),
		Boot: template.HTML(`<script type="application/json" data-hm-fd-boot>` + string(boot) + `</script>`), //nolint:gosec // jsonx.HTMLSafe output: <, >, & and U+2028/9 are escaped
	}
	a.Render(w, r, http.StatusOK, "forms/design", v)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
