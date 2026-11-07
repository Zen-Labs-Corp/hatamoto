package render

import (
	"strings"
)

// Data widgets show what the page view carries: a form definition, a menu, the
// breadcrumb trail. Without that data they render nothing publicly and a
// placeholder in the canvas, so the node stays selectable in the editor.
func init() {
	registerWidget("form", func(w *widgetCtx) (any, error) {
		w.Class = "hm-formw"
		slug := str(w.P, "slug")
		f, ok := w.View.Forms[slug]
		if slug == "" || !ok || f.Slug == "" {
			if !w.Canvas {
				return nil, errWidgetEmpty
			}
			return struct{ Slug string }{}, nil // the template shows "Form not found"
		}
		type field struct {
			FormField
			ID, InputType, Href string
		}
		x := struct {
			Slug, Path, Success, Submit string
			Submitted, Failed           bool
			Captcha, HasFile            bool
			CaptchaProvider             string
			CaptchaSiteKey              string
			Fields                      []field
		}{
			Slug: f.Slug, Path: w.View.Path, Success: f.SuccessNotice, Submit: f.SubmitLabel,
			Submitted: w.View.Submitted, Failed: w.View.FormError,
			Captcha: f.Captcha, HasFile: f.HasFile(), CaptchaProvider: f.CaptchaProvider, CaptchaSiteKey: f.CaptchaSiteKey,
		}
		if x.Success == "" {
			x.Success = "Thank you, your message has been sent."
		}
		if x.Submit == "" {
			x.Submit = "Send"
		}
		for _, ff := range f.Fields {
			fl := field{FormField: ff, ID: "hm-form-" + f.Slug + "-" + ff.Name, InputType: inputType(ff.Type)}
			if ff.Link != "" {
				fl.Href = safeURL(ff.Link)
			}
			x.Fields = append(x.Fields, fl)
		}
		return x, nil
	})
	registerWidget("menu", func(w *widgetCtx) (any, error) {
		w.Class = "hm-menu"
		type item struct {
			Label, Href    string
			Blank, Current bool
			NoLink         bool
			Class, ID      string
			Title          string
			AriaLabel, Rel string
			Badge          string
			BadgeClass     string
			Icon           string
			Children       []item
		}
		var conv func(ns []NavItem) []item
		conv = func(ns []NavItem) []item {
			var out []item
			for _, n := range ns {
				out = append(out, item{
					Label: n.Label, Href: safeURL(n.Href), Blank: n.Target == "_blank",
					Current: n.Href != "" && n.Href == w.View.Path, NoLink: n.NoLink,
					Class: n.Class, ID: n.ID, Title: n.Title, AriaLabel: n.AriaLabel, Rel: n.Rel,
					Badge: n.Badge, BadgeClass: n.BadgeClass, Icon: n.Icon, Children: conv(n.Children),
				})
			}
			return out
		}
		loc := str(w.P, "location")
		x := struct {
			Location, Label string
			Items           []item
		}{Location: loc, Label: str(w.P, "label")}
		if x.Label == "" {
			x.Label = "Menu"
		}
		if ds := w.E.opts.Data; ds != nil && loc != "" {
			x.Items = conv(ds.Menu(loc))
		}
		if len(x.Items) == 0 && !w.Canvas {
			return nil, errWidgetEmpty
		}
		return x, nil
	})
	registerWidget("breadcrumbs", func(w *widgetCtx) (any, error) {
		w.Class = "hm-crumbs"
		type crumb = VisibleCrumb
		co := w.View.CrumbOpts()
		cs := w.View.VisibleCrumbs()
		if len(cs) == 0 {
			if !w.Canvas {
				return nil, errWidgetEmpty
			}
			cs = []crumb{{Name: "Home"}, {Name: "Section"}, {Name: "This page", Current: true}} // canvas placeholder trail
		}
		return struct {
			Crumbs []crumb
			Sep    string
		}{cs, co.Sep}, nil
	})
}

// inputType maps a form field type to the type of its <input>.
func inputType(t string) string {
	switch strings.ToLower(t) {
	case "email":
		return "email"
	case "tel", "phone":
		return "tel"
	case "date", "number", "url", "file":
		return strings.ToLower(t)
	}
	return "text"
}
