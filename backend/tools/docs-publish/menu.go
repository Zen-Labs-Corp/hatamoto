package main

import (
	"context"
	"fmt"
	"io"
)

// SidebarLocation is the menu location the docs layout reads.
const SidebarLocation = "sidebar"

// MenuItem is one item of the menus_update tool input (menuItemIn in
// backend/internal/mcpserver/tools_content.go).
type MenuItem struct {
	Key       string `json:"key"`
	ParentKey string `json:"parent_key,omitempty"`
	Label     string `json:"label"`
	LinkType  string `json:"link_type"`
	EntityID  string `json:"entity_id,omitempty"`
}

// groupLabels are the label-only menu items that group a section's pages.
var groupLabels = map[string]string{GroupGuides: "Guides", GroupReference: "Reference"}

// BuildMenu returns the whole sidebar: one top level item per section (its
// index page) followed by that section's pages, parents before children, in
// manifest order. Guides and reference pages sit under a label-only group
// item (key "<section>#guides" or "<section>#reference", no link target); a
// group without pages is left out. Legacy ungrouped pages sit directly under
// the section. ids maps Key(page) to the entity id; pages without an id are
// left out.
func BuildMenu(sections []SectionSpec, pages []Page, ids map[string]string) []MenuItem {
	byKey := map[string]Page{}
	for _, p := range pages {
		if p.Section != "" {
			byKey[Key(p)] = p
		}
	}
	label := func(p Page, fallback string) string {
		if p.NavTitle != "" {
			return p.NavTitle
		}
		if p.Title != "" {
			return p.Title
		}
		return fallback
	}
	var items []MenuItem
	for _, s := range sections {
		root, ok := ids[s.Slug]
		if !ok {
			continue
		}
		items = append(items, MenuItem{Key: s.Slug, Label: label(byKey[s.Slug], s.Title), LinkType: "entity", EntityID: root})
		group := ""
		for _, ref := range s.All() {
			k := s.Slug + "/" + ref.Slug
			id, ok := ids[k]
			if !ok {
				continue
			}
			parent := s.Slug
			if ref.Group != "" {
				parent = s.Slug + "#" + ref.Group
				if group != ref.Group {
					items = append(items, MenuItem{Key: parent, ParentKey: s.Slug, Label: groupLabels[ref.Group], LinkType: "custom"})
					group = ref.Group
				}
			}
			items = append(items, MenuItem{Key: k, ParentKey: parent, Label: label(byKey[k], ref.Title), LinkType: "entity", EntityID: id})
		}
	}
	return items
}

// Menu is the part of the menus_get result this tool needs.
type Menu struct {
	ID    string      `json:"id"`
	Slug  string      `json:"slug"`
	Items []*MenuNode `json:"items"`
}

// MenuNode is one stored item in the nested tree menus_get returns.
type MenuNode struct {
	Label        string      `json:"label"`
	LinkType     string      `json:"link_type"`
	EntityID     string      `json:"entity_id"`
	URL          string      `json:"url"`
	SchemaSlug   string      `json:"schema_slug"`
	OpenInNewTab bool        `json:"open_in_new_tab"`
	Children     []*MenuNode `json:"children"`
}

// menuRow is the comparable form of one item: what it shows, what it links
// to and the labels of its ancestors, joined ("" at the top level). Parents
// are named by label path because group items link to no entity.
type menuRow struct {
	Label, LinkType, EntityID, ParentPath string
}

// pathSep joins labels in a menuRow path; it cannot occur in a label.
const pathSep = "\x00"

func childPath(parent, label string) string { return parent + pathSep + label }

// MenuMatches reports whether the stored menu already holds exactly the items
// that would be written, in the same order and nesting, so the write (which
// replaces every row and purges the page cache) can be skipped. Items with a
// url, an archive slug or a new-tab flag never match: this tool writes none.
func MenuMatches(current []*MenuNode, want []MenuItem) bool {
	var have []menuRow
	extra := false
	var walk func(nodes []*MenuNode, parent string)
	walk = func(nodes []*MenuNode, parent string) {
		for _, n := range nodes {
			if n.URL != "" || n.SchemaSlug != "" || n.OpenInNewTab {
				extra = true
			}
			have = append(have, menuRow{n.Label, n.LinkType, n.EntityID, parent})
			walk(n.Children, childPath(parent, n.Label))
		}
	}
	walk(current, "")
	if extra || len(have) != len(want) {
		return false
	}
	pathOf := map[string]string{}
	for i, it := range want {
		parent := ""
		if it.ParentKey != "" {
			p, ok := pathOf[it.ParentKey]
			if !ok {
				return false
			}
			parent = p
		}
		if it.Key != "" {
			pathOf[it.Key] = childPath(parent, it.Label)
		}
		if have[i] != (menuRow{it.Label, it.LinkType, it.EntityID, parent}) {
			return false
		}
	}
	return true
}

// FindSidebar reads the sidebar menu. A tool error means the menu does not
// exist yet (the owner creates it), reported as a PrereqError that carries the
// tool's own message, so a scope or server error is not mistaken for a missing menu.
func FindSidebar(ctx context.Context, m *MCP) (Menu, error) {
	var menu Menu
	if err := m.Call(ctx, "menus_get", map[string]string{"location": SidebarLocation}, &menu); err != nil {
		if te, ok := err.(*ToolError); ok {
			return Menu{}, &PrereqError{Msg: "no menu has the location \"" + SidebarLocation + "\" (" + te.Msg + "); the site owner must create one (see docs/site/README.md)"}
		}
		return Menu{}, err
	}
	return menu, nil
}

// ReplaceSidebar sets the menu items to exactly items.
func ReplaceSidebar(ctx context.Context, m *MCP, menuID string, items []MenuItem) error {
	return m.Call(ctx, "menus_update", map[string]any{"id": menuID, "items": items}, nil)
}

// PrereqError marks a missing owner-side prerequisite (exit code 3).
type PrereqError struct{ Msg string }

func (e *PrereqError) Error() string { return e.Msg }

// docSchemaSlug is the slug of the doc type (docs/site/doc-schema.json).
const docSchemaSlug = "doc"

// WarnSectionPages prints a warning when the doc type does not have
// section_pages on: /docs would then show the doc archive listing instead of
// the landing page entity. It is best effort. A key that cannot read the
// schema, or an answer that cannot be parsed, prints nothing.
func WarnSectionPages(ctx context.Context, m *MCP, out io.Writer) {
	var sc struct {
		Settings struct {
			SectionPages bool `json:"section_pages"`
		} `json:"settings"`
	}
	if err := m.Call(ctx, "schemas_get", map[string]string{"slug": docSchemaSlug}, &sc); err != nil {
		return
	}
	if !sc.Settings.SectionPages {
		fmt.Fprintln(out, "warning: the doc type does not have section pages turned on, so /docs will show the doc archive listing instead of the landing page. Add \"section_pages\": true to the doc type settings (see docs/site/README.md)")
	}
}
