package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestGroup(t *testing.T) {
	cases := []struct {
		name string
		pr   PR
		list string // Features, Fixes, Docs, Chores or "" for dropped
	}{
		{"title prefix beats branch", PR{1, "feat/builder-convert", "fix(builder): keep dates"}, "Fixes"},
		{"branch prefix when title has none", PR{2, "fix/sitemap", "sitemap redirects"}, "Fixes"},
		{"docs branch", PR{3, "docs/close", "close plan"}, "Docs"},
		{"refactor title is a chore", PR{4, "feat/x", "refactor: tidy"}, "Chores"},
		{"breaking marker", PR{5, "feat/x", "fix!: drop thing"}, "Fixes"},
		{"no prefix at all is a feature", PR{6, "weird", "something"}, "Features"},
		{"unknown branch prefix is a feature", PR{7, "wip/x", "something"}, "Features"},
		{"edge bump dropped", PR{8, "ops/edge-v0.26.99", "release: v0.26.99 on edge"}, ""},
		{"stable bump dropped", PR{9, "ops/stable-v0.26.0", "ops: offer v0.26.0"}, ""},
		{"drill dropped", PR{10, "ops/edge-halt-drill", "release: halt drill"}, ""},
		{"schedule dropped", PR{11, "ops/schedule-v0.27.1", "release: v0.27.1 on canary now, edge +2d, stable +7d"}, ""},
		{"canary bump dropped", PR{12, "ops/canary-v0.28.0", "release: v0.28.0 on canary"}, ""},
		{"ops-like feature kept", PR{13, "feat/ops-dashboard", "ops dashboard"}, "Features"},
	}
	for _, c := range cases {
		n := Group([]PR{c.pr})
		lists := map[string][]string{"Features": n.Features, "Fixes": n.Fixes, "Docs": n.Docs, "Chores": n.Chores}
		for name, l := range lists {
			if (name == c.list) != (len(l) == 1) {
				t.Errorf("%s: list %s has %v", c.name, name, l)
			}
		}
	}
}

func TestGroupItemText(t *testing.T) {
	n := Group([]PR{{161, "feat/builder-b6-reuse", "feat(builder): B6 reuse: templates"}})
	if !reflect.DeepEqual(n.Features, []string{"B6 reuse: templates (#161)"}) {
		t.Errorf("got %v", n.Features)
	}
}

func TestNoOpsItemsAnywhere(t *testing.T) { // ISC-17
	n := Group([]PR{
		{1, "ops/edge-v1.0.0", "release: v1.0.0 on edge"},
		{2, "ops/stable-v1.0.0", "ops: offer v1.0.0 on stable"},
		{3, "feat/a", "a"},
	})
	all := strings.Join(append(append(n.Features, n.Fixes...), append(n.Docs, n.Chores...)...), "\n")
	if strings.Contains(all, "ops") || strings.Contains(all, "release:") || !strings.Contains(all, "a (#3)") {
		t.Errorf("unexpected items: %q", all)
	}
}

func TestHash(t *testing.T) {
	a := Group([]PR{{1, "feat/a", "a"}, {2, "fix/b", "b"}})
	b := Group([]PR{{1, "feat/a", "a"}, {2, "fix/b", "b"}})
	if a.Hash() != b.Hash() || len(a.Hash()) != 64 {
		t.Error("hash not stable")
	}
	c := Group([]PR{{1, "feat/a", "a changed"}, {2, "fix/b", "b"}})
	if a.Hash() == c.Hash() {
		t.Error("hash did not change with an item")
	}
	d := a
	d.Summary = "hello"
	if a.Hash() == d.Hash() {
		t.Error("hash ignores summary")
	}
	if (Notes{}).Hash() != (Notes{Features: []string{}, Fixes: []string{}, Docs: []string{}, Chores: []string{}}).Hash() {
		t.Error("nil and empty lists must hash alike")
	}
}
