package admin

import (
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const seoAdsTxtPath = "/settings/seo/ads-txt"

// adsTxtState is the text of the two files, as stored or as submitted.
type adsTxtState struct{ Ads, App string }

type seoAdsTxtData struct {
	Summary  []ui.FieldError
	Errs     map[string]string
	State    adsTxtState
	MaxLines int
	// Note says that a line was added to the box and is not saved yet.
	Note string
}

// adsTxtFields names the two files: validation field, input id and label.
var adsTxtFields = [2]struct{ field, id, label string }{
	{"ads_txt", "ads-txt", "ads.txt"},
	{"app_ads_txt", "app-ads-txt", "app-ads.txt"},
}

func (a *Admin) handleSEOAdsTxt(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	st := adsTxtState{Ads: set.AdsTxt, App: set.AppAdsTxt}
	note := ""
	if add := r.URL.Query().Get("add"); add != "" {
		if line := seo.AdSenseLine(add); line != "" {
			note = "The AdSense line is already in your ads.txt."
			if !strings.Contains("\n"+st.Ads, "\n"+line+"\n") {
				st.Ads += line + "\n" // stored text always ends with a newline
				note = "The AdSense line was added below. It is not saved until you press Save."
			}
		}
	}
	a.renderSEOAdsTxt(w, r, http.StatusOK, st, nil, note)
}

func (a *Admin) handleSEOAdsTxtSave(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.seoLoad(w, r); !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves both files empty, which validates
	typed := adsTxtState{Ads: r.PostForm.Get("ads_txt"), App: r.PostForm.Get("app_ads_txt")}
	next := adsTxtState{Ads: seo.NormalizeAdsTxt(typed.Ads), App: seo.NormalizeAdsTxt(typed.App)}
	var errs []ui.FieldError
	for i, text := range [2]string{next.Ads, next.App} {
		f := adsTxtFields[i]
		for _, fe := range seo.ValidateAdsTxt(f.field, text) {
			errs = append(errs, ui.FieldError{ID: f.id, Label: f.label, Message: fe.Message})
		}
	}
	if len(errs) > 0 {
		a.renderSEOAdsTxt(w, r, http.StatusUnprocessableEntity, typed, errs, "")
		return
	}
	audit := map[string]any{"tab": "ads-txt", "ads_lines": adsTxtLines(next.Ads), "app_ads_lines": adsTxtLines(next.App)}
	if !a.seoSave(w, r, func(s *seo.Settings) { s.AdsTxt, s.AppAdsTxt = next.Ads, next.App }, audit) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoAdsTxtPath, http.StatusSeeOther)
}

// adsTxtLines counts the non-empty lines of a normalised body.
func adsTxtLines(text string) int {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if l != "" {
			n++
		}
	}
	return n
}

func (a *Admin) renderSEOAdsTxt(w http.ResponseWriter, r *http.Request, status int, st adsTxtState, errs []ui.FieldError, note string) {
	d := seoAdsTxtData{Summary: errs, Errs: map[string]string{}, State: st, MaxLines: seo.MaxAdsTxtLines, Note: note}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-ads-txt"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-ads-txt", v)
}
