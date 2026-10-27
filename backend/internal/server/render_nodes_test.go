package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/render"
)

// nodesDoc is three converted sections (ids a, b, c) and a native container.
func nodesDoc() *builder.Doc {
	d := builder.FromSections([]builder.Section{
		{ID: "a", Type: "html", SortOrder: 1, Content: map[string]any{"body": "<p>one</p>"}},
		{ID: "b", Type: "html", SortOrder: 2, Content: map[string]any{"body": "<p>two</p>"}},
		{ID: "c", Type: "html", SortOrder: 3, Content: map[string]any{"body": "<p>three</p><script>alert(1)</script>"}},
	})
	d.Nodes = append(d.Nodes, &builder.Node{ID: "row", T: "container",
		S: map[string]map[string]string{"base": {"ta": "center"}},
		C: []*builder.Node{{ID: "h", T: "heading", P: map[string]any{"text": "Hi", "level": float64(2)}}}})
	return d
}

func nodesBody(t testing.TB, d *builder.Doc, extra map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	req := map[string]any{
		"schema":     "page",
		"attributes": map[string]any{"page_doc": json.RawMessage(raw)},
	}
	for k, v := range extra {
		req[k] = v
	}
	b, _ := json.Marshal(req)
	return string(b)
}

type nodesReply struct {
	Fragments []render.Fragment `json:"fragments"`
	CSS       map[string]string `json:"css"`
	Order     []string          `json:"order"`
	Seq       int               `json:"seq"`
	Error     string            `json:"error"`
}

func postNodes(t *testing.T, body string) (*httptest.ResponseRecorder, nodesReply) {
	t.Helper()
	w := httptest.NewRecorder()
	newPreviewHandlers(t).handleRenderNodes(w, httptest.NewRequest(http.MethodPost, "/edit/render", strings.NewReader(body)))
	var out nodesReply
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestRenderNodes(t *testing.T) {
	op := func(s string) json.RawMessage { return json.RawMessage(s) }
	tests := []struct {
		name   string
		extra  map[string]any
		status int
		check  func(t *testing.T, r nodesReply)
	}{
		{"converted section", map[string]any{"render": []string{"b"}, "seq": 7}, 200, func(t *testing.T, r nodesReply) {
			if len(r.Fragments) != 1 || r.Fragments[0].Sel != "section" || r.Fragments[0].Key != "b" ||
				!strings.Contains(r.Fragments[0].HTML, `data-hm-section="b"`) || !strings.Contains(r.Fragments[0].HTML, "two") {
				t.Fatalf("fragments: %+v", r.Fragments)
			}
			if r.Seq != 7 || strings.Join(r.Order, ",") != "a,b,c,row" {
				t.Errorf("seq=%d order=%v", r.Seq, r.Order)
			}
		}},
		{"native with css", map[string]any{"render": []string{"row"}}, 200, func(t *testing.T, r nodesReply) {
			if len(r.Fragments) != 1 || r.Fragments[0].Sel != "node" || r.Fragments[0].CSS["row"] == "" ||
				!strings.Contains(r.Fragments[0].HTML, `data-hm-node="row"`) {
				t.Fatalf("fragments: %+v", r.Fragments)
			}
		}},
		{"nested node", map[string]any{"render": []string{"h"}}, 200, func(t *testing.T, r nodesReply) {
			if len(r.Fragments) != 1 || r.Fragments[0].Key != "h" || !strings.Contains(r.Fragments[0].HTML, "<h2") {
				t.Fatalf("fragments: %+v", r.Fragments)
			}
		}},
		{"unknown id skipped", map[string]any{"render": []string{"nope"}}, 200, func(t *testing.T, r nodesReply) {
			if len(r.Fragments) != 0 {
				t.Fatalf("fragments: %+v", r.Fragments)
			}
		}},
		{"move reflected in order", map[string]any{
			"ops":    []json.RawMessage{op(`{"op":"move","id":"c-c","index":0}`)},
			"render": []string{"c"},
		}, 200, func(t *testing.T, r nodesReply) {
			if got := strings.Join(r.Order, ","); got != "c,a,b,row" {
				t.Errorf("order = %s", got)
			}
			if len(r.Fragments) != 1 || r.Fragments[0].Key != "c" {
				t.Errorf("fragments: %+v", r.Fragments)
			}
		}},
		{"setStyle and css", map[string]any{
			"ops": []json.RawMessage{op(`{"op":"setStyle","id":"row","bp":"base","key":"pt","value":"space-2"}`)},
			"css": []string{"row"},
		}, 200, func(t *testing.T, r nodesReply) {
			if !strings.Contains(r.CSS["row"], "padding-top") {
				t.Errorf("css = %v", r.CSS)
			}
		}},
		{"remove op", map[string]any{"ops": []json.RawMessage{op(`{"op":"remove","id":"c-a"}`)}}, 200, func(t *testing.T, r nodesReply) {
			if got := strings.Join(r.Order, ","); got != "b,c,row" {
				t.Errorf("order = %s", got)
			}
		}},
		{"unknown node", map[string]any{"ops": []json.RawMessage{op(`{"op":"remove","id":"ghost"}`)}}, 422, nil},
		{"bad index", map[string]any{"ops": []json.RawMessage{op(`{"op":"move","id":"c-a","index":99}`)}}, 422, nil},
		{"bad style value", map[string]any{"ops": []json.RawMessage{op(`{"op":"setStyle","id":"row","bp":"base","key":"pt","value":"red;}"}`)}}, 422, nil},
		{"duplicate id breaks validation", map[string]any{"ops": []json.RawMessage{op(`{"op":"insert","index":0,"node":{"id":"row","t":"divider"}}`)}}, 422, nil},
		{"too many ops", map[string]any{"ops": make([]json.RawMessage, 101)}, 400, nil},
		{"too many ids", map[string]any{"render": make([]string, 51)}, 400, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ops, ok := tt.extra["ops"].([]json.RawMessage); ok && len(ops) == 101 {
				for i := range ops {
					ops[i] = json.RawMessage(`{"op":"remove","id":"x"}`)
				}
			}
			w, r := postNodes(t, nodesBody(t, nodesDoc(), tt.extra))
			if w.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			if tt.status == 422 && r.Error == "" {
				t.Error("422 without error")
			}
			if tt.check != nil {
				tt.check(t, r)
			}
			if h := w.Header(); h.Get("Cache-Control") != "no-store" && tt.status == 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
				t.Errorf("headers: %v", h)
			}
		})
	}
}

