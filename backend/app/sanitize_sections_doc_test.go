package app

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func TestSanitizeStoredAttributesDoc(t *testing.T) {
	const dirty = `<p>hi</p><script>x</script>`
	mk := func(tag string, v1 bool) string {
		p := `{"tag":"` + tag + `"`
		if v1 {
			p += `,"v1":true`
		}
		p += `}`
		return `{"page_doc":{"v":2,"nodes":[{"id":"s","t":"container","p":` + p + `,"c":[{"id":"k","t":"component","p":{"type":"html","content":{"body":"` + dirty + `"}}}]}]}}`
	}

	t.Run("v1-expressible doc is sanitised, no mirror created", func(t *testing.T) {
		out, n, err := entity.SanitizeStoredAttributes(json.RawMessage(mk("section", true)))
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		s := string(out)
		if strings.Contains(s, "script") || strings.Contains(s, `"page_sections"`) {
			t.Errorf("out = %s", s)
		}
	})
	t.Run("builder-only doc is sanitised, no mirror", func(t *testing.T) {
		out, n, err := entity.SanitizeStoredAttributes(json.RawMessage(mk("div", false)))
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if strings.Contains(string(out), "script") || strings.Contains(string(out), "page_sections") {
			t.Errorf("out = %s", out)
		}
	})
	t.Run("clean doc is returned verbatim", func(t *testing.T) {
		in := json.RawMessage(strings.Replace(mk("div", false), dirty, "<p>hi</p>", 1))
		out, n, err := entity.SanitizeStoredAttributes(in)
		if err != nil || n != 0 || string(out) != string(in) {
			t.Fatalf("n=%d err=%v out=%s", n, err, out)
		}
	})
}

func TestSanitizeStoredAttributesKeepsMirrorStructure(t *testing.T) {
	const dirty = `<p>hi</p><script>x</script>`
	in := `{"page_doc":{"v":2,"nodes":[{"id":"s","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"k","t":"component","p":{"type":"html","content":{"body":"` + dirty + `"}}}]}]},"page_sections":[{"id":"k","type":"html","sort_order":0,"content":{"body":"` + dirty + `"}}]}`
	out, _, err := entity.SanitizeStoredAttributes(json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Sections []struct {
			ID      string
			Type    string
			Content map[string]string
		} `json:"page_sections"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Sections) != 1 || m.Sections[0].ID != "k" || m.Sections[0].Type != "html" || strings.Contains(m.Sections[0].Content["body"], "script") {
		t.Errorf("mirror must keep its structure and be cleaned in place: %s", out)
	}
}
