package builder

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Bind classes: what a source yields and what a prop accepts (x-hm-bind).
const (
	BindText  = "text"
	BindURL   = "url"
	BindImage = "image"
)

// BindSource is one entry of the closed source vocabulary. A Source ending in
// "<path>" or "<key>" is a pattern; the rest are exact.
type BindSource struct {
	Source, Label, Group, Class string
}

var bindSources = []BindSource{
	{"entity.name", "Name", "This page", BindText},
	{"entity.title", "Title", "This page", BindText},
	{"entity.excerpt", "Excerpt", "This page", BindText},
	{"entity.image", "Image", "This page", BindImage},
	{"entity.url", "URL", "This page", BindURL},
	{"entity.published_at", "Published date", "This page", BindText},
	{"entity.updated_at", "Updated date", "This page", BindText},
	{"entity.author.name", "Author name", "This page", BindText},
	{"entity.author.url", "Author URL", "This page", BindURL},
	{"entity.attributes.<path>", "Attribute", "This page", BindText},

	{"site.name", "Site name", "Site", BindText},
	{"site.tagline", "Tagline", "Site", BindText},
	{"site.logo", "Logo", "Site", BindImage},
	{"site.phone", "Phone", "Site", BindText},
	{"site.hours", "Hours", "Site", BindText},
	{"site.address.street", "Street", "Site", BindText},
	{"site.address.city", "City", "Site", BindText},
	{"site.address.state", "State", "Site", BindText},
	{"site.address.postal_code", "Postal code", "Site", BindText},
	{"site.address.country", "Country", "Site", BindText},
	{"site.settings.<key>", "Setting", "Site", BindText},

	{"term.name", "Name", "Term", BindText},
	{"term.slug", "Slug", "Term", BindText},
	{"term.url", "URL", "Term", BindURL},
	{"term.description", "Description", "Term", BindText},
	{"term.kind", "Kind", "Term", BindText},

	{"loop.item.title", "Title", "Loop item", BindText},
	{"loop.item.name", "Name", "Loop item", BindText},
	{"loop.item.url", "URL", "Loop item", BindURL},
	{"loop.item.excerpt", "Excerpt", "Loop item", BindText},
	{"loop.item.image", "Image", "Loop item", BindImage},
	{"loop.item.category", "Category", "Loop item", BindText},
	{"loop.item.author", "Author", "Loop item", BindText},
	{"loop.item.author_url", "Author URL", "Loop item", BindURL},
	{"loop.item.published_at", "Published date", "Loop item", BindText},
	{"loop.item.reading_time", "Reading time", "Loop item", BindText},
	{"loop.item.attributes.<path>", "Attribute", "Loop item", BindText},
}

// patternPrefixes are the sources that take a dotted path; the path rule
// (1 to 4 segments) is shared.
var patternPrefixes = []string{"entity.attributes.", "site.settings.", "loop.item.attributes."}

var (
	exactBinds = func() map[string]string {
		m := map[string]string{}
		for _, s := range bindSources {
			if !strings.HasSuffix(s.Source, ">") {
				m[s.Source] = s.Class
			}
		}
		return m
	}()
	bindSegRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// bodies are never bound: the first path segment may not name one.
	bodySegs = map[string]bool{"page_sections": true, "page_doc": true, "content": true}
)

// PropBindClass is the binding class ("text", "url", "image") the prop key of
// kind takes, "" when it is not bindable. It reads the live registry, so a
// plugin widget counts as soon as it is registered.
func PropBindClass(kind, key string) string {
	return reg.Load().kinds[kind].bind[key]
}

// BindSources lists the source vocabulary for the editor, in display order.
// Pattern sources end in "<path>" or "<key>". Registered plugin value
// sources (ext.<plugin>.<name>) follow the core ones. The result is a fresh copy.
func BindSources() []BindSource {
	return append(append([]BindSource(nil), bindSources...), extBindSources()...)
}

// SourcesFor lists the sources a prop of propClass (text, url or image) may
// bind to, in vocabulary order; inLoop says whether a loop item is in scope.
// Pattern sources are included (their Source ends in "<path>" or "<key>").
func SourcesFor(propClass string, inLoop bool) []BindSource {
	var out []BindSource
	for _, s := range BindSources() {
		attr := strings.HasSuffix(s.Source, ">")
		if bindOK(propClass, s.Class, attr) && (inLoop || !strings.HasPrefix(s.Source, "loop.item.")) {
			out = append(out, s)
		}
	}
	return out
}

// ValidBind reports the class of a binding source, or an error wrapping
// ErrInvalid when it is not in the vocabulary.
func ValidBind(src string) (class string, err error) {
	class, _, err = parseBind(src)
	if err != nil {
		return "", invalid("%v", err)
	}
	return class, nil
}

// parseBind also reports whether src is an attribute-path source.
func parseBind(src string) (class string, attr bool, err error) {
	if c, ok := exactBinds[src]; ok {
		return c, false, nil
	}
	if strings.HasPrefix(src, "ext.") {
		return parseExtBind(src)
	}
	for _, pre := range patternPrefixes {
		path, ok := strings.CutPrefix(src, pre)
		if !ok {
			continue
		}
		segs := strings.Split(path, ".")
		if len(segs) > 4 {
			return "", false, fmt.Errorf("binding %q: path has more than 4 segments", src)
		}
		for _, s := range segs {
			if !bindSegRe.MatchString(s) {
				return "", false, fmt.Errorf("binding %q: bad path segment %q", src, s)
			}
		}
		if bodySegs[segs[0]] {
			return "", false, fmt.Errorf("binding %q: %s is not bindable", src, segs[0])
		}
		return BindText, true, nil
	}
	return "", false, fmt.Errorf("unknown binding source %q", src)
}

// bindOK is the class matrix: a text prop takes any source; a url or image
// prop takes sources of its class and attribute paths (data of unknown shape).
func bindOK(propClass, srcClass string, attr bool) bool {
	return propClass == BindText || attr || propClass == srcClass
}

// bindProps reads the x-hm-bind annotations of a parsed kind schema.
func bindProps(ws widgetSchema) (map[string]string, error) {
	var out map[string]string
	for key, p := range ws.Properties {
		switch p.Bind {
		case "":
		case BindText, BindURL, BindImage:
			if out == nil {
				out = map[string]string{}
			}
			out[key] = p.Bind
		default:
			return nil, fmt.Errorf("prop %q: x-hm-bind %q is not text, url or image", key, p.Bind)
		}
	}
	return out, nil
}

// validateBinds checks the b map of one node: every key is a bindable prop of
// the kind, every source is in the vocabulary and fits the prop, and loop.item
// sources only occur inside a loop.
func validateBinds(r *registry, x *Node, inLoop bool) error {
	if len(x.B) == 0 {
		return nil
	}
	keys := make([]string, 0, len(x.B))
	for k := range x.B {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		src := x.B[key]
		k := r.kinds[x.T]
		propClass, ok := k.bind[key]
		if !ok {
			return fmt.Errorf("prop %q of %s cannot be bound", key, x.T)
		}
		srcClass, attr, err := parseBind(src)
		if err != nil {
			return err
		}
		if !bindOK(propClass, srcClass, attr) {
			return fmt.Errorf("%s (%s) cannot bind to %s prop %q", src, srcClass, propClass, key)
		}
		if strings.HasPrefix(src, "loop.item.") && !inLoop {
			return fmt.Errorf("%s is only valid inside a loop", src)
		}
	}
	return nil
}
