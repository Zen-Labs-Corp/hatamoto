package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildMenuOrderAndParents(t *testing.T) {
	t.Parallel()
	pages := testPages("v1")
	pages[1].NavTitle = "Section A"
	ids := map[string]string{"a": "ida", "a/one": "id1", "a/two": "id2", "b": "idb", "b/three": "id3"}
	got := BuildMenu(testSections, pages, ids)
	type row struct{ Key, Parent, Label, ID string }
	var rows []row
	for _, it := range got {
		if it.LinkType != "entity" {
			t.Errorf("%s link_type %q", it.Key, it.LinkType)
		}
		rows = append(rows, row{it.Key, it.ParentKey, it.Label, it.EntityID})
	}
	want := []row{
		{"a", "", "Section A", "ida"}, {"a/one", "a", "N", "id1"}, {"a/two", "a", "N", "id2"},
		{"b", "", "N", "idb"}, {"b/three", "b", "N", "id3"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("got %+v\nwant %+v", rows, want)
	}
	// Every parent_key names an earlier item, as menus_update requires.
	seen := map[string]bool{}
	for _, it := range got {
		if it.ParentKey != "" && !seen[it.ParentKey] {
			t.Errorf("%s names %s before it is listed", it.Key, it.ParentKey)
		}
		seen[it.Key] = true
	}
}

var groupedSections = []SectionSpec{{
	Slug: "a", Title: "A",
	Guides:    []PageRef{{Slug: "one"}, {Slug: "two"}},
	Reference: []PageRef{{Slug: "ref"}},
}, {
	Slug: "b", Title: "B",
	Reference: []PageRef{{Slug: "r2"}},
}, {
	Slug: "c", Title: "C", Guides: []PageRef{{Slug: "g"}},
}}

func TestBuildMenuGroups(t *testing.T) {
	t.Parallel()
	ids := map[string]string{"a": "ida", "a/one": "id1", "a/two": "id2", "a/ref": "idr", "b": "idb", "b/r2": "idr2", "c": "idc"}
	got := BuildMenu(groupedSections, nil, ids)
	type row struct{ Key, Parent, Label, Type, ID string }
	var rows []row
	for _, it := range got {
		rows = append(rows, row{it.Key, it.ParentKey, it.Label, it.LinkType, it.EntityID})
	}
	want := []row{
		{"a", "", "A", "entity", "ida"},
		{"a#guides", "a", "Guides", "custom", ""},
		{"a/one", "a#guides", "", "entity", "id1"}, {"a/two", "a#guides", "", "entity", "id2"},
		{"a#reference", "a", "Reference", "custom", ""},
		{"a/ref", "a#reference", "", "entity", "idr"},
		{"b", "", "B", "entity", "idb"},
		{"b#reference", "b", "Reference", "custom", ""},
		{"b/r2", "b#reference", "", "entity", "idr2"},
		{"c", "", "C", "entity", "idc"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("got %+v\nwant %+v", rows, want)
	}
	seen := map[string]bool{}
	for _, it := range got {
		if it.ParentKey != "" && !seen[it.ParentKey] {
			t.Errorf("%s names %s before it is listed", it.Key, it.ParentKey)
		}
		seen[it.Key] = true
	}
}

func TestMenuMatchesGroups(t *testing.T) {
	t.Parallel()
	ids := map[string]string{"a": "ida", "a/one": "id1", "a/ref": "idr", "b": "idb"}
	secs := []SectionSpec{
		{Slug: "a", Title: "A", Guides: []PageRef{{Slug: "one", Title: "One"}}, Reference: []PageRef{{Slug: "ref", Title: "Ref"}}},
		{Slug: "b", Title: "B"},
	}
	want := BuildMenu(secs, nil, ids)
	tree := func() []*MenuNode {
		return []*MenuNode{
			{Label: "A", LinkType: "entity", EntityID: "ida", Children: []*MenuNode{
				{Label: "Guides", LinkType: "custom", Children: []*MenuNode{{Label: "One", LinkType: "entity", EntityID: "id1"}}},
				{Label: "Reference", LinkType: "custom", Children: []*MenuNode{{Label: "Ref", LinkType: "entity", EntityID: "idr"}}},
			}},
			{Label: "B", LinkType: "entity", EntityID: "idb"},
		}
	}
	if !MenuMatches(tree(), want) {
		t.Fatal("identical grouped menu must match")
	}
	for name, mut := range map[string]func([]*MenuNode) []*MenuNode{
		"group label": func(n []*MenuNode) []*MenuNode { n[0].Children[0].Label = "Guide"; return n },
		"group link":  func(n []*MenuNode) []*MenuNode { n[0].Children[0].URL = "/x"; return n },
		"wrong group": func(n []*MenuNode) []*MenuNode {
			n[0].Children[0], n[0].Children[1] = n[0].Children[1], n[0].Children[0]
			return n
		},
		"ungrouped":     func(n []*MenuNode) []*MenuNode { n[0].Children = n[0].Children[0].Children; return n },
		"page to group": func(n []*MenuNode) []*MenuNode { n[0].Children[0].Children[0].EntityID = "x"; return n },
	} {
		if MenuMatches(mut(tree()), want) {
			t.Errorf("%s: a different menu matched", name)
		}
	}
}

func TestBuildMenuSkipsPagesWithoutID(t *testing.T) {
	t.Parallel()
	got := BuildMenu(testSections, testPages("v1"), map[string]string{"a": "ida", "a/two": "id2"})
	if len(got) != 2 || got[1].Key != "a/two" {
		t.Fatalf("%+v", got)
	}
}

// site fixtures -----------------------------------------------------------

const bearer = "Bearer " + fakeKey

// stack serves the REST fake and a fake MCP endpoint on one server and keeps
// one ordered event log across both.
type stack struct {
	*fakeSite
	mu            sync.Mutex
	events        []string
	menuID        string // "" = no sidebar menu
	noSection     bool   // the doc schema lacks section_pages
	sse           bool
	items         []MenuItem
	redirects     []redirectRow // stored redirect rules, in creation order
	hideRedirects bool          // redirects_list returns nothing, to force a duplicate create
	srv           *httptest.Server
}

// redirectRow is one stored redirect of the fake site.
type redirectRow struct {
	ID         string `json:"id"`
	SourcePath string `json:"source_path"`
	TargetURL  string `json:"target_url"`
	StatusCode int    `json:"status_code"`
}

func newStack(t *testing.T) *stack {
	t.Helper()
	s := &stack{fakeSite: newFakeSite(t), menuID: "menu-1"}
	s.srv = httptest.NewServer(http.HandlerFunc(s.route))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stack) log(e string) { s.mu.Lock(); s.events = append(s.events, e); s.mu.Unlock() }

func (s *stack) cfg() Config { return Config{URL: s.srv.URL, APIKey: fakeKey} }

func (s *stack) clients() (*Client, *MCP) {
	c := NewClient(s.cfg())
	c.Delay = 0
	c.Sleep = func(d time.Duration) {}
	return c, NewMCP(s.cfg())
}

func (s *stack) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		if r.Method != http.MethodGet {
			s.log("REST " + r.Method + " " + r.URL.Path)
		}
		s.fakeSite.serve(w, r)
		return
	}
	if r.Header.Get("Authorization") != bearer {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-1" {
		http.Error(w, "no session", http.StatusBadRequest)
		return
	}
	reply := func(result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		if s.sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
	text := func(v any, isErr bool) {
		b, _ := json.Marshal(v)
		reply(map[string]any{"isError": isErr, "content": []map[string]string{{"type": "text", "text": string(b)}}})
	}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-1")
		reply(map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.log("MCP " + p.Name)
		switch p.Name {
		case "menus_get":
			if s.menuID == "" {
				text("menu not found", true)
				return
			}
			text(map[string]any{"id": s.menuID, "slug": "docs-sidebar", "items": menuTree(s.items)}, false)
		case "schemas_get":
			text(map[string]any{"slug": "doc", "settings": map[string]any{"section_pages": !s.noSection}}, false)
		case "menus_update":
			var a struct {
				ID    string     `json:"id"`
				Items []MenuItem `json:"items"`
			}
			_ = json.Unmarshal(p.Arguments, &a)
			if a.ID != s.menuID {
				text("menu not found", true)
				return
			}
			s.items = a.Items
			text(map[string]any{"id": a.ID}, false)
		case "redirects_list":
			var a struct {
				Search  string `json:"search"`
				Page    int    `json:"page"`
				PerPage int    `json:"per_page"`
			}
			_ = json.Unmarshal(p.Arguments, &a)
			all := []redirectRow{}
			for _, r := range s.redirects {
				if !s.hideRedirects && strings.Contains(r.SourcePath, a.Search) {
					all = append(all, r)
				}
			}
			// like the server: page 1 by default, 50 per page, total of all matches
			if a.Page < 1 {
				a.Page = 1
			}
			if a.PerPage < 1 {
				a.PerPage = 50
			}
			items := []redirectRow{}
			if lo := (a.Page - 1) * a.PerPage; lo < len(all) {
				items = all[lo:min(lo+a.PerPage, len(all))]
			}
			text(map[string]any{"items": items, "total": len(all)}, false)
		case "redirects_create":
			var a struct {
				SourcePath string `json:"source_path"`
				TargetURL  string `json:"target_url"`
				StatusCode int    `json:"status_code"`
			}
			_ = json.Unmarshal(p.Arguments, &a)
			for _, r := range s.redirects {
				if r.SourcePath == a.SourcePath {
					text("redirect with this source path already exists", true)
					return
				}
			}
			r := redirectRow{ID: fmt.Sprintf("rd-%d", len(s.redirects)+1), SourcePath: a.SourcePath, TargetURL: a.TargetURL, StatusCode: a.StatusCode}
			s.redirects = append(s.redirects, r)
			text(r, false)
		default:
			text("unknown tool", true)
		}
	default:
		http.Error(w, "bad method", http.StatusBadRequest)
	}
}

// docsDir writes a small valid site: landing, two sections.
func docsDir(t *testing.T) (string, []Page, Manifest) {
	t.Helper()
	fm := func(title string) string {
		return "---\ntitle: " + title + "\ndescription: D.\n---\n\n## Heading\n\nSee [self](index.md) and [home](../index.md#top).\n"
	}
	root := site(t, map[string]string{
		"manifest.json": `{"root":"docs","sections":[{"slug":"a","title":"A","description":"d","pages":["one","two"]},{"slug":"b","title":"B","description":"d","pages":["three"]}]}`,
		"index.md":      strings.Replace(fm("Docs"), "[self](index.md) and [home](../index.md#top)", "[a](a/index.md#start)", 1),
		"a/index.md":    fm("A"), "a/one.md": fm("One"), "a/two.md": fm("Two"),
		"b/index.md": fm("B"), "b/three.md": strings.Replace(fm("Three"), "[self](index.md)", "[one](../a/one.md)", 1),
	})
	pages, m, err := LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if errs := Lint(pages, m); len(errs) > 0 {
		t.Fatal(errs)
	}
	return root, pages, m
}

func TestPublishApplyOrder(t *testing.T) {
	t.Parallel()
	for _, sse := range []bool{false, true} {
		s := newStack(t)
		s.sse = sse
		_, pages, m := docsDir(t)
		c, mc := s.clients()
		var out bytes.Buffer
		if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, &out); err != nil {
			t.Fatalf("sse=%v: %v\n%s", sse, err, out.String())
		}
		// the menu write is the very last event, after every entity publish
		last := s.events[len(s.events)-1]
		if last != "MCP menus_update" {
			t.Fatalf("sse=%v last event %q, events %v", sse, last, s.events)
		}
		lastStatus := -1
		for i, e := range s.events {
			if strings.HasSuffix(e, "/status") {
				lastStatus = i
			}
		}
		if lastStatus < 0 || lastStatus > len(s.events)-2 {
			t.Fatalf("menu not after the last publish: %v", s.events)
		}
		if len(s.items) != 5 || s.items[0].Key != "a" || s.items[1].ParentKey != "a" || s.items[3].Key != "b" {
			t.Fatalf("menu items: %+v", s.items)
		}
		for _, e := range s.ents {
			if e.Status != "published" {
				t.Errorf("%s not published", e.Slug)
			}
		}
		// landing: a root doc entity with slug docs, links rewritten, hash recorded
		var land *Entity
		for _, e := range s.ents {
			if e.Slug == "docs" {
				land = e
			}
		}
		if land == nil || land.ParentID != "" || land.SchemaID != fakeSchema {
			t.Fatalf("landing entity: %+v", land)
		}
		var attrs map[string]any
		_ = json.Unmarshal(land.Attributes, &attrs)
		if attrs["source_hash"] == nil {
			t.Fatalf("landing attributes: %v", attrs)
		}
		if !strings.Contains(attrs["content"].(string), `href="/docs/a#start"`) {
			t.Fatalf("landing links not rewritten: %v", attrs["content"])
		}
	}
}

