package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeGit answers by joined args; unknown commands fail.
func fakeGit(answers map[string]string) gitRunner {
	return func(args ...string) (string, error) {
		if out, ok := answers[strings.Join(args, " ")]; ok {
			return out, nil
		}
		return "", errors.New("unexpected git " + strings.Join(args, " "))
	}
}

const logFormat = "log --merges --format=%s%x1f%b%x1e "

// v0.26.99..v0.27.0 as git prints it with the format above.
const logV0270 = "Merge pull request #141 from example-org/chore/vanity-module-path\x1fchore: vanity module paths hatamoto.dev/core + hatamoto.dev/sdk\n\x1e\n" +
	"Merge pull request #130 from example-org/docs/close-settings-ia-split\x1fdocs(plans): close settings-ia-split\n\x1e\n" +
	"Merge origin/main into feat/x\x1f\x1e\n" +
	"Merge pull request #160 from example-org/feat/theme-light-default\x1ffeat(theme): light by default, header toggle for dark mode\n\x1e\n" +
	"Merge pull request #158 from example-org/feat/daterange-picker-polish\x1fdaterange-picker-polish: presets apply at once, tidier picker\n\x1e\n" +
	"Merge pull request #159 from example-org/ops/edge-v0.26.99\x1frelease: v0.26.99 on edge (ramp now:100)\n\x1e\n"

func TestListPRs(t *testing.T) {
	g := fakeGit(map[string]string{logFormat + "v0.26.99..v0.27.0": logV0270})
	prs, err := ListPRs(g, "v0.26.99", "v0.27.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 5 {
		t.Fatalf("want 5 PRs (non-PR merge ignored), got %d: %+v", len(prs), prs)
	}
	want := PR{Num: 141, Branch: "chore/vanity-module-path", Title: "chore: vanity module paths hatamoto.dev/core + hatamoto.dev/sdk"}
	if prs[0] != want {
		t.Errorf("first PR = %+v, want %+v", prs[0], want)
	}
}

func TestListPRsEmptyPrevListsWholeTag(t *testing.T) {
	g := fakeGit(map[string]string{logFormat + "v0.25.0": ""})
	if _, err := ListPRs(g, "", "v0.25.0"); err != nil {
		t.Fatal(err)
	}
}

func TestPrevTag(t *testing.T) {
	g := fakeGit(map[string]string{"tag --sort=v:refname": "legacy-hybrid\nv0.25.0\nv0.26.0\nv0.26.99\nv0.27.0\nv0.28.0-rc1\n"})
	for _, c := range []struct {
		tag, prev string
		ok        bool
	}{
		{"v0.25.0", "", false}, // legacy-hybrid is not semver
		{"v0.26.0", "v0.25.0", true},
		{"v0.27.0", "v0.26.99", true},
		{"v9.9.9", "", false},
	} {
		prev, ok := PrevTag(g, c.tag)
		if prev != c.prev || ok != c.ok {
			t.Errorf("PrevTag(%s) = %q,%v want %q,%v", c.tag, prev, ok, c.prev, c.ok)
		}
	}
}

func TestVersionSort(t *testing.T) {
	got, err := VersionSort("v0.26.99")
	if err != nil || got != 26099 {
		t.Fatalf("got %v, %v", got, err)
	}
	if a, _ := VersionSort("v0.27.0"); a <= got {
		t.Error("v0.27.0 must sort after v0.26.99")
	}
	for _, bad := range []string{"0.27.0", "v1.2", "v1.2.3-rc1", ""} {
		if _, err := VersionSort(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestListPRsThenGroupV0270(t *testing.T) {
	g := fakeGit(map[string]string{logFormat + "v0.26.99..v0.27.0": logV0270})
	prs, _ := ListPRs(g, "v0.26.99", "v0.27.0")
	n := Group(prs)
	want := Notes{
		Features: []string{"light by default, header toggle for dark mode (#160)", "daterange-picker-polish: presets apply at once, tidier picker (#158)"},
		Fixes:    []string{},
		Docs:     []string{"close settings-ia-split (#130)"},
		Chores:   []string{"vanity module paths hatamoto.dev/core + hatamoto.dev/sdk (#141)"},
	}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got %+v\nwant %+v", n, want)
	}
}

func TestExecGitErrorCarriesStderr(t *testing.T) {
	_, err := execGit(t.TempDir())("log", "-1", "--format=%cI", "v9.9.9")
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("err = %v", err)
	}
}
