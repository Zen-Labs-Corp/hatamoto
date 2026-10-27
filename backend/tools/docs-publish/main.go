// Command docs-publish turns the markdown under docs/site into doc entities
// on a running Hatamoto site. It talks to the site only through the REST API
// and the MCP endpoint with an API key, never to the database.
//
//	go run . --lint                  check the sources offline
//	go run . [--apply]               plan (default) or publish
//
// Credentials come from HATAMOTO_URL and HATAMOTO_API_KEY in the environment
// or an env file; the key is never accepted as a flag.
//
// Exit codes: 0 ok, 1 failure or lint errors, 2 usage or missing credentials,
// 3 a prerequisite the site owner must create is missing.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path"
	"strings"
)

const (
	exitFail    = 1
	exitUsage   = 2
	exitMissing = 3
)

// Options are the parsed command line flags.
type Options struct {
	Root     string
	Lint     bool
	Apply    bool
	EnvFile  string
	SchemaID string
}

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var o Options
	fs := flag.NewFlagSet("docs-publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.Root, "root", "docs/site", "directory holding manifest.json and the markdown sources")
	fs.BoolVar(&o.Lint, "lint", false, "check the sources offline and exit (no credentials needed)")
	fs.BoolVar(&o.Apply, "apply", false, "write to the site (default is a dry run that only reads)")
	fs.StringVar(&o.EnvFile, "env-file", "", "file with HATAMOTO_URL and HATAMOTO_API_KEY lines (default .docs-publish.env when present)")
	fs.StringVar(&o.SchemaID, "schema-id", "", "id of the doc schema (default HATAMOTO_DOC_SCHEMA_ID; required to publish)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: docs-publish [--root DIR] [--lint] [--apply] [--env-file FILE] [--schema-id ID]")
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "\nenvironment: HATAMOTO_URL, HATAMOTO_API_KEY, HATAMOTO_DOC_SCHEMA_ID (the key is never read from flags)")
		fmt.Fprintln(stderr, "key scopes: read for a dry run; write:entities and write:menus to publish; write:redirects only when manifest.json has moved entries")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return exitUsage
	}

	pages, manifest, err := LoadSite(o.Root)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitFail
	}
	StampHashes(pages, manifest)
	errs := Lint(pages, manifest)
	errs = append(errs, LintContent(pages, manifest)...)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(stderr, e)
		}
		fmt.Fprintf(stderr, "%d lint error(s)\n", len(errs))
		return exitFail
	}
	if o.Lint {
		fmt.Fprintf(stdout, "ok: %d pages, %d sections\n", len(pages), len(manifest.Sections))
		return 0
	}

	cfg, err := LoadConfig(getenv, o.EnvFile)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}
	if o.SchemaID != "" {
		cfg.SchemaID = o.SchemaID
	}
	ctx := context.Background()
	err = publish(ctx, NewClient(cfg), NewMCP(cfg), cfg.SchemaID, o.Apply, pages, manifest, stdout)
	var pre *PrereqError
	switch {
	case errors.As(err, &pre):
		fmt.Fprintln(stderr, "missing prerequisite:", pre.Msg)
		return exitMissing
	case err != nil:
		fmt.Fprintln(stderr, "error:", err)
		return exitFail
	}
	return 0
}

