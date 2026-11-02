package admin

import (
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const seoIndexNowPath = "/settings/seo/indexnow"

type seoIndexNowData struct {
	Summary   []ui.FieldError
	Errs      map[string]string
	State     seo.IndexNow
	Endpoints []ui.Option
	KeyHelp   string
}

// indexNowFromForm applies the posted options over stored. A field the post
// does not carry keeps its stored value (a switch posts "true" or "false").
// The key is never read from the form: it is generated, see applyIndexNowKey.
func indexNowFromForm(form url.Values, stored seo.IndexNow) seo.IndexNow {
	n := stored
	if form.Has("enabled") {
		n.Enabled = form.Get("enabled") == "true"
	}
	if form.Has("endpoint") {
		n.Endpoint = strings.TrimSpace(form.Get("endpoint"))
	}
	return n
}

// applyIndexNowKey generates a key when the pings are on and none is saved
// yet, or when a new one was asked for.
func applyIndexNowKey(n *seo.IndexNow, regenerate bool) {
	if regenerate || (n.Enabled && n.Key == "") {
		n.Key = seo.NewIndexNowKey()
	}
}

func indexNowEndpointOptions(selected string) []ui.Option {
	if selected == "" {
		selected = seo.DefaultIndexNowHost
	}
	opts := make([]ui.Option, 0, len(seo.IndexNowEndpoints))
	for _, h := range seo.IndexNowEndpoints {
		opts = append(opts, ui.Option{Value: h, Label: h, Selected: h == selected})
	}
	return opts
}

func (a *Admin) handleSEOIndexNow(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOIndexNow(w, r, http.StatusOK, set.IndexNow, nil)
}

func (a *Admin) handleSEOIndexNowSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := indexNowFromForm(r.PostForm, set.IndexNow)
	regenerate := r.PostForm.Get("regenerate") == "true"
	probe := set.Clone()
	probe.IndexNow = next
	var errs []ui.FieldError
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "indexnow[") {
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	if len(errs) > 0 {
		a.renderSEOIndexNow(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	// The key stays out of the audit entry and the flash message.
	save := func(s *seo.Settings) {
		key := s.IndexNow.Key
		s.IndexNow = next
		s.IndexNow.Key = key
		applyIndexNowKey(&s.IndexNow, regenerate)
	}
	if !a.seoSave(w, r, save, map[string]any{"tab": "indexnow"}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoIndexNowPath, http.StatusSeeOther)
}

func (a *Admin) renderSEOIndexNow(w http.ResponseWriter, r *http.Request, status int, n seo.IndexNow, errs []ui.FieldError) {
	d := seoIndexNowData{Summary: errs, Errs: map[string]string{}, State: n, Endpoints: indexNowEndpointOptions(n.Endpoint)}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	if n.Key != "" {
		d.KeyHelp = "Served at /" + n.Key + ".txt so search engines can check it is yours."
	}
	view := a.NewView(w, r, "SEO")
	view.Section, view.Tab = "seo", "seo-indexnow"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/seo-indexnow", view)
}
