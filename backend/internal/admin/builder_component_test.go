package admin

import (
	"bytes"
	"encoding/json"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// fragmentValues reads the named controls of an HTML fragment the way a
// browser would post them (checked boxes, selected options, textareas).
func fragmentValues(frag string) url.Values {
	v := url.Values{}
	for _, tag := range inputRx.FindAllString(frag, -1) {
		name := attr(tag, "name")
		if name == "" || strings.Contains(tag, " disabled") || attr(tag, "type") == "submit" || attr(tag, "type") == "checkbox" && !strings.Contains(tag, " checked") {
			continue
		}
		v.Add(name, html.UnescapeString(attr(tag, "value")))
	}
	for _, m := range textareaRx.FindAllStringSubmatch(frag, -1) {
		v.Add(attr(m[1], "name"), html.UnescapeString(strings.TrimPrefix(m[2], "\n")))
	}
	for _, m := range selectRx.FindAllStringSubmatch(frag, -1) {
		name, picked := attr(m[1], "name"), ""
		for i, o := range optionRx.FindAllStringSubmatch(m[2], -1) {
			if i == 0 || strings.Contains(o[1], " selected") {
				picked = html.UnescapeString(attr(o[1], "value"))
			}
		}
		if name != "" {
			v.Add(name, picked)
		}
	}
	return v
}

type componentOut struct {
	HTML       string              `json:"html"`
	Content    map[string]any      `json:"content"`
	Errors     map[string][]string `json:"errors"`
	Structural bool                `json:"structural"`
	Code       string              `json:"code"`
}

func (r *builderRenderRig) component(t *testing.T, form url.Values) (int, componentOut) {
	t.Helper()
	rec := r.do("POST", r.url(r.page, "/builder/component"), "owner-token", form, nil)
	var out componentOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	return rec.Code, out
}

func rawJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var heroContent = map[string]any{
	"heading": "Hello",
	"image":   "/media/a.png",
	"faqs": []any{
		map[string]any{"question": "Why?", "answer": "<p>Because</p>"},
		map[string]any{"question": "How?", "answer": "<p>Like so</p>"},
	},
}

func TestComponentRoundTrip(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	prev := rawJSON(t, heroContent)
	code, out := r.component(t, url.Values{"type": {"hero"}, "node": {"n1"}, "mode": {"form"}, "prev": {prev}})
	if code != 200 || !strings.Contains(out.HTML, `name="n.n1.c.heading"`) || !strings.Contains(out.HTML, `n.n1.c.faqs.1.question`) {
		t.Fatalf("form: %d %s", code, out.HTML)
	}
	form := fragmentValues(out.HTML)
	form.Set("type", "hero")
	form.Set("node", "n1")
	form.Set("mode", "decode")
	form.Set("prev", prev)
	code, got := r.component(t, form)
	if code != 200 || got.Structural || len(got.Errors) > 0 || got.HTML != "" {
		t.Fatalf("decode: %d %+v", code, got)
	}
	if !reflect.DeepEqual(got.Content, heroContent) {
		t.Errorf("round trip changed the content:\n got %v\nwant %v", got.Content, heroContent)
	}

	// The html field type round trips too.
	htmlContent := map[string]any{"html": "<h2>Story</h2><p>Text &amp; more</p>"}
	_, out = r.component(t, url.Values{"type": {"html"}, "node": {"h"}, "mode": {"form"}, "prev": {rawJSON(t, htmlContent)}})
	form = fragmentValues(out.HTML)
	form.Set("type", "html")
	form.Set("node", "h")
	form.Set("mode", "decode")
	_, got = r.component(t, form)
	if !reflect.DeepEqual(got.Content, htmlContent) {
		t.Errorf("html content: %v", got.Content)
	}
}

func TestComponentStructuralButton(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	prev := rawJSON(t, heroContent)
	_, out := r.component(t, url.Values{"type": {"hero"}, "node": {"n1"}, "mode": {"form"}, "prev": {prev}})
	form := fragmentValues(out.HTML)
	form.Set("type", "hero")
	form.Set("node", "n1")
	form.Set("mode", "decode")
	form.Set("prev", prev)
	form.Set("_add.n.n1.c.faqs", "1")
	code, got := r.component(t, form)
	if code != 200 || !got.Structural || got.HTML == "" || len(got.Errors) > 0 {
		t.Fatalf("add row: %d %+v", code, got)
	}
	if !strings.Contains(got.HTML, `n.n1.c.faqs.2.question`) {
		t.Errorf("the new row is not in the form: %s", got.HTML)
	}
}

func TestComponentValidationErrors(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	form := url.Values{"type": {"hero"}, "node": {"n1"}, "mode": {"decode"}, "prev": {"{}"},
		"n.n1.c.heading": {strings.Repeat("x", 61)}}
	code, got := r.component(t, form)
	if code != 200 || len(got.Errors["heading"]) == 0 || got.HTML == "" || got.Structural {
		t.Fatalf("errors: %d %+v", code, got)
	}
	if got.Content["heading"] != strings.Repeat("x", 61) {
		t.Errorf("the typed value is not echoed: %v", got.Content)
	}
	if !strings.Contains(got.HTML, "n.n1.c.heading") {
		t.Errorf("html lacks the field: %s", got.HTML)
	}
}

func TestComponentPrefixesAreIndependent(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	form := url.Values{"type": {"hero"}, "mode": {"decode"}, "prev": {"{}"},
		"n.a.c.heading": {"Alpha"}, "n.b.c.heading": {"Beta"}}
	for node, want := range map[string]string{"a": "Alpha", "b": "Beta"} {
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		f.Set("node", node)
		_, got := r.component(t, f)
		if got.Content["heading"] != want {
			t.Errorf("node %s decoded %v, want %s", node, got.Content, want)
		}
	}
}

func TestComponentKeepsUnknownKeys(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	prev := rawJSON(t, map[string]any{"heading": "Old", "legacy": "keep", "extra": map[string]any{"a": 1.0}})
	_, got := r.component(t, url.Values{"type": {"hero"}, "node": {"n1"}, "mode": {"decode"}, "prev": {prev}, "n.n1.c.heading": {"New"}})
	if got.Content["heading"] != "New" || got.Content["legacy"] != "keep" || got.Content["extra"] == nil {
		t.Errorf("content: %v", got.Content)
	}
}

func TestComponentMultipartPost(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range map[string]string{"type": "hero", "node": "n1", "mode": "decode", "prev": "{}", "n.n1.c.heading": "Multi"} {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", r.url(r.page, "/builder/component"), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	r.addFetchMeta(req, "owner-token", nil)
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	var got componentOut
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || got.Content["heading"] != "Multi" {
		t.Fatalf("multipart: %d %s", rec.Code, rec.Body)
	}
}

func TestComponentGuards(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	path := r.url(r.page, "/builder/component")
	ok := url.Values{"type": {"hero"}, "node": {"n1"}, "mode": {"form"}, "prev": {"{}"}}
	with := func(k, v string) url.Values {
		f := url.Values{}
		for kk, vv := range ok {
			f[kk] = vv
		}
		f.Set(k, v)
		return f
	}
	if rec := r.do("POST", path, "owner-token", ok, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("no csrf: %d", rec.Code)
	}
	if rec := r.do("POST", path, "", ok, nil); rec.Code == 200 {
		t.Error("answered without a session")
	}
	if rec := r.do("POST", "/admin/entities/"+fid(999).String()+"/builder/component", "owner-token", ok, nil); rec.Code != 404 {
		t.Errorf("wrong id: %d", rec.Code)
	}
	if code, out := r.component(t, with("type", "nope")); code != 422 || out.Code != "unknown_type" {
		t.Errorf("unknown type: %d %+v", code, out)
	}
	if code, out := r.component(t, with("node", "bad id")); code != 422 || out.Code != "invalid_node" {
		t.Errorf("bad node: %d %+v", code, out)
	}
	if code, _ := r.component(t, with("prev", "[1]")); code != 422 {
		t.Errorf("bad prev: %d", code)
	}
	if code, _ := r.component(t, with("mode", "x")); code != 400 {
		t.Errorf("bad mode: %d", code)
	}
}
