package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"hatamoto.dev/core/internal/form"
)

type embedControl struct{ tag, typ, name, value string }

// parseEmbed parses markup from form.EmbedHTML and returns the form action
// and the controls a browser would offer.

func parseEmbed(t *testing.T, markup string) (action string, controls []embedControl) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, k string) string {
		for _, a := range n.Attr {
			if a.Key == k {
				return a.Val
			}
		}
		return ""
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "form":
				action = attr(n, "action")
			case "input":
				controls = append(controls, embedControl{"input", attr(n, "type"), attr(n, "name"), attr(n, "value")})
			case "textarea":
				controls = append(controls, embedControl{tag: "textarea", name: attr(n, "name")})
			case "select":
				c := embedControl{tag: "select", name: attr(n, "name")}
				for o := n.FirstChild; o != nil; o = o.NextSibling {
					if v := attr(o, "value"); v != "" && c.value == "" {
						c.value = v
					}
				}
				controls = append(controls, c)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return action, controls
}

func embedTestFields() []form.FormField {
	return []form.FormField{
		{ID: "h", Type: form.FieldTypeHeading, Text: "About you"},
		{ID: "p", Type: form.FieldTypeParagraph, Text: "Intro"},
		{ID: "d", Type: form.FieldTypeDivider},
		{ID: "1", Type: form.FieldTypeText, Name: "name", Label: "Name", Required: true},
		{ID: "2", Type: form.FieldTypeEmail, Name: "email", Label: "Email"},
		{ID: "3", Type: form.FieldTypePhone, Name: "phone", Label: "Phone"},
		{ID: "4", Type: form.FieldTypeTextarea, Name: "message", Label: "Message"},
		{ID: "5", Type: form.FieldTypeSelect, Name: "topic", Label: "Topic", Options: []form.FieldOption{{Label: "Sales", Value: "sales"}}},
		{ID: "6", Type: form.FieldTypeRadio, Name: "size", Label: "Size", Options: []form.FieldOption{{Label: "Small", Value: "s"}}},
		{ID: "7", Type: form.FieldTypeCheckbox, Name: "extras", Label: "Extras", Options: []form.FieldOption{{Label: "Tea", Value: "tea"}}},
		{ID: "8", Type: form.FieldTypeCheckbox, Name: "newsletter", Label: "Newsletter"},
		{ID: "9", Type: form.FieldTypeDate, Name: "when", Label: "When"},
		{ID: "10", Type: form.FieldTypeNumber, Name: "count", Label: "Count"},
		{ID: "11", Type: form.FieldTypeURL, Name: "site", Label: "Site"},
		{ID: "12", Type: form.FieldTypeHidden, Name: "campaign", Label: "Campaign", Hidden: &form.HiddenSpec{Source: "static", Value: "spring"}},
		{ID: "13", Type: form.FieldTypeConsent, Name: "agree", Label: "Terms"},
	}
}

// fillEmbed builds the urlencoded body a browser would send after the visitor
// ticked, selected and typed into every control.
func fillEmbed(controls []embedControl, answers map[string]string) url.Values {
	v := url.Values{}
	for _, c := range controls {
		switch {
		case c.typ == "hidden":
			v.Add(c.name, c.value)
		case c.typ == "checkbox" || c.typ == "radio":
			v.Add(c.name, c.value)
		case c.tag == "select":
			v.Add(c.name, c.value)
		case c.typ == "text" && c.name == "hm_website":
			v.Add(c.name, "")
		default:
			if a, ok := answers[c.name]; ok {
				v.Add(c.name, a)
			}
		}
	}
	return v
}

func TestFormEmbedHTMLSubmits(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, &form.Form{Fields: embedTestFields()})
	f := repo.forms["t"]

	action, controls := parseEmbed(t, form.EmbedHTML(f, "/forms/t/submit"))
	if action != "/forms/t/submit" {
		t.Fatalf("action = %q", action)
	}
	honeypot := false
	for _, c := range controls {
		honeypot = honeypot || c.name == "hm_website"
	}
	if !honeypot {
		t.Fatal("honeypot input missing")
	}

	values := fillEmbed(controls, map[string]string{
		"name": "Ada", "email": "ada@example.test", "phone": "+15550100", "message": "Hello",
		"when": "2030-01-02", "count": "3", "site": "https://example.test",
	})
	rec := postForm(srv, "t", values)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(repo.subs) != 1 {
		t.Fatalf("submissions = %d", len(repo.subs))
	}
	data := repo.subs[0].Data
	for k, want := range map[string]any{
		"name": "Ada", "email": "ada@example.test", "message": "Hello", "topic": "sales",
		"size": "s", "campaign": "spring", "agree": "Yes", "count": float64(3),
	} {
		if got := data[k]; got != want {
			t.Errorf("data[%s] = %v (%T), want %v", k, got, got, want)
		}
	}
}

func TestFormEmbedHTMLHoneypotOff(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, &form.Form{Fields: embedTestFields()})
	f := repo.forms["t"]
	f.Settings.HoneypotOff = true

	markup := form.EmbedHTML(f, "/forms/t/submit")
	if strings.Contains(markup, "hm_website") {
		t.Fatal("honeypot markup present with HoneypotOff")
	}
	_, controls := parseEmbed(t, markup)
	rec := postForm(srv, "t", fillEmbed(controls, map[string]string{"name": "Ada"}))
	if rec.Code != http.StatusSeeOther || len(repo.subs) != 1 {
		t.Fatalf("status %d, submissions %d", rec.Code, len(repo.subs))
	}
}
