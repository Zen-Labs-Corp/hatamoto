package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

type pasteOut struct {
	Nodes         []*builder.Node `json:"nodes"`
	MissingBlocks []string        `json:"missingBlocks"`
	Error         string          `json:"error"`
	Code          string          `json:"code"`
}

func (r *reuseRig) paste(t *testing.T, token, body string) (*httptest.ResponseRecorder, pasteOut) {
	t.Helper()
	rec := r.postJSON(r.url(r.page, "/builder/paste"), token, body, nil)
	var out pasteOut
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func envelope(nodes string) string {
	return `{"hm":"nodes","v":2,"site":"https://other.example","nodes":` + nodes + `}`
}

const pasteNodes = `[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2,"navlabel":"Title"}}]},{"id":"t1","t":"text","p":{"text":"Body"}}]`

func TestPasteReturnsFreshIDs(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	rec, out := r.paste(t, "owner-token", envelope(pasteNodes))
	if rec.Code != 200 || len(out.Nodes) != 2 || len(out.Nodes[0].C) != 1 || out.MissingBlocks == nil || len(out.MissingBlocks) != 0 {
		t.Fatalf("paste: %d %s", rec.Code, rec.Body)
	}
	seen := map[string]int{}
	collectIDs(out.Nodes, seen)
	if len(seen) != 3 {
		t.Errorf("ids must be unique: %v", seen)
	}
	for id := range seen {
		if id == "c1" || id == "h1" || id == "t1" || !builder.ValidID(id) {
			t.Errorf("id %q was not renewed", id)
		}
	}
	if out.Nodes[0].C[0].P["navlabel"] != "Title" {
		t.Errorf("props lost: %v", out.Nodes[0].C[0].P)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Error("paste reply must not be cached")
	}
	if len(r.rec.inputs) != 0 {
		t.Error("paste wrote")
	}
}

func TestPasteRefusals(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	for name, body := range map[string]string{
		"wrong hm":      `{"hm":"doc","v":2,"nodes":` + pasteNodes + `}`,
		"wrong version": `{"hm":"nodes","v":1,"nodes":` + pasteNodes + `}`,
		"no version":    `{"hm":"nodes","nodes":` + pasteNodes + `}`,
		"empty":         envelope(`[]`),
		"unknown prop":  envelope(`[{"id":"a","t":"text","p":{"text":"x","nope":1}}]`),
		"unknown kind":  envelope(`[{"id":"a","t":"nope"}]`),
		"slot":          envelope(`[{"id":"s","t":"slot"}]`),
		"duplicate ids": envelope(`[{"id":"a","t":"text"},{"id":"a","t":"text"}]`),
		"long navlabel": envelope(`[{"id":"a","t":"text","p":{"navlabel":"` + strings.Repeat("n", 81) + `"}}]`),
	} {
		rec, out := r.paste(t, "owner-token", body)
		if rec.Code != http.StatusUnprocessableEntity || out.Error == "" || out.Code == "" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec, _ := r.paste(t, "owner-token", `{nope`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
}

func TestPasteRefusesOversize(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	body := envelope(`[{"id":"a","t":"text","p":{"text":"x"}}]`)
	body = body[:len(body)-1] + `,"pad":"` + strings.Repeat("a", treeTemplateMax) + `"}`
	if rec, _ := r.paste(t, "owner-token", body); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", rec.Code)
	}
}

func TestPasteSanitisesRichText(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	html := `<p onclick=\"x()\">ok</p><script>alert(1)</script><img src=x onerror=alert(1)>`
	rec, out := r.paste(t, "owner-token", envelope(`[{"id":"a","t":"richtext","p":{"html":"`+html+`"}}]`))
	if rec.Code != 200 || len(out.Nodes) != 1 {
		t.Fatalf("paste: %d %s", rec.Code, rec.Body)
	}
	got, _ := out.Nodes[0].P["html"].(string)
	if !strings.Contains(got, "ok") || strings.Contains(got, "script") || strings.Contains(got, "onclick") || strings.Contains(got, "onerror") {
		t.Errorf("html not sanitised: %q", got)
	}
}

func TestPasteListsMissingBlocks(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	const gone = "11111111-2222-3333-4444-555555555555"
	nodes := `[{"id":"r1","t":"ref","p":{"block":"` + r.block.ID.String() + `"}},{"id":"r2","t":"ref","p":{"block":"` + gone + `"}}]`
	rec, out := r.paste(t, "owner-token", envelope(nodes))
	if rec.Code != 200 || len(out.Nodes) != 2 || len(out.MissingBlocks) != 1 || out.MissingBlocks[0] != gone {
		t.Errorf("missing blocks: %d %s", rec.Code, rec.Body)
	}
}

func TestPasteGuards(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	body := envelope(pasteNodes)
	if rec := r.postJSON(r.url(r.page, "/builder/paste"), "owner-token", body, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if rec, _ := r.paste(t, "", body); rec.Code == 200 {
		t.Error("pasted without a session")
	}
	if rec := r.postJSON("/admin/entities/"+fid(999).String()+"/builder/paste", "owner-token", body, nil); rec.Code != 404 {
		t.Errorf("unknown entity: %d", rec.Code)
	}
}

func TestBuilderBootCarriesPasteURL(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	body := r.do("GET", r.path(), "owner-token", nil, nil).Body.String()
	m := bootRx.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no boot element")
	}
	var boot struct {
		URLs map[string]string `json:"urls"`
	}
	if err := json.Unmarshal([]byte(m[1]), &boot); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^/admin/entities/[^/]+/builder/paste$`).MatchString(boot.URLs["paste"]) {
		t.Errorf("urls.paste = %q", boot.URLs["paste"])
	}
}

func TestPasteIsStrict(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	for name, body := range map[string]string{
		"unknown node field": envelope(`[{"id":"a","t":"text","p":{"text":"x"},"bogus":1}]`),
		"trailing data":      envelope(pasteNodes) + `{"more":1}`,
		"null nodes":         `{"hm":"nodes","v":2,"nodes":null}`,
	} {
		rec, out := r.paste(t, "owner-token", body)
		if rec.Code < 400 || rec.Code >= 500 || out.Error == "" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

// A ref to the block being edited (or to a block that holds it) is refused at paste, as the save would.
func TestPasteRefusesRefCycleInBlock(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	self := `[{"id":"r1","t":"ref","p":{"block":"` + r.block.ID.String() + `"}}]`
	rec := r.postJSON(r.url(r.block, "/builder/paste"), "owner-token", envelope(self), nil)
	var out pasteOut
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusUnprocessableEntity || out.Code != "ref_cycle" {
		t.Fatalf("self ref in a block: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := r.paste(t, "owner-token", envelope(self)); rec.Code != 200 {
		t.Errorf("a page may paste a ref to a block: %d %s", rec.Code, rec.Body)
	}
}

func TestPasteLongRichText(t *testing.T) {
	r := newReuseRig(t)
	html, _ := json.Marshal("<p>" + strings.Repeat("word ", 5000) + "</p>")
	rec, _ := r.paste(t, "owner-token", envelope(`[{"id":"r1","t":"richtext","p":{"html":`+string(html)+`}}]`))
	if rec.Code != 200 {
		t.Fatalf("25k richtext paste: %d %s", rec.Code, rec.Body)
	}
}