func TestPublishLinksRewritten(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, pages, m := docsDir(t)
	c, mc := s.clients()
	if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.ents {
		if e.Slug == "three" {
			if !strings.Contains(string(e.Attributes), `href=\"/docs/a/one\"`) || !strings.Contains(string(e.Attributes), `href=\"/docs#top\"`) {
				t.Fatalf("three: %s", e.Attributes)
			}
		}
	}
}

func TestPublishRerunWritesNothing(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, pages, m := docsDir(t)
	c, mc := s.clients()
	if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, io.Discard); err != nil {
		t.Fatal(err)
	}
	s.events = nil
	if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.events {
		if strings.HasPrefix(e, "REST") {
			t.Errorf("entity write on re-run: %s", e)
		}
	}
}

func TestPublishDryRunMakesNoWrites(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, pages, m := docsDir(t)
	c, mc := s.clients()
	var out bytes.Buffer
	if err := publish(context.Background(), c, mc, fakeSchema, false, pages, m, &out); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.events {
		if e != "MCP menus_get" && e != "MCP schemas_get" {
			t.Errorf("dry run event %q", e)
		}
	}
	if len(s.writes()) != 0 || len(s.items) != 0 {
		t.Fatalf("writes %v items %v", s.writes(), s.items)
	}
	for _, want := range []string{"plan: 6 create", "menu: 5 items", "dry run: nothing written"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestPublishPrerequisites(t *testing.T) {
	t.Parallel()
	_, pages, m := docsDir(t)
	var pre *PrereqError

	s := newStack(t)
	s.menuID = ""
	c, mc := s.clients()
	if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, io.Discard); !errors.As(err, &pre) || !strings.Contains(pre.Msg, "menu") {
		t.Fatalf("no menu: %v", err)
	}
	if len(s.writes()) != 0 {
		t.Fatalf("wrote before the preflight failed: %v", s.writes())
	}

	s = newStack(t)
	c, mc = s.clients()
	if err := publish(context.Background(), c, mc, "", true, pages, m, io.Discard); !errors.As(err, &pre) || !strings.Contains(pre.Msg, "schema id") {
		t.Fatalf("no schema id: %v", err)
	}
}

func TestPublishBadLinkWritesNothing(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, pages, m := docsDir(t)
	pages[2].Body += "\n[gone](missing.md)\n"
	c, mc := s.clients()
	err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "missing.md") {
		t.Fatalf("err = %v", err)
	}
	if len(s.writes()) != 0 {
		t.Fatalf("writes: %v", s.writes())
	}
}

