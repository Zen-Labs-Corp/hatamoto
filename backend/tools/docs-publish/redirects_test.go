package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func eventCount(s *stack, name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e == name {
			n++
		}
	}
	return n
}

func TestRedirectsFromMoved(t *testing.T) {
	t.Parallel()
	m := Manifest{Root: "/docs/", Moved: map[string]string{"b/old": "b/new", "a/old": "a/index", "a/gone": "a/new"}}
	got := RedirectsFromMoved(m)
	want := []Redirect{
		{"/docs/a/gone", "/docs/a/new"},
		{"/docs/a/old", "/docs/a"},
		{"/docs/b/old", "/docs/b/new"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if len(RedirectsFromMoved(Manifest{Root: "docs"})) != 0 {
		t.Fatal("no moved entries, no redirects")
	}
}

func TestReconcileDryRunOnlyReads(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.redirects = []redirectRow{{ID: "r1", SourcePath: "/docs/a/same", TargetURL: "/docs/a/x", StatusCode: 301}}
	_, mc := s.clients()
	want := []Redirect{{"/docs/a/new", "/docs/a/x"}, {"/docs/a/same", "/docs/a/x"}}
	var out bytes.Buffer
	created, existing, conflicts, err := ReconcileRedirects(context.Background(), mc, want, false, &out)
	if err != nil || created != 1 || existing != 1 || conflicts != 0 {
		t.Fatalf("%d %d %d %v", created, existing, conflicts, err)
	}
	for _, line := range []string{"redirect create /docs/a/new -> /docs/a/x", "redirect exists /docs/a/same -> /docs/a/x"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("output lacks %q:\n%s", line, out.String())
		}
	}
	if eventCount(s, "MCP redirects_create") != 0 || len(s.redirects) != 1 {
		t.Fatalf("a dry run wrote: %v %v", s.events, s.redirects)
	}
}

func TestReconcileIsIdempotentAndNeverChangesConflicts(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.redirects = []redirectRow{{ID: "r1", SourcePath: "/docs/a/taken", TargetURL: "/elsewhere", StatusCode: 302}}
	_, mc := s.clients()
	want := []Redirect{{"/docs/a/new", "/docs/a/x"}, {"/docs/a/taken", "/docs/a/y"}}
	var out bytes.Buffer
	created, _, conflicts, err := ReconcileRedirects(context.Background(), mc, want, true, &out)
	if err != nil || created != 1 || conflicts != 1 {
		t.Fatalf("%d %d %v", created, conflicts, err)
	}
	if !strings.Contains(out.String(), "redirect conflict /docs/a/taken already points to /elsewhere") {
		t.Errorf("no conflict line:\n%s", out.String())
	}
	if len(s.redirects) != 2 || s.redirects[0].TargetURL != "/elsewhere" || s.redirects[1].StatusCode != 301 {
		t.Fatalf("stored: %+v", s.redirects)
	}
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
	created, existing, conflicts, err := ReconcileRedirects(context.Background(), mc, want, true, io.Discard)
	if err != nil || created != 0 || existing != 1 || conflicts != 1 || eventCount(s, "MCP redirects_create") != 0 {
		t.Fatalf("second run: %d %d %d %v %v", created, existing, conflicts, err, s.events)
	}
}

// The fake refuses a duplicate source the way the server does; the tool
// treats that answer as "exists" (see ReconcileRedirects).
func TestFakeRefusesDuplicateRedirect(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, mc := s.clients()
	s.redirects = []redirectRow{{ID: "r1", SourcePath: "/docs/a/new", TargetURL: "/docs/a/x"}}
	err := mc.Call(context.Background(), "redirects_create", map[string]any{"source_path": "/docs/a/new", "target_url": "/docs/a/x"}, nil)
	var te *ToolError
	if !errors.As(err, &te) || !strings.Contains(te.Msg, "already exists") {
		t.Fatalf("err = %v", err)
	}
}

// The list call is a substring search with pages: the exact source can be
// past the first page, and a longer source that merely contains it is not a
// match.
func TestReconcileFindsSourceOnLaterPage(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	for i := 0; i < 120; i++ {
		s.redirects = append(s.redirects, redirectRow{ID: fmt.Sprintf("x%d", i), SourcePath: fmt.Sprintf("/docs/a/new/%03d", i), TargetURL: "/z", StatusCode: 301})
	}
	s.redirects = append(s.redirects, redirectRow{ID: "r", SourcePath: "/docs/a/new", TargetURL: "/docs/a/x", StatusCode: 301})
	_, mc := s.clients()
	var out bytes.Buffer
	created, existing, conflicts, err := ReconcileRedirects(context.Background(), mc, []Redirect{{"/docs/a/new", "/docs/a/x"}}, true, &out)
	if err != nil || created != 0 || existing != 1 || conflicts != 0 {
		t.Fatalf("%d %d %d %v\n%s", created, existing, conflicts, err, out.String())
	}
	if eventCount(s, "MCP redirects_create") != 0 {
		t.Fatal("created a duplicate")
	}
	// only longer sources exist: the exact one is missing and is created
	s.redirects = s.redirects[:120]
	created, existing, _, err = ReconcileRedirects(context.Background(), mc, []Redirect{{"/docs/a/new", "/docs/a/x"}}, true, io.Discard)
	if err != nil || created != 1 || existing != 0 {
		t.Fatalf("%d %d %v", created, existing, err)
	}
}

// A refused create is an error, never read as "exists" from its message.
func TestReconcileCreateErrorIsReported(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_, mc := s.clients()
	s.hideRedirects = true
	s.redirects = []redirectRow{{ID: "r1", SourcePath: "/docs/a/new", TargetURL: "/docs/a/x"}}
	_, _, _, err := ReconcileRedirects(context.Background(), mc, []Redirect{{"/docs/a/new", "/docs/a/x"}}, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "redirects create") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectErrNamesScope(t *testing.T) {
	t.Parallel()
	err := redirectErr("create", &ToolError{Tool: "redirects_create", Msg: "forbidden: the API key lacks the write:redirects scope"})
	if !strings.Contains(err.Error(), "write:redirects") {
		t.Fatal(err)
	}
	err = redirectErr("list", &ToolError{Tool: "redirects_list", Msg: "forbidden: the API key lacks the read scope"})
	if !strings.Contains(err.Error(), "needs the read scope") {
		t.Fatal(err)
	}
}

func TestSplitRedirected(t *testing.T) {
	t.Parallel()
	red, rest := SplitRedirected([]string{"/docs/a/old", "/docs/b/x"}, []Redirect{{Source: "/docs/a/old"}})
	if !reflect.DeepEqual(red, []string{"/docs/a/old"}) || !reflect.DeepEqual(rest, []string{"/docs/b/x"}) {
		t.Fatalf("%v %v", red, rest)
	}
}

func TestPublishRedirectsFromMoved(t *testing.T) {
	t.Parallel()
	_, pages, m := docsDir(t)
	m.Moved = map[string]string{"a/old": "a/one"}
	s := newStack(t)

	var out bytes.Buffer
	c, mc := s.clients()
	if err := publish(context.Background(), c, mc, fakeSchema, false, pages, m, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "redirect create /docs/a/old -> /docs/a/one") {
		t.Errorf("dry run lacks the redirect line:\n%s", out.String())
	}
	if eventCount(s, "MCP redirects_create") != 0 || len(s.redirects) != 0 {
		t.Fatalf("dry run created: %v", s.events)
	}

	run := func() string {
		var o bytes.Buffer
		c, mc := s.clients()
		if err := publish(context.Background(), c, mc, fakeSchema, true, pages, m, &o); err != nil {
			t.Fatalf("%v\n%s", err, o.String())
		}
		return o.String()
	}
	run()
	if len(s.redirects) != 1 || s.redirects[0].TargetURL != "/docs/a/one" || s.redirects[0].StatusCode != 301 {
		t.Fatalf("redirects: %+v", s.redirects)
	}
	// the old page is still a published entity under section a
	var secID string
	for _, e := range s.ents {
		if e.Slug == "a" {
			secID = e.ID
		}
	}
	s.ents = append(s.ents, &Entity{ID: "old", ParentID: secID, SchemaID: fakeSchema, Name: "Old", Slug: "old", Status: "published"})
	// the redirect is written after the menu, which is after the last publish
	last := ""
	for _, e := range s.events {
		if strings.HasPrefix(e, "MCP menus_update") || strings.HasPrefix(e, "MCP redirects_create") {
			last = e
		}
	}
	if last != "MCP redirects_create" {
		t.Errorf("redirect not after the menu: %v", s.events)
	}
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
	if second := run(); !strings.Contains(second, "redirected, still live") || strings.Contains(second, "stay live (unpublish") {
		t.Errorf("no note about the live old page:\n%s", second)
	}
	if eventCount(s, "MCP redirects_create") != 0 || len(s.redirects) != 1 {
		t.Fatalf("second apply created: %v %v", s.events, s.redirects)
	}
	for _, e := range s.events {
		if strings.HasPrefix(e, "REST") || strings.Contains(e, "delete") {
			t.Errorf("second apply wrote: %s", e)
		}
	}
	if s.ents[len(s.ents)-1].Status != "published" {
		t.Error("the old entity must stay untouched")
	}
}

func TestPublishWithoutMovedNeverCallsRedirects(t *testing.T) {
	t.Parallel()
	_, pages, m := docsDir(t)
	s := newStack(t)
	for _, apply := range []bool{false, true} {
		c, mc := s.clients()
		if err := publish(context.Background(), c, mc, fakeSchema, apply, pages, m, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range s.events {
		if strings.Contains(e, "redirects_") {
			t.Fatalf("redirect call without moved entries: %s", e)
		}
	}
}
