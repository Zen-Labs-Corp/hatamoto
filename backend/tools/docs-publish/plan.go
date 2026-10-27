package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Existing is a doc entity already on the site.
type Existing struct{ ID, ParentID, Slug, Status, SourceHash string }

// Action kinds.
const (
	KindCreate  = "create"  // create the entity, then publish it
	KindUpdate  = "update"  // replace name and attributes (and publish when it is a draft)
	KindPublish = "publish" // content is current, only the status changes
	KindSkip    = "skip"    // nothing to do
)

// Action is one planned step for one page.
type Action struct {
	Kind string
	Page *Page
	// Parent is the section slug of the parent entity, "" for a section root.
	Parent string
	// ID is the existing entity id, "" for a create.
	ID string
	// Publish says the entity must be published after the write.
	Publish bool
}

// landingSlug is the slug of the root doc entity that serves the docs landing
// page (index.md at the root). Lint keeps a section from taking it.
const landingSlug = "docs"

// Key identifies the entity of a page: landingSlug for the landing page, the
// section slug for a section index, "section/slug" for a page.
func Key(p Page) string {
	if p.Section == "" {
		return landingSlug
	}
	if p.Slug == IndexSlug {
		return p.Section
	}
	return p.Section + "/" + p.Slug
}

// BuildPlan compares the desired pages with the entities on the site. Sections
// are roots (slug = section slug, parent none) and come first in manifest
// order, after the landing page (a root with slug "docs"); their pages are
// children of the section entity and follow. It is pure.
func BuildPlan(desired []Page, sections []SectionSpec, existing []Existing) []Action {
	type key struct{ parent, slug string }
	index := map[key]Existing{}
	for _, e := range existing {
		index[key{e.ParentID, e.Slug}] = e
	}
	byKey := map[string]*Page{}
	for i := range desired {
		byKey[Key(desired[i])] = &desired[i]
	}

	decide := func(p *Page, parent string, e Existing, found bool) Action {
		a := Action{Page: p, Parent: parent, ID: e.ID}
		switch {
		case !found:
			a.Kind, a.Publish = KindCreate, true
		case e.SourceHash != p.Hash:
			a.Kind, a.Publish = KindUpdate, e.Status != "published"
		case e.Status != "published":
			a.Kind, a.Publish = KindPublish, true
		default:
			a.Kind = KindSkip
		}
		return a
	}

	var out []Action
	if land, ok := byKey[landingSlug]; ok {
		e, found := index[key{"", landingSlug}]
		out = append(out, decide(land, "", e, found))
	}
	for _, s := range sections {
		root, ok := byKey[s.Slug]
		if !ok {
			continue
		}
		e, found := index[key{"", s.Slug}]
		out = append(out, decide(root, "", e, found))
	}
	for _, s := range sections {
		root, found := index[key{"", s.Slug}]
		for _, ref := range s.All() {
			p, ok := byKey[s.Slug+"/"+ref.Slug]
			if !ok {
				continue
			}
			var e Existing
			var f bool
			if found {
				e, f = index[key{root.ID, ref.Slug}]
			}
			out = append(out, decide(p, s.Slug, e, f))
		}
	}
	return out
}

// ToExisting converts listed entities into plan input.
func ToExisting(es []Entity) []Existing {
	out := make([]Existing, len(es))
	for i, e := range es {
		out[i] = Existing{ID: e.ID, ParentID: e.ParentID, Slug: e.Slug, Status: e.Status, SourceHash: e.SourceHash()}
	}
	return out
}

// Counts summarises a plan for the dry run output.
func Counts(plan []Action) map[string]int {
	m := map[string]int{}
	for _, a := range plan {
		m[a.Kind]++
	}
	return m
}

// PrintPlan writes one line per action and a summary.
func PrintPlan(w io.Writer, plan []Action) {
	for _, a := range plan {
		fmt.Fprintf(w, "%-8s %s\n", a.Kind, Key(*a.Page))
	}
	c := Counts(plan)
	fmt.Fprintf(w, "plan: %d create, %d update, %d publish, %d skip\n", c[KindCreate], c[KindUpdate], c[KindPublish], c[KindSkip])
}

// Render turns a page into the HTML stored in attributes.content.
type Render func(Page) (string, error)

// Apply executes plan against the site and returns the entity id of every
// page by Key. Parents always exist before their children because the plan
// lists sections first. Skipped pages cost no write.
func Apply(ctx context.Context, c *Client, schemaID string, plan []Action, render Render) (map[string]string, error) {
	ids := map[string]string{}
	for _, a := range plan {
		p := *a.Page
		id := a.ID
		if a.Kind != KindSkip && a.Kind != KindPublish {
			html, err := render(p)
			if err != nil {
				return ids, fmt.Errorf("%s: %w", p.Path, err)
			}
			attrs := DocAttributes{Content: html, Description: p.Description, NavTitle: p.NavTitle, SourceHash: p.Hash}
			if a.Kind == KindCreate {
				parentID := ids[a.Parent]
				if a.Parent != "" && parentID == "" {
					return ids, fmt.Errorf("%s: parent section %q has no entity", p.Path, a.Parent)
				}
				e, err := c.CreateEntity(ctx, schemaID, parentID, entitySlug(p), p.Title, attrs)
				if err != nil {
					return ids, fmt.Errorf("create %s: %w", Key(p), err)
				}
				id = e.ID
			} else if _, err := c.UpdateEntity(ctx, id, p.Title, attrs); err != nil {
				return ids, fmt.Errorf("update %s: %w", Key(p), err)
			}
		}
		if a.Publish {
			if err := c.SetStatus(ctx, id, "published"); err != nil {
				return ids, fmt.Errorf("publish %s: %w", Key(p), err)
			}
		}
		ids[Key(p)] = id
	}
	return ids, nil
}

// entitySlug is the entity slug of a page: "docs" for the landing page, the
// section for a section index.
func entitySlug(p Page) string {
	if p.Section == "" {
		return landingSlug
	}
	if p.Slug == IndexSlug {
		return strings.TrimSpace(p.Section)
	}
	return p.Slug
}

// Orphans lists the site paths of published doc entities that no action of
// plan covers, that is pages removed from the manifest, renamed or moved.
// They stay live; the tool only reports them. root is the manifest root.
func Orphans(plan []Action, docs []Entity, root string) []string {
	planned := map[string]bool{}
	for _, a := range plan {
		if a.ID != "" {
			planned[a.ID] = true
		}
	}
	byID := map[string]Entity{}
	for _, e := range docs {
		byID[e.ID] = e
	}
	var out []string
	for _, e := range docs {
		if planned[e.ID] || e.Status != "published" {
			continue
		}
		p := "/" + e.Slug
		for cur, hops := e, 0; cur.ParentID != "" && hops < 16; hops++ {
			parent, ok := byID[cur.ParentID]
			if !ok {
				break
			}
			p = "/" + parent.Slug + p
			cur = parent
		}
		out = append(out, "/"+strings.Trim(root, "/")+p)
	}
	sort.Strings(out)
	return out
}

// PrintOrphans warns about published doc entities the manifest no longer has.
func PrintOrphans(w io.Writer, urls []string) {
	if len(urls) == 0 {
		return
	}
	fmt.Fprintf(w, "warning: %d published doc page(s) are not in the manifest and stay live (unpublish them in the admin if they should go):\n", len(urls))
	for _, u := range urls {
		fmt.Fprintf(w, "  %s\n", u)
	}
}