// publish plans and, with apply, executes the whole publish: preflight, doc
// entities, the landing page, then the sidebar menu (last, so it only links
// pages that exist). Without apply it only reads.
func publish(ctx context.Context, c *Client, mc *MCP, schemaID string, apply bool, pages []Page, m Manifest, out io.Writer) error {
	if schemaID == "" {
		return &PrereqError{Msg: "the doc schema id is unknown: set HATAMOTO_DOC_SCHEMA_ID or --schema-id (see docs/site/README.md)"}
	}
	defer mc.Close(ctx)

	menu, err := FindSidebar(ctx, mc)
	if err != nil {
		return err
	}
	WarnSectionPages(ctx, mc, out)
	docs, err := c.ListEntities(ctx, schemaID, "")
	if err != nil {
		return err
	}
	plan := BuildPlan(pages, m.Sections, ToExisting(docs))

	render, err := renderer(pages, m)
	if err != nil {
		return err
	}
	// Render everything up front so a bad page stops the run before any write.
	for _, a := range plan {
		if a.Kind == KindCreate || a.Kind == KindUpdate {
			if _, err := render(*a.Page); err != nil {
				return fmt.Errorf("%s: %w", a.Page.Path, err)
			}
		}
	}

	redirects := RedirectsFromMoved(m)
	redirected, orphans := SplitRedirected(Orphans(plan, docs, m.Root), redirects)
	PrintPlan(out, plan)
	PrintOrphans(out, orphans)
	PrintRedirected(out, redirected)
	if !apply {
		ids := map[string]string{}
		for _, a := range plan {
			ids[Key(*a.Page)] = "pending"
		}
		fmt.Fprintf(out, "menu: %d items would replace the %q menu\n", len(BuildMenu(m.Sections, pages, ids)), SidebarLocation)
		if _, _, _, err := ReconcileRedirects(ctx, mc, redirects, false, out); err != nil {
			return err
		}
		fmt.Fprintln(out, "dry run: nothing written (use --apply)")
		return nil
	}

	ids, err := Apply(ctx, c, schemaID, plan, render)
	if err != nil {
		return err
	}
	items := BuildMenu(m.Sections, pages, ids)
	if MenuMatches(menu.Items, items) {
		fmt.Fprintf(out, "applied: sidebar menu already up to date (%d items), not written\n", len(items))
	} else {
		if err := ReplaceSidebar(ctx, mc, menu.ID, items); err != nil {
			return fmt.Errorf("sidebar menu: %w", err)
		}
		fmt.Fprintf(out, "applied: sidebar menu replaced with %d items\n", len(items))
	}
	// Redirects come last: their targets are published pages by now.
	created, existing, conflicts, err := ReconcileRedirects(ctx, mc, redirects, true, out)
	if err != nil {
		return err
	}
	if len(redirects) > 0 {
		fmt.Fprintf(out, "applied: redirects %d created, %d existing, %d conflicting\n", created, existing, conflicts)
	}
	return nil
}

// renderer converts page bodies, rewriting relative .md links to site paths
// under the manifest root ("/docs/<section>/<slug>", "/docs/<section>" for an
// index, "/docs" for the landing page). A link to a file that is not in the
// manifest is an error.
func renderer(pages []Page, m Manifest) (Render, error) {
	return newRenderer(pages, m, nil)
}

// fileKey is the manifest-relative file name of a page, as links name it.
func fileKey(p Page) string {
	if p.Section == "" {
		return p.Slug + ".md"
	}
	return p.Section + "/" + p.Slug + ".md"
}

// newRenderer is renderer with a hook that sees every resolved relative .md
// link: the page it is on, the key of the target file and the fragment.
func newRenderer(pages []Page, m Manifest, onLink func(from Page, key, frag string)) (Render, error) {
	if m.Root == "" {
		return nil, errors.New("manifest.json: root is required")
	}
	target := map[string]string{} // "section/slug.md" -> site path
	titles := map[string]string{} // "section/slug.md" -> link text
	base := "/" + strings.Trim(m.Root, "/")
	for _, p := range pages {
		titles[fileKey(p)] = cmp.Or(p.NavTitle, p.Title)
		switch {
		case p.Section == "":
			target[IndexSlug+".md"] = base
		case p.Slug == IndexSlug:
			target[fileKey(p)] = base + "/" + p.Section
		default:
			target[fileKey(p)] = base + "/" + p.Section + "/" + p.Slug
		}
	}
	return func(p Page) (string, error) {
		linkFor := func(href string) (string, error) {
			file, frag, _ := strings.Cut(href, "#")
			key := path.Join(p.Section, file)
			dest, ok := target[key]
			if !ok {
				return "", fmt.Errorf("no page in the manifest for %q", file)
			}
			if onLink != nil {
				onLink(p, key, frag)
			}
			if frag != "" {
				dest += "#" + frag
			}
			return dest, nil
		}
		body, err := Convert(p.Body, linkFor)
		if err != nil {
			return "", err
		}
		next, err := nextSteps(p, linkFor, func(file string) string { return titles[path.Join(p.Section, file)] })
		return body + next, err
	}, nil
}

// nextSteps renders the Next steps block of a guide from its next front
// matter key: a heading and a list of links, in the order given. Targets
// resolve through linkFor, so an unknown target is an error. A page without
// next gets "". The block is plain h2 and ul markup; the class lets the docs
// layout style it and survives the entity sanitizer (class is allowed on every
// element).
func nextSteps(p Page, linkFor func(string) (string, error), title func(string) string) (string, error) {
	if len(p.Next) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString(`<h2>Next steps</h2><ul class="hm-next-steps">`)
	for _, file := range p.Next {
		href, err := linkFor(file)
		if err != nil {
			return "", fmt.Errorf("next: %w", err)
		}
		fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`, html.EscapeString(href), html.EscapeString(cmp.Or(title(file), file)))
	}
	b.WriteString("</ul>")
	return b.String(), nil
}
