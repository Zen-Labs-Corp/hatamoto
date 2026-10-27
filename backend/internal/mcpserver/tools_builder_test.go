package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	hmauth "hatamoto.dev/core/internal/auth"
)

// session opens an MCP client session against ts with the "good" key.
func session(t *testing.T, ts *httptest.Server) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL, HTTPClient: &http.Client{Transport: bearerRT{"good"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// call runs a tool and returns its text and whether it was an error.
func call(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(res.Content) == 0 {
		return "", res.IsError
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func readOnly() []hmauth.APIKeyScope { return []hmauth.APIKeyScope{hmauth.ScopeReadEntities} }

func TestBuilderKinds(t *testing.T) {
	sess := session(t, newTestServer(t, readOnly(), Deps{}))

	text, isErr := call(t, sess, "builder_kinds", nil)
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		Kinds []struct {
			Kind     string `json:"kind"`
			Children bool   `json:"children"`
			OnlyIn   string `json:"onlyIn"`
			Props    []struct {
				Key  string `json:"key"`
				Type string `json:"type"`
			} `json:"props"`
		} `json:"kinds"`
		BindSources []map[string]string `json:"bindSources"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for i, k := range out.Kinds {
		by[k.Kind] = i
	}
	c, ok := by["container"]
	if !ok || !out.Kinds[c].Children {
		t.Fatalf("container kind missing or without children: %s", text)
	}
	h := out.Kinds[by["heading"]]
	if len(h.Props) == 0 || h.Props[0].Key == "" {
		t.Errorf("heading has no props: %+v", h)
	}
	for _, key := range []string{`"u":`, `"ia":`} {
		if strings.Contains(text, key) {
			t.Errorf("terse key %s in builder_kinds", key)
		}
	}
	if !strings.Contains(text, `"universal":["preset"`) || !strings.Contains(text, `"interactive":true`) {
		t.Errorf("readable keys missing: %.300s", text)
	}
	var lim struct {
		UniversalProps []struct {
			Key       string `json:"key"`
			MaxLength int    `json:"maxLength"`
			Pattern   string `json:"pattern"`
		} `json:"universalProps"`
	}
	if err := json.Unmarshal([]byte(text), &lim); err != nil {
		t.Fatal(err)
	}
	limits := map[string]int{}
	patterns := map[string]string{}
	for _, p := range lim.UniversalProps {
		limits[p.Key], patterns[p.Key] = p.MaxLength, p.Pattern
	}
	if limits["aria"] != 200 || limits["tip"] != 200 || patterns["anchor"] == "" || patterns["preset"] == "" {
		t.Errorf("universal prop limits: %+v", lim.UniversalProps)
	}
	onlyIn := false
	for _, k := range out.Kinds {
		onlyIn = onlyIn || k.OnlyIn != ""
	}
	if !onlyIn || len(out.BindSources) == 0 {
		t.Errorf("onlyIn %v, bind sources %d", onlyIn, len(out.BindSources))
	}

	text, isErr = call(t, sess, "builder_kinds", map[string]any{"kind": "heading"})
	if isErr || strings.Count(text, `"kind":"`) != 1 || !strings.Contains(text, `"heading"`) {
		t.Errorf("filter: err=%v %.200s", isErr, text)
	}
	if text, isErr = call(t, sess, "builder_kinds", map[string]any{"kind": "nope"}); !isErr || !strings.Contains(text, "unknown kind") {
		t.Errorf("unknown kind: err=%v %q", isErr, text)
	}
}

func TestBuilderStyleSpec(t *testing.T) {
	sess := session(t, newTestServer(t, readOnly(), Deps{}))
	text, isErr := call(t, sess, "builder_stylespec", nil)
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		Keys        []struct{ Key, Kind string } `json:"keys"`
		Breakpoints []string                     `json:"breakpoints"`
		States      []string                     `json:"states"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Keys) < 10 || strings.Join(out.Breakpoints, ",") != "base,md,sm" || len(out.States) != 4 {
		t.Errorf("keys %d, breakpoints %v, states %v", len(out.Keys), out.Breakpoints, out.States)
	}
}

func TestBuilderDesign(t *testing.T) {
	var out struct {
		Tokens  map[string][]string          `json:"tokens"`
		Palette map[string]map[string]string `json:"palette"`
		Presets []DesignPreset               `json:"presets"`
	}
	decode := func(sess *mcp.ClientSession) {
		t.Helper()
		text, isErr := call(t, sess, "builder_design", nil)
		if isErr {
			t.Fatal(text)
		}
		out.Presets = nil
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
	}
	decode(session(t, newTestServer(t, readOnly(), Deps{})))
	if len(out.Tokens["space"]) == 0 || out.Tokens["space"][0] != "space-0" || len(out.Palette["colors"]) == 0 || out.Presets == nil {
		t.Errorf("defaults: %+v", out)
	}

	d := Deps{Design: func(context.Context) (DesignInfo, error) {
		return DesignInfo{
			Colors: map[string]string{"primary": "#112233"}, Space: map[string]string{"1": "4px"}, FS: map[string]string{"xs": "12px"},
			Presets: []DesignPreset{{Name: "card", Label: "Card", S: map[string]map[string]string{"base": {"pt": "space-4"}}}},
		}, nil
	}}
	decode(session(t, newTestServer(t, readOnly(), d)))
	if out.Palette["colors"]["primary"] != "#112233" || len(out.Presets) != 1 || out.Presets[0].Name != "card" || out.Tokens["color"][0] != "primary" || out.Tokens["space"][0] != "space-1" {
		t.Errorf("site design: %+v", out)
	}
}

// Every builder read tool needs the read scope.
func TestBuilderReadToolsNeedReadScope(t *testing.T) {
	sess := session(t, newTestServer(t, []hmauth.APIKeyScope{hmauth.ScopeReadCitations}, Deps{}))
	for _, name := range []string{"builder_kinds", "builder_stylespec", "builder_design"} {
		if text, isErr := call(t, sess, name, nil); !isErr || !strings.Contains(text, "forbidden") {
			t.Errorf("%s without read:entities: err=%v %q", name, isErr, text)
		}
	}
}

func TestCodedErr(t *testing.T) {
	if got := codedErr(codeConflict, "current version is %d", 7).Error(); got != "conflict: current version is 7" {
		t.Errorf("got %q", got)
	}
}
