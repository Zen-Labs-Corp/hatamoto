package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/sectiontemplate"
)

type fakeTemplates struct {
	items []sectiontemplate.SectionTemplate
}

func (f fakeTemplates) List(_ context.Context, flt *sectiontemplate.ListFilter, limit, _ int) (*sectiontemplate.ListResult, error) {
	var out []sectiontemplate.SectionTemplate
	for _, t := range f.items {
		if flt.SectionType == "" || t.SectionType == flt.SectionType {
			out = append(out, t)
		}
	}
	return &sectiontemplate.ListResult{Items: out[:min(len(out), limit)], Total: len(out)}, nil
}

func (f fakeTemplates) Get(_ context.Context, id uuid.UUID) (*sectiontemplate.SectionTemplate, error) {
	for _, t := range f.items {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, sectiontemplate.ErrTemplateNotFound
}

func TestBuilderTemplates(t *testing.T) {
	tree := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Hero", Description: "A hero", SectionType: treeTemplateType,
		Content: json.RawMessage(`{"v":1,"nodes":[{"id":"t1","t":"heading","p":{"text":"Big"}},{"id":"bad","t":"nope"}]}`)}
	legacy := sectiontemplate.SectionTemplate{ID: uuid.New(), Name: "Old", SectionType: "hero", Content: json.RawMessage(`{}`)}
	r := newRig(t)
	st := r.st
	sess := session(t, newTestServer(t, readOnly(), Deps{BuilderEntities: st, Templates: fakeTemplates{[]sectiontemplate.SectionTemplate{tree, legacy}}}))

	text, isErr := call(t, sess, "builder_templates_list", nil)
	m := decode(t, text)
	items := m["templates"].([]any)
	if isErr || len(items) != 1 || items[0].(map[string]any)["name"] != "Hero" || strings.Contains(text, "Old") {
		t.Fatalf("list: err=%v %s", isErr, text)
	}

	var firstID string
	for i := 0; i < 2; i++ {
		text, isErr = call(t, sess, "builder_template_nodes", map[string]any{"id": tree.ID.String()})
		if isErr {
			t.Fatal(text)
		}
		nodes := decode(t, text)["nodes"].([]any)
		if len(nodes) != 1 { // the unreadable node is skipped
			t.Fatalf("nodes: %s", text)
		}
		id := nodes[0].(map[string]any)["id"].(string)
		if id == "t1" || id == "" {
			t.Errorf("ids are not fresh: %s", id)
		}
		if i == 1 && id == firstID {
			t.Error("ids repeat between calls")
		}
		firstID = id
	}
	if text, isErr := call(t, sess, "builder_template_nodes", map[string]any{"id": legacy.ID.String()}); !isErr || !strings.Contains(text, "only tree templates") {
		t.Errorf("legacy: err=%v %q", isErr, text)
	}
	if text, isErr := call(t, sess, "builder_template_nodes", map[string]any{"id": uuid.NewString()}); !isErr || !strings.Contains(text, "no such template") {
		t.Errorf("unknown: err=%v %q", isErr, text)
	}

	// Without a template store the list is empty and the fetch says so.
	bare := session(t, newTestServer(t, readOnly(), Deps{BuilderEntities: st}))
	if text, _ := call(t, bare, "builder_templates_list", nil); !strings.Contains(text, `"templates":[]`) {
		t.Errorf("no store: %s", text)
	}
	if text, isErr := call(t, bare, "builder_template_nodes", map[string]any{"id": tree.ID.String()}); !isErr || !strings.HasPrefix(text, "unavailable:") {
		t.Errorf("no store fetch: err=%v %q", isErr, text)
	}
}

func TestBuilderRender(t *testing.T) {
	r := newRig(t)
	var got RenderRequest
	var out RenderResult
	var rerr error
	sess := session(t, newTestServer(t, readOnly(), Deps{BuilderEntities: r.st, RenderPage: func(_ context.Context, req RenderRequest) (RenderResult, error) {
		got = req
		return out, rerr
	}}))
	id := r.page.ID.String()

	out = RenderResult{HTML: "<h1>Hi</h1>", CSS: ".a{}"}
	text, isErr := call(t, sess, "builder_render", map[string]any{"entity_id": id})
	if m := decode(t, text); isErr || m["html"] != "<h1>Hi</h1>" || m["css"] != ".a{}" {
		t.Fatalf("page: err=%v %s", isErr, text)
	}
	if got.Entity.ID != r.page.ID || !strings.Contains(string(got.Doc), `"Hi"`) || len(got.IDs) != 0 {
		t.Errorf("request: %+v", got)
	}

	// A dry run: the ops reach the renderer, the store stays untouched.
	text, isErr = call(t, sess, "builder_render", map[string]any{"entity_id": id, "ids": []string{"h"},
		"ops": []map[string]any{{"op": "setProp", "id": "h", "key": "text", "value": "Draft"}}})
	if isErr || !strings.Contains(string(got.Doc), `"Draft"`) || len(got.IDs) != 1 {
		t.Errorf("dry run: err=%v %q req=%s", isErr, text, got.Doc)
	}
	if len(r.st.updates) != 0 {
		t.Error("render stored something")
	}

	// The ids generated for the ops come back, so they can be named in ids.
	text, _ = call(t, sess, "builder_render", map[string]any{"entity_id": id,
		"ops": []map[string]any{{"op": "insert", "node": map[string]any{"t": "text", "p": map[string]any{"text": "x"}}}}})
	gen, _ := decode(t, text)["ids"].([]any)
	if len(gen) != 1 || gen[0].(map[string]any)["path"] != "node" || gen[0].(map[string]any)["id"] == "" {
		t.Errorf("generated ids: %s", text)
	}

	for name, args := range map[string]map[string]any{
		"unknown id":   {"entity_id": id, "ids": []string{"zz"}},
		"bad op":       {"entity_id": id, "ops": []map[string]any{{"op": "remove", "id": "zz"}}},
		"too many ids": {"entity_id": id, "ids": make([]string, maxRenderIDs+1)},
	} {
		if text, isErr := call(t, sess, "builder_render", args); !isErr {
			t.Errorf("%s: want an error, got %q", name, text)
		}
	}

	out = RenderResult{HTML: strings.Repeat("x", maxRenderBytes+1)}
	if text, isErr := call(t, sess, "builder_render", map[string]any{"entity_id": id}); !isErr || !strings.HasPrefix(text, "limit:") {
		t.Errorf("2 MiB: err=%v %.60q", isErr, text)
	}
	out, rerr = RenderResult{}, ErrRenderUnavailable
	if text, isErr := call(t, sess, "builder_render", map[string]any{"entity_id": id}); !isErr || !strings.HasPrefix(text, "unavailable:") {
		t.Errorf("engine off: err=%v %q", isErr, text)
	}

	bare := session(t, newTestServer(t, readOnly(), Deps{BuilderEntities: r.st}))
	if text, isErr := call(t, bare, "builder_render", map[string]any{"entity_id": id}); !isErr || !strings.HasPrefix(text, "unavailable:") {
		t.Errorf("no dep: err=%v %q", isErr, text)
	}
}

func TestJoinCSS(t *testing.T) {
	if got := JoinCSS(map[string]string{"b": ".b{}", "a": ".a{}"}); got != ".a{}\n.b{}\n" {
		t.Errorf("got %q", got)
	}
}
