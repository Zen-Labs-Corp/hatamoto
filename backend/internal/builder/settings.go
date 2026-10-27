package builder

import (
	"regexp"
	"sort"
	"strings"
)

// ReservedAnchors are the ids the theme and the core own: an anchor equal to
// one would make a duplicate DOM id. The hm- prefix covers the builder's own
// ids (hm-search-q, hm-related-h, hm-builder-*, hm-canvas-bridge, hm-tab<N>[p]),
// the -strip-label suffix the section strips. A node id of the same doc is
// reserved too (form fields render it as their id); see CheckSettings.
// A test in render scans the default theme for literal ids against this.
var ReservedAnchors = []string{"main-content", "toc", "top", "main", "content"}

// ReservedAnchor reports whether a is an id the page itself uses.
func ReservedAnchor(a string) bool {
	for _, r := range ReservedAnchors {
		if a == r {
			return true
		}
	}
	return strings.HasPrefix(a, "hm-") || strings.HasSuffix(a, "-strip-label")
}

// anchorRe is the anchor grammar; it is also the "anchor" prop pattern.
var anchorRe = regexp.MustCompile(anchorPattern)

// ValidAnchor reports whether a is usable as a public anchor id (the render
// also checks it: canvas patches skip Validate).
func ValidAnchor(a string) bool { return anchorRe.MatchString(a) && !ReservedAnchor(a) }

// TakesElementProps reports whether nodes of kind carry anchor, aria and tip.
func TakesElementProps(kind string) bool { return !noElementProps[kind] }

// LightboxUnnamed returns the ids of the images that open in a lightbox (their
// own switch or their gallery's) but have nothing to name the link by: no
// accessible name, no alt text (a decorative image has none) and no caption.
// A screen reader reads such a link as an empty one. It is a warning for the
// author, not a save error.
func (d *Doc) LightboxUnnamed() []string {
	var ids []string
	var walk func(ns []*Node, parent *Node)
	walk = func(ns []*Node, parent *Node) {
		for _, n := range ns {
			if n == nil {
				continue
			}
			if n.T == "image" && lightboxOpens(n, parent) && !imageNamed(n) {
				ids = append(ids, n.ID)
			}
			walk(n.C, n)
		}
	}
	walk(d.Nodes, nil)
	return ids
}

func lightboxOpens(n, parent *Node) bool {
	if str(n.P, "href") != "" || n.B["href"] != "" {
		return false // a link, not a lightbox (CheckSettings refuses both)
	}
	return n.P["lightbox"] == true || (parent != nil && parent.T == "gallery" && parent.P["lightbox"] == true)
}

func imageNamed(n *Node) bool {
	has := func(key string) bool { return strings.TrimSpace(str(n.P, key)) != "" || n.B[key] != "" }
	return has("aria") || has("caption") || (n.P["decorative"] != true && has("alt"))
}

// AriaAllowed reports whether n may carry an accessible name (aria-label): a
// plain div container (no link, no landmark tag) has no role that takes one.
func AriaAllowed(n *Node) bool {
	if noAriaKinds[n.T] || noElementProps[n.T] {
		return false
	}
	if n.T == "container" {
		if tag := str(n.P, "tag"); tag != "" && tag != "div" {
			return true
		}
		return str(n.P, "href") != "" || n.B["href"] != ""
	}
	return true
}

// interactiveKinds render links, buttons or other interactive or unknown
// content, so a link (a container with href) must not wrap them: nested
// interactive content is invalid HTML and breaks keyboard navigation.
// Plugin widget kinds count as interactive too (their output is unknown).
// A test checks every core widget template against this list.
var interactiveKinds = map[string]bool{
	"button": true, "buttons": true, "menu": true, "breadcrumbs": true, "map": true, "video": true,
	"accordion": true, "accorditem": true, "tabs": true, "tabpanel": true, "form": true,
	"html": true, "richtext": true, "loop": true, "loopempty": true,
	"component": true, "ref": true, "slot": true,
}