func TestMCPToolErrorAndAuth(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, mc := s.clients()
	var out any
	err := mc.Call(context.Background(), "nope", map[string]string{}, &out)
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v", err)
	}
	bad := NewMCP(Config{URL: s.srv.URL, APIKey: "wrong"})
	err = bad.Call(context.Background(), "menus_get", map[string]string{}, &out)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("err = %v", err)
	}
}

// menuTree nests items like the menus_get answer: roots in order, children
// under the item whose key their parent_key names.
func menuTree(items []MenuItem) []map[string]any {
	nodes := map[string]map[string]any{}
	var roots []map[string]any
	for _, it := range items {
		n := map[string]any{"label": it.Label, "link_type": it.LinkType, "open_in_new_tab": false, "children": []map[string]any{}}
		if it.EntityID != "" {
			n["entity_id"] = it.EntityID
		}
		nodes[it.Key] = n
		if p, ok := nodes[it.ParentKey]; ok && it.ParentKey != "" {
			p["children"] = append(p["children"].([]map[string]any), n)
		} else {
			roots = append(roots, n)
		}
	}
	return roots
}

func TestApplyTwiceWritesNothingSecondTime(t *testing.T) {
	t.Parallel()
	_, pages, m := docsDir(t)
	s := newStack(t)
	run := func() string {
		c, mc := s.clients()
		var out bytes.Buffer
		if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	run()
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
	out := run()
	for _, e := range s.events {
		if strings.HasPrefix(e, "REST") || e == "MCP menus_update" {
			t.Errorf("second apply wrote: %v", s.events)
		}
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("output lacks the unchanged note:\n%s", out)
	}
}

func TestMenuMatches(t *testing.T) {
	t.Parallel()
	want := []MenuItem{
		{Key: "a", Label: "A", LinkType: "entity", EntityID: "ida"},
		{Key: "a/one", ParentKey: "a", Label: "One", LinkType: "entity", EntityID: "id1"},
		{Key: "b", Label: "B", LinkType: "entity", EntityID: "idb"},
	}
	tree := func() []*MenuNode {
		return []*MenuNode{
			{Label: "A", LinkType: "entity", EntityID: "ida", Children: []*MenuNode{{Label: "One", LinkType: "entity", EntityID: "id1"}}},
			{Label: "B", LinkType: "entity", EntityID: "idb"},
		}
	}
	if !MenuMatches(tree(), want) {
		t.Fatal("identical menu must match")
	}
	for name, mut := range map[string]func([]*MenuNode) []*MenuNode{
		"label":   func(n []*MenuNode) []*MenuNode { n[1].Label = "x"; return n },
		"entity":  func(n []*MenuNode) []*MenuNode { n[0].Children[0].EntityID = "x"; return n },
		"nesting": func(n []*MenuNode) []*MenuNode { return []*MenuNode{n[0], n[0].Children[0], n[1]} },
		"extra":   func(n []*MenuNode) []*MenuNode { return append(n, &MenuNode{Label: "C", LinkType: "entity"}) },
		"url":     func(n []*MenuNode) []*MenuNode { n[1].URL = "/x"; return n },
		"empty":   func([]*MenuNode) []*MenuNode { return nil },
		"new tab": func(n []*MenuNode) []*MenuNode { n[1].OpenInNewTab = true; return n },
		"reorder": func(n []*MenuNode) []*MenuNode { return []*MenuNode{n[1], n[0]} },
		"unnested": func(n []*MenuNode) []*MenuNode {
			n[0].Children = nil
			return append(n, &MenuNode{Label: "One", LinkType: "entity", EntityID: "id1"})
		},
	} {
		if MenuMatches(mut(tree()), want) {
			t.Errorf("%s: a different menu matched", name)
		}
	}
}

func TestPublishWarnsAboutOrphansWithoutDeleting(t *testing.T) {
	t.Parallel()
	_, pages, m := docsDir(t)
	s := newStack(t)
	s.ents = append(s.ents, &Entity{ID: "old", SchemaID: fakeSchema, Name: "Old", Slug: "removed", Status: "published", Attributes: json.RawMessage(`{}`)})
	for _, apply := range []bool{false, true} {
		c, mc := s.clients()
		var out bytes.Buffer
		if err := publish(context.Background(), c, mc, fakeSchema, apply, pages, m, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "warning: 1 published") || !strings.Contains(out.String(), "/removed") {
			t.Errorf("apply=%v: no orphan warning:\n%s", apply, out.String())
		}
	}
	for _, w := range s.writes() {
		if strings.Contains(w, "/old") {
			t.Errorf("orphan was touched: %s", w)
		}
	}
}

func TestPublishWarnsWithoutSectionPages(t *testing.T) {
	t.Parallel()
	_, pages, m := docsDir(t)
	for _, noSection := range []bool{false, true} {
		s := newStack(t)
		s.noSection = noSection
		c, mc := s.clients()
		var out bytes.Buffer
		if err := publish(context.Background(), c, mc, fakeSchema, false, pages, m, &out); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "section pages turned on"); got != noSection {
			t.Errorf("noSection=%v: warning printed = %v\n%s", noSection, got, out.String())
		}
	}
}

func TestFindSidebarKeepsToolMessage(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.menuID = ""
	_, mc := s.clients()
	_, err := FindSidebar(context.Background(), mc)
	var pre *PrereqError
	if !errors.As(err, &pre) || !strings.Contains(pre.Msg, "menu not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishGroupedMenuRerunWritesNothing(t *testing.T) {
	t.Parallel()
	files := groupedFiles()
	root := site(t, files)
	pages, m, err := LoadSite(root)
	if err != nil {
		t.Fatal(err)
	}
	if errs := Lint(pages, m); len(errs) > 0 {
		t.Fatal(errs)
	}
	s := newStack(t)
	run := func() {
		c, mc := s.clients()
		if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	run()
	// section, two group labels, three pages
	if len(s.items) != 6 || s.items[1].Label != "Guides" || s.items[1].LinkType != "custom" || s.items[1].EntityID != "" || s.items[2].ParentKey != "a#guides" {
		t.Fatalf("menu items: %+v", s.items)
	}
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
	run()
	for _, e := range s.events {
		if strings.HasPrefix(e, "REST") || e == "MCP menus_update" {
			t.Errorf("second apply wrote: %v", s.events)
		}
	}
}
