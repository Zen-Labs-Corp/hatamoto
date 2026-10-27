package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// JSONLDReport is the result of checking a page's JSON-LD.
type JSONLDReport struct {
	Valid  bool         `json:"valid"` // no errors (warnings allowed)
	Blocks int          `json:"blocks"`
	Nodes  []JSONLDNode `json:"nodes"`
	Errors []string     `json:"errors,omitempty"` // block-level problems
}

// JSONLDNode is one typed object found in the page.
type JSONLDNode struct {
	Type     string   `json:"type"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// rule lists what a type must (error) and should (warning) carry.
type rule struct{ required, recommended []string }

var typeRules = map[string]rule{
	"Article":             {[]string{"headline", "author", "datePublished"}, []string{"image", "dateModified", "publisher"}},
	"BlogPosting":         {[]string{"headline", "author", "datePublished"}, []string{"image", "dateModified", "publisher"}},
	"NewsArticle":         {[]string{"headline", "author", "datePublished"}, []string{"image", "dateModified", "publisher"}},
	"WebPage":             {[]string{"name"}, []string{"description"}},
	"AboutPage":           {[]string{"name"}, nil},
	"ContactPage":         {[]string{"name"}, nil},
	"WebSite":             {[]string{"name", "url"}, nil},
	"Organization":        {[]string{"name"}, []string{"url", "logo"}},
	"LocalBusiness":       {[]string{"name", "address"}, []string{"telephone", "openingHours"}},
	"Person":              {[]string{"name"}, nil},
	"Product":             {[]string{"name"}, []string{"image", "description", "offers"}},
	"Service":             {[]string{"name"}, []string{"description", "provider"}},
	"Event":               {[]string{"name", "startDate", "location"}, []string{"endDate", "description"}},
	"FAQPage":             {[]string{"mainEntity"}, nil},
	"Question":            {[]string{"name", "acceptedAnswer"}, nil},
	"Answer":              {[]string{"text"}, nil},
	"HowTo":               {[]string{"name", "step"}, nil},
	"BreadcrumbList":      {[]string{"itemListElement"}, nil},
	"ListItem":            {[]string{"position"}, nil},
	"ImageObject":         {[]string{"url"}, nil},
	"CollectionPage":      {[]string{"name"}, nil},
	"ProfilePage":         {[]string{"name"}, nil},
	"PostalAddress":       {nil, nil},
	"VideoObject":         {[]string{"name", "uploadDate", "thumbnailUrl"}, []string{"description"}},
	"SoftwareApplication": {[]string{"name"}, []string{"offers"}},
}

// ValidateJSONLD checks each block: it must parse, carry @context (top level)
// and a typed node, and each known type its required fields. Unknown types
// only get a note: schema.org has hundreds and the checker is a guard against
// broken output, not a full schema.org validator.
func ValidateJSONLD(blocks [][]byte) JSONLDReport {
	rep := JSONLDReport{Blocks: len(blocks), Nodes: []JSONLDNode{}}
	if len(blocks) == 0 {
		rep.Errors = append(rep.Errors, "no application/ld+json block found")
	}
	for i, b := range blocks {
		var doc any
		dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(b)))
		if err := dec.Decode(&doc); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("block %d: invalid JSON: %v", i, err))
			continue
		}
		top, isObj := doc.(map[string]any)
		if !isObj {
			if _, isArr := doc.([]any); !isArr {
				rep.Errors = append(rep.Errors, fmt.Sprintf("block %d: expected a JSON object", i))
				continue
			}
		}
		if isObj && top["@context"] == nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("block %d: missing @context", i))
		} else if isObj && !contextIsSchemaOrg(top["@context"]) {
			rep.Errors = append(rep.Errors, fmt.Sprintf("block %d: @context should be https://schema.org", i))
		}
		walkNodes(doc, true, func(n map[string]any) { rep.Nodes = append(rep.Nodes, checkNode(n)) })
	}
	rep.Valid = len(rep.Errors) == 0
	for _, n := range rep.Nodes {
		if len(n.Errors) > 0 {
			rep.Valid = false
		}
	}
	return rep
}

func contextIsSchemaOrg(c any) bool {
	switch v := c.(type) {
	case string:
		return strings.Contains(v, "schema.org")
	case []any:
		for _, x := range v {
			if contextIsSchemaOrg(x) {
				return true
			}
		}
	case map[string]any:
		return true // an inline term map; nothing to check
	}
	return false
}

// walkNodes calls fn for every object that has an @type, descending into
// @graph and into property values (so nested Question/Answer/ListItem count).
func walkNodes(v any, top bool, fn func(map[string]any)) {
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			walkNodes(x, top, fn)
		}
	case map[string]any:
		if _, typed := t["@type"]; typed {
			fn(t)
		} else if top && t["@graph"] == nil {
			fn(t) // untyped top-level object: reported as missing @type
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if k == "@context" {
				continue
			}
			walkNodes(t[k], false, fn)
		}
	}
}

func typesOf(n map[string]any) []string {
	switch t := n["@type"].(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func checkNode(n map[string]any) JSONLDNode {
	types := typesOf(n)
	node := JSONLDNode{Type: strings.Join(types, ",")}
	if len(types) == 0 {
		node.Type = "(none)"
		node.Errors = append(node.Errors, "missing @type")
		return node
	}
	known := false
	for _, t := range types {
		r, ok := typeRules[t]
		if !ok {
			continue
		}
		known = true
		for _, f := range r.required {
			if empty(n[f]) {
				node.Errors = append(node.Errors, fmt.Sprintf("%s: missing required field %q", t, f))
			}
		}
		for _, f := range r.recommended {
			if empty(n[f]) {
				node.Warnings = append(node.Warnings, fmt.Sprintf("%s: recommended field %q is missing", t, f))
			}
		}
	}
	if !known {
		node.Warnings = append(node.Warnings, "no rules for this type: only @type was checked")
	}
	return node
}

func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}