// inlineLinkRe finds a link in the inline html of a heading or text.
var inlineLinkRe = regexp.MustCompile(`(?i)<a[\s>/]`)

// interactiveNode reports whether n itself renders (or may render) interactive content.
func interactiveNode(n *Node) bool {
	if interactiveKinds[n.T] || IsPackKind(n.T) {
		return true
	}
	linked := func(key string) bool { return str(n.P, key) != "" || n.B[key] != "" }
	switch n.T {
	case "image":
		return linked("href") || n.P["lightbox"] == true
	case "container":
		return linked("href")
	case "gallery":
		return n.P["lightbox"] == true
	case "heading", "text":
		return inlineLinkRe.MatchString(str(n.P, "html"))
	}
	return false
}

// InteractiveIn returns the first node below n (depth first, hidden ones
// included) that is or may be interactive, or nil. n itself is not examined:
// a linked container is the link.
func InteractiveIn(n *Node) *Node {
	for _, c := range n.C {
		if c == nil {
			continue
		}
		if interactiveNode(c) {
			return c
		}
		if x := InteractiveIn(c); x != nil {
			return x
		}
	}
	return nil
}

func str(p map[string]any, key string) string {
	s, _ := p[key].(string)
	return s
}

// CheckSettings enforces the element-setting rules that relate nodes to each
// other: unique, unreserved anchors; a linked container without interactive
// content; an image that is a link or a lightbox, not both. It runs on write
// paths only (not in Parse): a stored doc is never refused for these, the
// renderer degrades instead. Errors wrap ErrInvalid.
func (d *Doc) CheckSettings() error {
	ids := map[string]bool{}
	anchors := map[string][]string{}
	var order []string
	var problem error
	var walk func(ns []*Node, parent *Node)
	walk = func(ns []*Node, parent *Node) {
		for _, n := range ns {
			if n == nil {
				continue
			}
			ids[n.ID] = true
			if a := str(n.P, "anchor"); a != "" && !PackOwns(n.T, "anchor") {
				if len(anchors[a]) == 0 {
					order = append(order, a)
				}
				anchors[a] = append(anchors[a], n.ID)
			}
			if problem == nil {
				problem = checkLinkSettings(n, parent)
			}
			walk(n.C, n)
		}
	}
	walk(d.Nodes, nil)
	// ids is complete only after the walk: node-id equality is checked last.
	var dup, reserved []string
	for _, a := range order {
		switch {
		case len(anchors[a]) > 1:
			dup = append(dup, a)
		case ReservedAnchor(a) || ids[a]:
			reserved = append(reserved, a)
		}
	}
	sort.Strings(dup)
	sort.Strings(reserved)
	if len(dup) > 0 {
		return invalid("anchor %s is used more than once; an anchor must be unique on the page", quoteList(dup))
	}
	if len(reserved) > 0 {
		return invalid("anchor %s is reserved by the page or matches an element id; choose another", quoteList(reserved))
	}
	return problem
}

func quoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = `"` + s + `"`
	}
	return strings.Join(q, ", ")
}

func checkLinkSettings(n, parent *Node) error {
	switch n.T {
	case "container":
		if str(n.P, "href") == "" && n.B["href"] == "" {
			return nil
		}
		if tag := str(n.P, "tag"); tag != "" && tag != "div" {
			return invalid("%s: a container with a link must use the div tag", n.ID)
		}
		if x := InteractiveIn(n); x != nil {
			return invalid("%s: a container with a link cannot hold interactive content (%s %s)", n.ID, x.T, x.ID)
		}
	case "image":
		hasLink := str(n.P, "href") != "" || n.B["href"] != ""
		lightbox := n.P["lightbox"] == true || (parent != nil && parent.T == "gallery" && parent.P["lightbox"] == true)
		if hasLink && lightbox {
			return invalid("%s: choose a link or a lightbox for an image, not both", n.ID)
		}
	}
	return nil
}
