package form

import (
	"html"
	"strings"
)

// EmbedHTML returns standalone markup for f that posts to action. It mirrors
// the field ids, names and types of the form widget so a copy pasted into any
// page creates the same submission. The caller adds a _redirect input when it
// wants one. Forms with captcha need the widget: no captcha markup is emitted.
func EmbedHTML(f *Form, action string) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s) }
	e := html.EscapeString

	w(`<form class="hm-form" method="post" action="` + e(action) + `">` + "\n")
	if !f.Settings.HoneypotOff {
		w(`<div aria-hidden="true"><label>Leave this empty<input type="text" name="hm_website" tabindex="-1" autocomplete="off"></label></div>` + "\n")
	}
	for _, fl := range f.Fields {
		embedField(&b, f.Slug, fl)
	}
	submit := f.Settings.SubmitButtonText
	if submit == "" {
		submit = "Send"
	}
	w(`<button type="submit">` + e(submit) + "</button>\n</form>")
	return b.String()
}

func embedField(b *strings.Builder, slug string, f FormField) {
	e := html.EscapeString
	w := func(s string) { b.WriteString(s) }
	id := e("hm-form-" + slug + "-" + f.Name)
	name := e(f.Name)
	req := ""
	star := ""
	if f.Required {
		req = " required"
		star = ` <abbr title="required">*</abbr>`
	}
	switch f.Type {
	case FieldTypeHidden:
		v := ""
		if f.Hidden != nil && f.Hidden.Source == "static" {
			v = f.Hidden.Value
		}
		w(`<input type="hidden" name="` + name + `" value="` + e(v) + `">` + "\n")
		return
	case FieldTypeHeading:
		w(`<h3>` + e(f.Text) + "</h3>\n")
		return
	case FieldTypeParagraph:
		w(`<p>` + e(f.Text) + "</p>\n")
		return
	case FieldTypeDivider:
		w("<hr>\n")
		return
	}

	w("<div>\n")
	switch {
	case f.Type == FieldTypeConsent:
		label := e(f.Label)
		if f.Link != "" {
			label = `<a href="` + e(f.Link) + `" rel="noopener">` + label + `</a>`
		}
		w(`<label><input type="checkbox" id="` + id + `" name="` + name + `" value="Yes"` + req + `> <span>` + label + star + "</span></label>\n")
	case f.Type == FieldTypeCheckbox && len(f.Options) > 0:
		w(`<fieldset><legend>` + e(f.Label) + star + `</legend>`)
		for _, o := range f.Options {
			w(`<label><input type="checkbox" name="` + name + `" value="` + e(o.Value) + `"> <span>` + e(o.Label) + `</span></label>`)
		}
		w("</fieldset>\n")
	case f.Type == FieldTypeCheckbox:
		w(`<label><input type="checkbox" id="` + id + `" name="` + name + `" value="true"` + req + `> <span>` + e(f.Label) + star + "</span></label>\n")
	case f.Type == FieldTypeRadio:
		w(`<fieldset><legend>` + e(f.Label) + star + `</legend>`)
		for _, o := range f.Options {
			w(`<label><input type="radio" name="` + name + `" value="` + e(o.Value) + `"` + req + `> <span>` + e(o.Label) + `</span></label>`)
		}
		w("</fieldset>\n")
	default:
		w(`<label for="` + id + `">` + e(f.Label) + star + "</label>\n")
		ph := ""
		if f.Placeholder != "" {
			ph = ` placeholder="` + e(f.Placeholder) + `"`
		}
		switch f.Type {
		case FieldTypeTextarea:
			w(`<textarea id="` + id + `" name="` + name + `" rows="5"` + ph + req + "></textarea>\n")
		case FieldTypeSelect:
			first := f.Placeholder
			if first == "" {
				first = "Select…"
			}
			w(`<select id="` + id + `" name="` + name + `"` + req + `><option value="">` + e(first) + `</option>`)
			for _, o := range f.Options {
				w(`<option value="` + e(o.Value) + `">` + e(o.Label) + `</option>`)
			}
			w("</select>\n")
		default:
			w(`<input id="` + id + `" name="` + name + `" type="` + embedInputType(f.Type) + `"` + ph + req + ">\n")
		}
	}
	if f.HelpText != "" {
		w(`<small>` + e(f.HelpText) + "</small>\n")
	}
	w("</div>\n")
}

func embedInputType(t string) string {
	switch t {
	case FieldTypeEmail, FieldTypeDate, FieldTypeNumber, FieldTypeURL:
		return t
	case FieldTypePhone:
		return "tel"
	}
	return "text"
}
