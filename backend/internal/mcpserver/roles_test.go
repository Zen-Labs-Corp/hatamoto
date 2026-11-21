package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hatamoto.dev/core/authz"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzsubject"
)

// sampleArgs builds the smallest arguments that satisfy a tool's input schema,
// so a refusal can only come from the authorizer, not from validation.
func sampleArgs(schema map[string]any) map[string]any {
	args := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	req, _ := schema["required"].([]any)
	for _, r := range req {
		name, _ := r.(string)
		p, _ := props[name].(map[string]any)
		args[name] = sampleValue(p)
	}
	return args
}

func sampleValue(p map[string]any) any {
	if enum, ok := p["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	typ := p["type"]
	if list, ok := typ.([]any); ok { // ["null", "array"]
		for _, v := range list {
			if v != "null" {
				typ = v
			}
		}
	}
	switch typ {
	case "integer", "number":
		return 1
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return sampleArgs(p)
	}
	if p["format"] == "uuid" {
		return "6f1b1d52-8b3e-4a52-9c2f-0a1b2c3d4e5f"
	}
	return "x"
}

// A key never outgrows its creator: for a creator who may not write (read-only), every
// write tool is refused before its handler runs (Deps is empty, so reaching
// a handler would panic), while a read tool still works.
func TestReadOnlyKeysCannotWrite(t *testing.T) {
	specs := &Server{mcp: mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)}
	specs.registerContent()
	specs.registerInsight()
	specs.registerBuilder()
	specs.registerSEO()
	specs.registerComments()

	for _, role := range []string{"read-only"} {
		t.Run(role, func(t *testing.T) {
			ts := newTestServerRole(t, role, []hmauth.APIKeyScope{hmauth.ScopeAll}, Deps{})
			sess := session(t, ts)
			tools, err := sess.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			schemas := map[string]map[string]any{}
			for _, tool := range tools.Tools {
				m, _ := tool.InputSchema.(map[string]any)
				schemas[tool.Name] = m
			}
			writes := 0
			for _, spec := range specs.specs {
				if !spec.write {
					continue
				}
				writes++
				text, isErr := call(t, sess, spec.name, sampleArgs(schemas[spec.name]))
				if !isErr || !strings.Contains(text, "forbidden") {
					t.Errorf("%s via %s key = %q, isErr %v; want a forbidden error", spec.name, role, text, isErr)
				}
			}
			if writes == 0 {
				t.Fatal("no write tools registered")
			}
			// A read tool is still allowed. Calling it would reach the handler
			// (no service behind Deps), so ask the authorizer the tool asks.
			for _, spec := range specs.specs {
				if spec.name != "entities_list" {
					continue
				}
				sub := authzsubject.ForKey(&hmauth.APIKey{CreatedBy: &fakeOwner, CreatorRole: role, Scopes: []hmauth.APIKeyScope{hmauth.ScopeAll}})
				if ok, err := authz.Default().Can(t.Context(), sub, spec.action, authz.Object{}); err != nil || !ok {
					t.Errorf("entities_list for a %s key = %v, %v; want allowed", role, ok, err)
				}
			}
		})
	}
}

// contentRoleTools are the write tools an author, contributor or reviewer key may reach, with the zero
// Object the gate asks: everything else is refused at the gate.
var contentRoleTools = map[string][]string{
	"author":      {"entities_create", "entities_update", "entities_publish", "entities_unpublish", "media_upload", "builder_apply_ops"},
	"contributor": {"entities_create", "entities_update", "media_upload", "builder_apply_ops"},
	"reviewer":    {"entities_update", "entities_publish", "entities_unpublish", "media_upload", "builder_apply_ops"},
}

// The author, contributor and reviewer roles act within their content rights: the gate answers each write
// tool from the rights table. (The tools themselves are not called here: no service sits behind Deps.)
func TestContentRoleKeysGate(t *testing.T) {
	specs := &Server{mcp: mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)}
	specs.registerContent()
	specs.registerInsight()
	specs.registerBuilder()
	specs.registerSEO()
	specs.registerComments()

	for role, allowed := range contentRoleTools {
		sub := authzsubject.ForKey(&hmauth.APIKey{CreatedBy: &fakeOwner, CreatorRole: role, Scopes: []hmauth.APIKeyScope{hmauth.ScopeAll}})
		seen := 0
		for _, spec := range specs.specs {
			if !spec.write {
				continue
			}
			ok, err := authz.Default().Can(t.Context(), sub, spec.action, authz.Object{})
			if err != nil {
				t.Fatalf("%s: %v", spec.name, err)
			}
			if want := slices.Contains(allowed, spec.name); ok != want {
				t.Errorf("%s key, tool %s (%s): allowed = %v, want %v", role, spec.name, spec.action, ok, want)
			}
			if ok {
				seen++
			}
		}
		if seen != len(allowed) {
			t.Errorf("%s key: %d write tools allowed, want %d (%v)", role, seen, len(allowed), allowed)
		}
	}
}
