package entity

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeKinds stands in for the render engine's schema lookup.
func fakeKinds(t *testing.T) {
	t.Helper()
	SetFieldKindResolver(func(typ string, path []string) string {
		switch strings.Join(append([]string{typ}, path...), ".") {
		case "split.lede", "split.rows.note":
			return "richtext"
		case "widget.raw":
			return "html"
		}
		return ""
	})
	t.Cleanup(func() { SetFieldKindResolver(nil) })
}

func TestSanitizeAttributes_SchemaMarkedFields(t *testing.T) {
	fakeKinds(t)
	in := json.RawMessage(`{"page_sections":[
	 {"id":"a","type":"split","content":{"lede":"<p>hi</p><script>x()</script>","heading":"<b>keep</b>","rows":[{"note":"<img src=x onerror=y()>ok"}]}},
	 {"id":"b","type":"widget","content":{"raw":"<p>z</p><script>q()</script>"}}]}`)
	out, err := sanitizeAttributes(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"<script", "onerror"} {
		if strings.Contains(s, bad) {
			t.Errorf("%q survived save-time sanitising: %s", bad, s)
		}
	}
	if !strings.Contains(s, "<b>keep</b>") && !strings.Contains(s, `\u003cb\u003ekeep`) {
		t.Errorf("non-richtext field was altered: %s", s)
	}
}

func TestSanitizeAttributesAgainst_SchemaFieldsUnchangedByteIdentical(t *testing.T) {
	fakeKinds(t)
	legacy := json.RawMessage(`{"page_sections":[{"id":"a","type":"split","content":{"lede":"<p>x</p><script>1</script>"}}]}`)
	out, err := sanitizeAttributesAgainst(append(json.RawMessage(nil), legacy...), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(legacy) {
		t.Errorf("unchanged save rewrote stored bytes: %s", out)
	}
}
