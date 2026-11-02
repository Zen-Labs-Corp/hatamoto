package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const (
	seoVerificationPath = "/settings/seo/verification"
	// seoBlankTagRows are the empty custom tag rows offered below the saved ones.
	seoBlankTagRows = 3
)

// seoVerificationServices are one built-in service: the form key, label and help.
var seoVerificationServices = []struct{ Key, Label, Help string }{
	{"google", "Google", "The content of the google-site-verification tag."},
	{"bing", "Bing", "The content of the msvalidate.01 tag."},
	{"yandex", "Yandex", "The content of the yandex-verification tag."},
	{"baidu", "Baidu", "The content of the baidu-site-verification tag."},
	{"pinterest", "Pinterest", "The content of the p:domain_verify tag."},
	{"facebook", "Facebook", "The content of the facebook-domain-verification tag."},
}

type seoVerificationField struct{ ID, Name, Label, Help, Value string }

type seoCustomRow struct {
	Index                   int
	Name, Content           string
	NameID, ContentID       string
	NameLabel, ContentLabel string
	NameError, ContentError string
}

type seoVerificationData struct {
	Summary    []ui.FieldError
	Fields     []seoVerificationField
	Errs       map[string]string
	Custom     []seoCustomRow
	MaxContent int
}

func verificationValue(v seo.Verification, key string) string {
	switch key {
	case "google":
		return v.Google
	case "bing":
		return v.Bing
	case "yandex":
		return v.Yandex
	case "baidu":
		return v.Baidu
	case "pinterest":
		return v.Pinterest
	case "facebook":
		return v.Facebook
	}
	return ""
}

// seoVerificationFromForm applies the posted codes over stored. A code the post
// does not carry keeps its stored value; the custom rows are replaced when the
// post carries any, blank rows dropped.
func seoVerificationFromForm(form url.Values, stored seo.Verification) seo.Verification {
	v := stored
	v.Custom = append([]seo.MetaTag(nil), stored.Custom...)
	set := map[string]*string{"google": &v.Google, "bing": &v.Bing, "yandex": &v.Yandex, "baidu": &v.Baidu, "pinterest": &v.Pinterest, "facebook": &v.Facebook}
	for key, dst := range set {
		if form.Has(key) {
			*dst = strings.TrimSpace(form.Get(key))
		}
	}
	posted := false
	var custom []seo.MetaTag
	// Look a little past the cap so an over-long post is rejected, not trimmed.
	for i := 0; i < seo.MaxCustomMetaTags+seoBlankTagRows; i++ {
		p := "custom[" + strconv.Itoa(i) + "]"
		if !form.Has(p+"[name]") && !form.Has(p+"[content]") {
			continue
		}
		posted = true
		m := seo.MetaTag{Name: strings.TrimSpace(form.Get(p + "[name]")), Content: strings.TrimSpace(form.Get(p + "[content]"))}
		if m.Name != "" || m.Content != "" {
			custom = append(custom, m)
		}
	}
	if posted {
		v.Custom = custom
	}
	return v
}

func (a *Admin) handleSEOVerification(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOVerification(w, r, http.StatusOK, set.Verification, nil)
}

func (a *Admin) handleSEOVerificationSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := seoVerificationFromForm(r.PostForm, set.Verification)
	probe := set.Clone()
	probe.Verification = next
	var errs []ui.FieldError
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "verification") {
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	if len(errs) > 0 {
		a.renderSEOVerification(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	if !a.seoSave(w, r, func(s *seo.Settings) { s.Verification = next }, map[string]any{"tab": "verification", "custom": len(next.Custom)}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoVerificationPath, http.StatusSeeOther)
}

func (a *Admin) renderSEOVerification(w http.ResponseWriter, r *http.Request, status int, v seo.Verification, errs []ui.FieldError) {
	d := seoVerificationData{Summary: errs, Errs: map[string]string{}, MaxContent: seo.MaxMetaContentLen}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	for _, s := range seoVerificationServices {
		d.Fields = append(d.Fields, seoVerificationField{
			ID: "verification-" + s.Key, Name: s.Key, Label: s.Label, Help: s.Help, Value: verificationValue(v, s.Key),
		})
	}
	rows := append([]seo.MetaTag(nil), v.Custom...)
	for n := 0; n < seoBlankTagRows && len(rows) < seo.MaxCustomMetaTags; n++ {
		rows = append(rows, seo.MetaTag{})
	}
	for i, m := range rows {
		id := "verification-custom-" + strconv.Itoa(i)
		d.Custom = append(d.Custom, seoCustomRow{
			Index: i, Name: m.Name, Content: m.Content, NameID: id + "-name", ContentID: id + "-content",
			NameLabel: "Tag " + strconv.Itoa(i+1) + " name", ContentLabel: "Tag " + strconv.Itoa(i+1) + " content",
			NameError: d.Errs[id+"-name"], ContentError: d.Errs[id+"-content"],
		})
	}
	view := a.NewView(w, r, "SEO")
	view.Section, view.Tab = "seo", "seo-verification"
	view.Width = "narrow"
	view.Data = d
	a.Render(w, r, status, "settings/seo-verification", view)
}
