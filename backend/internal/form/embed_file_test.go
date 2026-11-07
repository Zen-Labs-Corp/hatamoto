package form

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// embedTags returns the attributes of every element named tag in markup.
func embedTags(t *testing.T, markup, tag string) []map[string]string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			out = append(out, attrs)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

func TestEmbedHTMLFileField(t *testing.T) {
	t.Parallel()
	f := &Form{Slug: "apply", Fields: []FormField{
		{Name: "name", Label: "Name", Type: FieldTypeText},
		{Name: "cv", Label: "CV", Type: FieldTypeFile, Required: true, Validation: &FieldValidation{Accept: []string{"pdf", "docx"}}},
	}}
	markup := EmbedHTML(f, "/forms/apply/submit")
	forms := embedTags(t, markup, "form")
	if len(forms) != 1 || forms[0]["enctype"] != "multipart/form-data" || forms[0]["method"] != "post" {
		t.Fatalf("form = %v", forms)
	}
	var file map[string]string
	for _, in := range embedTags(t, markup, "input") {
		if in["name"] == "cv" {
			file = in
		}
	}
	if file["type"] != "file" || file["accept"] != ".pdf,.docx" {
		t.Errorf("file input = %v", file)
	}
	if _, ok := file["required"]; !ok {
		t.Error("required missing on the file input")
	}
}

func TestEmbedHTMLWithoutFileFieldIsUnchanged(t *testing.T) {
	t.Parallel()
	f := &Form{Slug: "c", Fields: []FormField{{Name: "name", Label: "Name", Type: FieldTypeText}}}
	markup := EmbedHTML(f, "/forms/c/submit")
	if strings.Contains(markup, "enctype") || strings.Contains(markup, `type="file"`) {
		t.Errorf("a form without a file field changed:\n%s", markup)
	}
}
