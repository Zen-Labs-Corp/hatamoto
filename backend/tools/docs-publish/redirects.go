package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Redirect is one permanent redirect from an old site path to a new one.
type Redirect struct{ Source, Target string }

// redirectScope is the API key scope the redirect step needs to write.
const redirectScope = "write:redirects"

// redirectsPerPage is how many rules one redirects_list call asks for.
const redirectsPerPage = 50

// sitePath turns a manifest "section/slug" into a site path under the root.
// A section index ("section/index") is the section path itself.
func sitePath(root, key string) string {
	key = strings.TrimSuffix(key, "/"+IndexSlug)
	return "/" + strings.Trim(root, "/") + "/" + key
}

// RedirectsFromMoved turns the moved map into redirects ("/docs/old" to
// "/docs/new"), sorted by source.
func RedirectsFromMoved(m Manifest) []Redirect {
	out := make([]Redirect, 0, len(m.Moved))
	for from, to := range m.Moved {
		out = append(out, Redirect{Source: sitePath(m.Root, from), Target: sitePath(m.Root, to)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// ReconcileRedirects makes sure every wanted redirect exists. It only ever
// creates: a source that already redirects to the wanted target is left alone
// ("redirect exists"), one that redirects somewhere else is reported
// ("redirect conflict") and never changed, and nothing is deleted. Without
// apply it only reads (redirects_list) and reports what it would create. The
// counts are of creates (or would-be creates), existing rules and conflicts.
func ReconcileRedirects(ctx context.Context, mc *MCP, want []Redirect, apply bool, out io.Writer) (created, existing, conflicts int, err error) {
	for _, r := range want {
		found, same, other, err := findRedirect(ctx, mc, r)
		if err != nil {
			return created, existing, conflicts, redirectErr("list", err)
		}
		switch {
		case found && same:
			existing++
			fmt.Fprintf(out, "redirect exists %s -> %s\n", r.Source, r.Target)
		case found:
			conflicts++
			fmt.Fprintf(out, "redirect conflict %s already points to %s, not changed (wanted %s)\n", r.Source, other, r.Target)
		case !apply:
			created++
			fmt.Fprintf(out, "redirect create %s -> %s\n", r.Source, r.Target)
		default:
			err := mc.Call(ctx, "redirects_create", map[string]any{"source_path": r.Source, "target_url": r.Target, "status_code": 301}, nil)
			if err != nil {
				return created, existing, conflicts, redirectErr("create", err)
			}
			created++
			fmt.Fprintf(out, "redirect create %s -> %s\n", r.Source, r.Target)
		}
	}
	return created, existing, conflicts, nil
}

// findRedirect looks for a rule whose source is exactly r.Source. The list
// call is a substring search, so it pages through every match until the source
// is seen or the reported total is covered. same says the rule already points
// at r.Target, other is where it points.
func findRedirect(ctx context.Context, mc *MCP, r Redirect) (found, same bool, other string, err error) {
	seen := 0
	for page := 1; ; page++ {
		var list struct {
			Items []struct {
				SourcePath string `json:"source_path"`
				TargetURL  string `json:"target_url"`
			} `json:"items"`
			Total int `json:"total"`
		}
		args := map[string]any{"search": r.Source, "per_page": redirectsPerPage, "page": page}
		if err := mc.Call(ctx, "redirects_list", args, &list); err != nil {
			return false, false, "", err
		}
		for _, it := range list.Items {
			if it.SourcePath == r.Source {
				return true, it.TargetURL == r.Target, it.TargetURL, nil
			}
		}
		seen += len(list.Items)
		if len(list.Items) == 0 || seen >= list.Total {
			return false, false, "", nil
		}
	}
}

// redirectErr names the scope a refused redirect call needs.
func redirectErr(what string, err error) error {
	var te *ToolError
	if errors.As(err, &te) && strings.Contains(te.Msg, "forbidden") {
		scope := redirectScope
		if what == "list" {
			scope = "read"
		}
		return fmt.Errorf("redirects %s: the API key needs the %s scope (%s)", what, scope, te.Msg)
	}
	return fmt.Errorf("redirects %s: %w", what, err)
}

// SplitRedirected separates orphan site paths into those a redirect already
// covers and the rest.
func SplitRedirected(orphans []string, want []Redirect) (redirected, rest []string) {
	src := map[string]bool{}
	for _, r := range want {
		src[r.Source] = true
	}
	for _, o := range orphans {
		if src[o] {
			redirected = append(redirected, o)
		} else {
			rest = append(rest, o)
		}
	}
	return redirected, rest
}

// PrintRedirected lists moved pages whose old entity is still published: the
// redirect wins for visitors, the entity stays until the owner unpublishes it.
func PrintRedirected(w io.Writer, urls []string) {
	if len(urls) == 0 {
		return
	}
	fmt.Fprintf(w, "note: %d moved page(s) are redirected, still live as published doc entities (unpublish them in the admin when ready):\n", len(urls))
	for _, u := range urls {
		fmt.Fprintf(w, "  %s\n", u)
	}
}
