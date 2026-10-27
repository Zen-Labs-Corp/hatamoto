package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type nodeVersionOut struct {
	Version int    `json:"version"`
	By      string `json:"by"`
	Summary string `json:"summary"`
	Node    struct {
		T string         `json:"t"`
		P map[string]any `json:"p"`
	} `json:"node"`
	Parent string `json:"parent"`
	Index  int    `json:"index"`
}

func withNode(text string) string {
	return `{"v":2,"nodes":[{"id":"sec","t":"container","c":[{"id":"a","t":"text"},{"id":"h1","t":"heading","p":{"text":"` + text + `"}}]}]}`
}

const withoutNode = `{"v":2,"nodes":[{"id":"sec","t":"container"}]}`

func (r *versionsRig) nodeVersions(t *testing.T, token, query string) (int, []nodeVersionOut) {
	t.Helper()
	rec := r.do("GET", r.url(r.page, "/builder/node-versions"+query), token, nil, nil)
	var out []nodeVersionOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	return rec.Code, out
}

func TestNodeVersionsListsTheVersionsThatHoldTheNode(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t) // v1 seed, v2, current v3
	for _, d := range []string{withoutNode, withNode("Five"), withoutNode, withNode("Now")} {
		r.setDoc(t, d)
	}
	// v4 none, v5 Five, v6 none, current v7; v3 (nodesV3) has h1 "Hello" at index 0 of "hero"
	for _, token := range []string{"owner-token", "editor-token"} {
		code, rows := r.nodeVersions(t, token, "?node=h1")
		if code != http.StatusOK || len(rows) != 3 || rows[0].Version != 5 || rows[1].Version != 3 || rows[2].Version != 2 {
			t.Fatalf("%s: %d %+v", token, code, rows)
		}
		if rows[0].Parent != "sec" || rows[0].Index != 1 || rows[0].Node.T != "heading" || rows[0].Node.P["text"] != "Five" || rows[0].Summary != "set" {
			t.Errorf("row 5: %+v", rows[0])
		}
	}
	if n := len(r.ents.versions[r.page.ID]); n != 6 {
		t.Errorf("versions changed: %d", n)
	}
	if v := r.ents.ents[r.page.ID].Version; v != 7 {
		t.Errorf("page version moved: %d", v)
	}
}

func TestNodeVersionsCapsAtFifty(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	for i := 0; i < 60; i++ {
		r.setDoc(t, withNode(fmt.Sprint("v", i)))
	}
	code, rows := r.nodeVersions(t, "owner-token", "?node=h1")
	if code != http.StatusOK || len(rows) != 50 || rows[0].Version <= rows[49].Version {
		t.Fatalf("%d rows, code %d", len(rows), code)
	}
}

// The search window is the newest 100 saves, not 50: an element last changed before 50 later saves is still found.
func TestNodeVersionsFindsAnElementBeyondTheNewestFifty(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	for i := 0; i < 20; i++ {
		r.setDoc(t, withNode(fmt.Sprint("v", i)))
	}
	for i := 0; i < 70; i++ {
		r.setDoc(t, withoutNode)
	}
	code, rows := r.nodeVersions(t, "owner-token", "?node=h1")
	if code != http.StatusOK || len(rows) < 10 || rows[0].Node.P["text"] != "v19" {
		t.Fatalf("%d rows, code %d", len(rows), code)
	}
}

func TestNodeVersionsNodeMissingIsAnEmptyList(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	rec := r.do("GET", r.url(r.page, "/builder/node-versions?node=nope"), "owner-token", nil, nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestNodeVersionsRefusals(t *testing.T) {
	t.Parallel()

	r := newVersionsRig(t)
	get := func(token, path string) int {
		return r.do("GET", path, token, nil, nil).Code
	}
	if c := get("owner-token", r.url(r.page, "/builder/node-versions")); c != http.StatusBadRequest {
		t.Errorf("missing node: %d", c)
	}
	r.editor.Role = "viewer"
	if c := get("editor-token", r.url(r.page, "/builder/node-versions?node=h1")); c != http.StatusForbidden {
		t.Errorf("viewer: %d", c)
	}
	other := "/admin/entities/00000000-0000-0000-0000-000000000999/builder/node-versions?node=h1"
	if c := get("owner-token", other); c != http.StatusNotFound {
		t.Errorf("unknown entity: %d", c)
	}
	if c := get("", r.url(r.page, "/builder/node-versions?node=h1")); c == http.StatusOK {
		t.Errorf("anonymous: %d", c)
	}
}

func TestNodeVersionsHandlerNeverWrites(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("builder_node_versions.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"POST", "UpdateEntity", "RestoreVersion", "restoreNode", "saveBuilderDoc"} {
		if strings.Contains(string(src), bad) {
			t.Errorf("handler mentions %s", bad)
		}
	}
}

// The shape the client reads; Q/unit/builder-restoreops.test.mjs loads the same file.
func TestNodeVersionsShapeGolden(t *testing.T) {
	t.Parallel()

	row := nodeVersionRow{Version: 12, Saved: time.Date(2026, 10, 26, 16, 2, 0, 0, time.UTC), By: "Ana", Summary: "Saved",
		Node:   nodeVersionContent{T: "heading", P: map[string]any{"level": 2, "text": "Welcome"}, S: map[string]map[string]string{"base": {"fs": "xl"}}, B: map[string]string{"text": "page.title"}},
		Parent: "hero", Index: 1}
	got, err := json.MarshalIndent([]nodeVersionRow{row}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const golden = "testdata/builder_node_versions.golden.json"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil || string(want) != string(got) {
		t.Errorf("shape changed (UPDATE_GOLDEN=1 to accept): %v\n%s", err, got)
	}
}
