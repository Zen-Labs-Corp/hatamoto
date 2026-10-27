package main

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// gitRunner runs git with args and returns stdout. Tests inject canned output.
type gitRunner func(args ...string) (string, error)

// execGit runs the real git in dir ("" is the current directory).
func execGit(dir string) gitRunner {
	return func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
					return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
				}
			}
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return string(out), nil
	}
}

// PR is one merged pull request found in the merge commits of a tag range.
type PR struct {
	Num    int
	Branch string
	Title  string
}

var (
	mergeRe  = regexp.MustCompile(`^Merge pull request #(\d+) from [^/]+/(.+)$`)
	semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
)

// ListPRs returns the pull requests merged in prev..tag, oldest listing order
// as git prints them. Merge commits that are not "Merge pull request" are
// ignored. The PR title is the first line of the merge body. An empty prev
// lists the whole history of tag.
func ListPRs(g gitRunner, prev, tag string) ([]PR, error) {
	rng := tag
	if prev != "" {
		rng = prev + ".." + tag
	}
	out, err := g("log", "--merges", "--format=%s%x1f%b%x1e", rng)
	if err != nil {
		return nil, err
	}
	var prs []PR
	for _, rec := range strings.Split(out, "\x1e") {
		subject, body, _ := strings.Cut(strings.TrimSpace(rec), "\x1f")
		m := mergeRe.FindStringSubmatch(strings.TrimSpace(subject))
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		title, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
		prs = append(prs, PR{Num: num, Branch: m[2], Title: strings.TrimSpace(title)})
	}
	return prs, nil
}

// PrevTag returns the semver tag before tag in version order. Non-semver tags
// (legacy-hybrid) are ignored, so the first release has no previous tag.
func PrevTag(g gitRunner, tag string) (string, bool) {
	tags, err := SemverTags(g)
	if err != nil {
		return "", false
	}
	for i, t := range tags {
		if t == tag && i > 0 {
			return tags[i-1], true
		}
	}
	return "", false
}

// SemverTags lists the vX.Y.Z tags, oldest first.
func SemverTags(g gitRunner) ([]string, error) {
	out, err := g("tag", "--sort=v:refname")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, t := range strings.Fields(out) {
		if semverRe.MatchString(t) {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

// VersionSort maps vX.Y.Z to X*1e6+Y*1e3+Z so the site can order numerically.
func VersionSort(v string) (float64, error) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("version %q is not vX.Y.Z", v)
	}
	var n [3]float64
	for i := range n {
		x, _ := strconv.Atoi(m[i+1])
		n[i] = float64(x)
	}
	return n[0]*1e6 + n[1]*1e3 + n[2], nil
}
