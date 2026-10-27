package entity

import (
	"encoding/json"
	"testing"
)

func TestSanitizeHTMLSections_PreservesOrderAndUntouched(t *testing.T) {
	in := json.RawMessage(`{"z":1.50,"page_sections":[` +
		`{"type":"content","content":{"body":"<script>x</script>"}},` +
		`{"id":"b","type":"html","content":{"note":"<b onclick=1>n</b>","body":"<p onclick=\"y()\">hi</p><script>x</script>","a":[1, 2]}}],"a":{"k":true}}`)
	out, n, err := SanitizeHTMLSections(in)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := `{"z":1.50,"page_sections":[` +
		`{"type":"content","content":{"body":"<script>x</script>"}},` +
		`{"id":"b","type":"html","content":{"note":"<b onclick=1>n</b>","body":"<p>hi</p>","a":[1, 2]}}],"a":{"k":true}}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	out2, n2, _ := SanitizeHTMLSections(out)
	if n2 != 0 || string(out2) != string(out) {
		t.Errorf("second pass changed output: n=%d %s", n2, out2)
	}
}

func TestSanitizeHTMLSections_CleanAndMalformedUnchanged(t *testing.T) {
	for _, s := range []string{`{"page_sections":[{"type":"html","content":{"body":"<p>ok</p>"}}]}`, `{"x":1}`, `not json`, `{"page_sections":"x"}`, `{}`} {
		out, n, err := SanitizeHTMLSections(json.RawMessage(s))
		if err != nil || n != 0 || string(out) != s {
			t.Errorf("%s -> %s n=%d err=%v", s, out, n, err)
		}
	}
}
