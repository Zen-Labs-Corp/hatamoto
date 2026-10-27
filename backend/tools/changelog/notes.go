package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Notes is the generated body of a changelog entry. The JSON names are the
// entry attribute names.
type Notes struct {
	Summary  string   `json:"summary"`
	Features []string `json:"features"`
	Fixes    []string `json:"fixes"`
	Docs     []string `json:"docs"`
	Chores   []string `json:"chores"`
}

var (
	// Every ops/* branch is release plumbing (channel bumps, schedules, drills), not a change.
	skipRe  = regexp.MustCompile(`^ops/`)
	titleRe = regexp.MustCompile(`^(feat|fix|docs|chore|refactor|perf|test|build|ci|style)(\(.+?\))?!?:\s*`)
)

// Group sorts PRs into Notes. A conventional prefix in the title wins over the
// branch prefix; a PR with neither is a feature. Channel-bump PRs are dropped.
func Group(prs []PR) Notes {
	n := Notes{Features: []string{}, Fixes: []string{}, Docs: []string{}, Chores: []string{}}
	for _, p := range prs {
		if skipRe.MatchString(p.Branch) {
			continue
		}
		kind, text := "", p.Title
		if m := titleRe.FindStringSubmatch(p.Title); m != nil {
			kind, text = m[1], p.Title[len(m[0]):]
		} else if pre, _, ok := strings.Cut(p.Branch, "/"); ok {
			kind = pre
		}
		item := fmt.Sprintf("%s (#%d)", strings.TrimSpace(text), p.Num)
		switch kind {
		case "fix":
			n.Fixes = append(n.Fixes, item)
		case "docs":
			n.Docs = append(n.Docs, item)
		case "chore", "refactor", "perf", "test", "build", "ci", "style":
			n.Chores = append(n.Chores, item)
		default:
			n.Features = append(n.Features, item)
		}
	}
	return n
}

// Hash is the sha256 (hex) of the canonical JSON of the notes, with nil lists
// as []. The generator stores it next to the entry to tell whether the notes
// were edited by hand later.
func (n Notes) Hash() string {
	for _, l := range []*[]string{&n.Features, &n.Fixes, &n.Docs, &n.Chores} {
		if *l == nil {
			*l = []string{}
		}
	}
	b, _ := json.Marshal(n)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
