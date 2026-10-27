package v2

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func docAttrs(target uuid.UUID, withMirror bool) string {
	link := `<a data-entity-id=\"` + target.String() + `\" href=\"/old\">go</a>`
	comp := `{"id":"k","t":"component","p":{"type":"content","content":{"body":"` + link + `"}}}`
	doc := `{"v":2,"nodes":[{"id":"s","t":"container","p":{"tag":"section","v1":true},"c":[` + comp + `]}]}`
	if !withMirror {
		doc = `{"v":2,"nodes":[{"id":"row","t":"container","p":{"tag":"div"},"c":[` + comp + `]}]}`
		return `{"page_doc":` + doc + `}`
	}
	return `{"page_doc":` + doc + `,"page_sections":[{"id":"k","type":"content","sort_order":0,"content":{"body":"` + link + `"}}]}`
}

// assertDocRewrittenMirrorFrozen: the doc carries /new, the frozen page_sections
// mirror is left exactly as stored (never re-mirrored).
func assertDocRewrittenMirrorFrozen(t *testing.T, out []byte) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m["page_doc"]), `href=\"/new\"`) || strings.Contains(string(m["page_doc"]), "/old") {
		t.Errorf("doc must carry /new: %s", m["page_doc"])
	}
	if !strings.Contains(string(m["page_sections"]), `href=\"/old\"`) || strings.Contains(string(m["page_sections"]), "/new") {
		t.Errorf("mirror must stay frozen: %s", m["page_sections"])
	}
}

func TestRewriteHrefsUpdatesDocMirrorFrozen(t *testing.T) {
	target := uuid.New()
	out, recs, err := rewriteHrefsInAttributes([]byte(docAttrs(target, true)), target, "/new")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1 (doc is the single source of truth)", len(recs))
	}
	assertDocRewrittenMirrorFrozen(t, out)
}

func TestRewriteHrefsBuilderOnlyDoc(t *testing.T) {
	target := uuid.New()
	out, recs, err := rewriteHrefsInAttributes([]byte(docAttrs(target, false)), target, "/new")
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs=%d err=%v", len(recs), err)
	}
	if strings.Contains(string(out), "page_sections") || !strings.Contains(string(out), "/new") {
		t.Errorf("builder-only doc: %s", out)
	}
}

func TestRewriteSingleHrefUpdatesDocMirrorFrozen(t *testing.T) {
	target := uuid.New()
	out, rec, err := rewriteSingleHrefInAttributes([]byte(docAttrs(target, true)), target, 0, "/new")
	if err != nil || rec == nil {
		t.Fatalf("rec=%v err=%v", rec, err)
	}
	assertDocRewrittenMirrorFrozen(t, out)
}

func TestParseLinksDocNoDoubleCount(t *testing.T) {
	target := uuid.New()
	for name, withMirror := range map[string]bool{"mirrored": true, "builder-only": false} {
		links, err := ParseLinksFromAttributes([]byte(docAttrs(target, withMirror)))
		if err != nil || len(links) != 1 || links[0].TargetEntityID != target || links[0].OccurrenceIndex != 0 {
			t.Errorf("%s: links=%+v err=%v", name, links, err)
		}
	}
}