func TestRenderNodesNoDoc(t *testing.T) {
	for name, body := range map[string]string{
		"no page_doc":   `{"schema":"page","attributes":{"page_sections":[{"id":"a","type":"html","sort_order":1,"content":{"body":"x"}}]}}`,
		"invalid doc":   `{"schema":"page","attributes":{"page_doc":{"v":2,"nodes":[{"id":"bad id","t":"divider"}]}}}`,
		"null page_doc": `{"schema":"page","attributes":{"page_doc":null}}`,
	} {
		w, r := postNodes(t, body)
		if w.Code != http.StatusConflict || r.Error != "no_doc" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestRenderNodesSanitisesPostedContent(t *testing.T) {
	w, r := postNodes(t, nodesBody(t, nodesDoc(), map[string]any{"render": []string{"c"}}))
	if w.Code != 200 || len(r.Fragments) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(r.Fragments[0].HTML, "alert(1)") || !strings.Contains(r.Fragments[0].HTML, "three") {
		t.Errorf("fragment: %s", r.Fragments[0].HTML)
	}
}

func TestRenderNodesSanitisesInlineHTML(t *testing.T) {
	d := nodesDoc()
	d.Nodes[3].C[0].P["html"] = `Hi <strong>there</strong><img src=x onerror=alert(1)><a href="javascript:alert(2)">l</a>`
	w, r := postNodes(t, nodesBody(t, d, map[string]any{"render": []string{"row"}}))
	if w.Code != 200 || len(r.Fragments) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := r.Fragments[0].HTML
	if !strings.Contains(got, `data-hm-prop="html">Hi <strong>there</strong>l</h2>`) || strings.Contains(got, "alert") {
		t.Errorf("fragment: %s", got)
	}
}

func TestRenderNodesRejectsBadBodies(t *testing.T) {
	if w, _ := postNodes(t, `{nope`); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	big := `{"schema":"page","attributes":{"x":"` + strings.Repeat("a", maxPreviewBodySize) + `"}}`
	if w, _ := postNodes(t, big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", w.Code)
	}
}

// A fragment is the node's slice of a full canvas render of the same draft.
func TestRenderNodesFragmentIsPageSlice(t *testing.T) {
	d := nodesDoc()
	_, r := postNodes(t, nodesBody(t, d, map[string]any{"render": []string{"a", "row"}}))
	h := newPreviewHandlers(t)
	v := &render.PageView{BaseURL: "https://x.io", Path: "/", Doc: d, Canvas: true}
	var buf strings.Builder
	if err := h.engine.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	if len(r.Fragments) != 2 {
		t.Fatalf("fragments: %+v", r.Fragments)
	}
	for _, f := range r.Fragments {
		if !strings.Contains(buf.String(), f.HTML) {
			t.Errorf("%s: fragment not in full render", f.Key)
		}
	}
}

// docBody is a request in doc mode: the doc travels beside the attributes.
func docBody(t testing.TB, d *builder.Doc, attrs map[string]any, extra map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	req := map[string]any{"schema": "page", "doc": json.RawMessage(raw), "attributes": attrs}
	for k, v := range extra {
		req[k] = v
	}
	b, _ := json.Marshal(req)
	return string(b)
}

func TestRenderNodesDocMode(t *testing.T) {
	builderOnly := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "box", T: "container",
		C: []*builder.Node{{ID: "h1", T: "heading", P: map[string]any{"text": "Doc mode", "level": float64(1)}}}}}}

	t.Run("builder-only doc without page_doc attributes", func(t *testing.T) {
		w, r := postNodes(t, docBody(t, builderOnly, map[string]any{}, map[string]any{"render": []string{"box"}, "css": []string{"box"}}))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if len(r.Fragments) != 1 || r.Fragments[0].Key != "box" || !strings.Contains(r.Fragments[0].HTML, "Doc mode") {
			t.Fatalf("fragments: %+v", r.Fragments)
		}
		if strings.Join(r.Order, ",") != "box" {
			t.Errorf("order = %v", r.Order)
		}
	})
	t.Run("no attributes at all", func(t *testing.T) {
		if w, _ := postNodes(t, `{"schema":"page","doc":{"v":2,"nodes":[]}}`); w.Code != 200 {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	})
	t.Run("invalid doc", func(t *testing.T) {
		w, _ := postNodes(t, `{"schema":"page","doc":{"v":2,"nodes":[{"id":"bad id","t":"divider"}]}}`)
		var out struct{ Error, Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusUnprocessableEntity || out.Code != "invalid_page_doc" || out.Error == "" {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	})
	t.Run("ops apply to the doc", func(t *testing.T) {
		body := docBody(t, nodesDoc(), nil, map[string]any{
			"ops":    []json.RawMessage{json.RawMessage(`{"op":"move","id":"c-c","index":0}`)},
			"render": []string{"c"},
		})
		w, r := postNodes(t, body)
		if w.Code != 200 || strings.Join(r.Order, ",") != "c,a,b,row" {
			t.Errorf("%d order=%v", w.Code, r.Order)
		}
	})
	t.Run("doc wins over attributes.page_doc", func(t *testing.T) {
		raw, _ := json.Marshal(nodesDoc())
		w, r := postNodes(t, docBody(t, builderOnly, map[string]any{"page_doc": json.RawMessage(raw)}, nil))
		if w.Code != 200 || strings.Join(r.Order, ",") != "box" {
			t.Errorf("%d order=%v", w.Code, r.Order)
		}
	})
	t.Run("doc still sanitised and validated", func(t *testing.T) {
		w, r := postNodes(t, docBody(t, nodesDoc(), nil, map[string]any{"render": []string{"c"}}))
		if w.Code != 200 || len(r.Fragments) != 1 || strings.Contains(r.Fragments[0].HTML, "alert(1)") {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	})
	t.Run("null doc falls back to attributes", func(t *testing.T) {
		if w, r := postNodes(t, `{"schema":"page","doc":null}`); w.Code != http.StatusConflict || r.Error != "no_doc" {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	})
}

func TestLoopHostsRendersTheLoopForItsDescendants(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{
		{ID: "lp", T: "loop", C: []*builder.Node{{ID: "it", T: "container", C: []*builder.Node{{ID: "t1", T: "heading"}}}}},
		{ID: "o", T: "container", C: []*builder.Node{{ID: "o1", T: "text"}}},
	}}
	got := loopHosts(d, []string{"t1", "it", "o1", "lp", "lp"})
	if strings.Join(got, ",") != "lp,o1" {
		t.Fatalf("hosts = %v", got)
	}
}

// A nested loop is rendered twice (RenderTop, then RenderNode): it must count once against the cap.
func TestRenderNodesNestedLoopCountsOnceAtTheCap(t *testing.T) {
	loop := func(id string) *builder.Node {
		return &builder.Node{ID: id, T: "loop", P: map[string]any{"from": "archive"}}
	}
	for _, tc := range []struct {
		name  string
		loops int
		over  bool
	}{{"eight loops", 8, false}, {"nine loops", 9, true}} {
		t.Run(tc.name, func(t *testing.T) {
			d := &builder.Doc{V: 2}
			for i := 0; i < tc.loops-1; i++ {
				d.Nodes = append(d.Nodes, loop("l"+string(rune('a'+i))))
			}
			d.Nodes = append(d.Nodes, &builder.Node{ID: "box", T: "container", C: []*builder.Node{loop("nested")}})
			_, r := postNodes(t, nodesBody(t, d, map[string]any{"render": []string{"nested"}}))
			if len(r.Fragments) != 1 || r.Fragments[0].Key != "nested" {
				t.Fatalf("fragments: %+v", r.Fragments)
			}
			if got := strings.Contains(r.Fragments[0].HTML, "Loop limit reached"); got != tc.over {
				t.Errorf("limit notice = %v, want %v: %s", got, tc.over, r.Fragments[0].HTML)
			}
		})
	}
}

// A commit that only wants a node shown again re-sets what it has (the builder's repatchOps): a plain heading keeps
// its text and a component is accepted (a made-up text "" blanked the one and was a 422 for the other).
func TestRenderNodesRepatchKeepsTheNode(t *testing.T) {
	op := func(id, key string, v any) map[string]any {
		raw, _ := json.Marshal(v)
		return map[string]any{"op": "setProp", "id": id, "key": key, "value": json.RawMessage(raw)}
	}
	w, r := postNodes(t, nodesBody(t, nodesDoc(), map[string]any{"render": []string{"row"}, "ops": []any{op("h", "text", "Hi")}}))
	if w.Code != 200 || len(r.Fragments) != 1 || !strings.Contains(r.Fragments[0].HTML, ">Hi</h2>") {
		t.Fatalf("plain heading: %d %s", w.Code, w.Body)
	}
	d := nodesDoc()
	comp := d.Nodes[0].C[0]
	typ := comp.P["type"]
	if typ == nil {
		t.Fatalf("first node has no type prop: %+v", comp)
	}
	if w, _ = postNodes(t, nodesBody(t, d, map[string]any{"render": []string{comp.ID}, "ops": []any{op(comp.ID, "type", typ)}})); w.Code != 200 {
		t.Errorf("component: %d %s", w.Code, w.Body)
	}
}

// richtext keeps its own 100,000 limit; an over-limit heading gets the clear 422.
func TestRenderNodesPerKindHTMLLimit(t *testing.T) {
	d := nodesDoc()
	d.Nodes = append(d.Nodes, &builder.Node{ID: "rt", T: "richtext", P: map[string]any{"html": "<p>" + strings.Repeat("word ", 5000) + "</p>"}})
	if w, _ := postNodes(t, nodesBody(t, d, map[string]any{"render": []string{"rt"}})); w.Code != 200 {
		t.Fatalf("25k richtext: %d %s", w.Code, w.Body)
	}
	d = nodesDoc()
	d.Nodes[3].C[0].P["html"] = strings.Repeat("<a href=/x>", 1818) // grows once cleaned
	w, r := postNodes(t, nodesBody(t, d, map[string]any{"render": []string{"row"}}))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Error, "too long once cleaned") {
		t.Errorf("heading: %d %s", w.Code, w.Body)
	}
}
