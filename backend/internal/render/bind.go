package render

import (
	"maps"
	"math"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/builder"
)

// bindScope is the loop item a node renders for; nil outside a loop item.
type bindScope struct{ item *RelatedItem }

// dateLayout is how bound dates read, through the same formatDate the
// templates use.
const dateLayout = "2 January 2006"

var (
	excerptAttrs = []string{"excerpt", "summary", "description"}
	imageAttrs   = []string{"featured_image", "hero_image", "cover", "image", "thumbnail"}
)

// bound returns n itself when it has no bindings, otherwise a shallow copy
// whose props carry the bound values, so every reader downstream (escaping,
// safeURL, srcset, widget preps) treats them exactly like literals.
func (e *Engine) bound(v *PageView, n *builder.Node) *builder.Node {
	if len(n.B) == 0 {
		return n
	}
	cp := *n
	cp.P = maps.Clone(n.P)
	if cp.P == nil {
		cp.P = make(map[string]any, len(n.B))
	}
	for key, src := range n.B {
		val, _ := e.resolveBind(v, src)
		switch {
		case val != "":
			cp.P[key] = val
		case v.Canvas && bindsText(n.T, key):
			cp.P[key] = "‹" + src + "›"
		default:
			delete(cp.P, key)
		}
	}
	return &cp
}

// bindsText reports whether key of kind takes a text-class binding, the only
// kind of prop that shows a canvas placeholder. It reads the live builder
// registry, so plugin widgets registered later count too.
func bindsText(kind, key string) bool { return builder.PropBindClass(kind, key) == builder.BindText }

// resolveBind reads one source of the closed vocabulary (builder.BindSources)
// from the view. A source that has no value here (no term, no loop item)
// gives ("", false).
func (e *Engine) resolveBind(v *PageView, src string) (string, bool) {
	root, rest, _ := strings.Cut(src, ".")
	var val string
	switch root {
	case "entity":
		val = entityBind(v, rest)
	case "site":
		val = siteBind(&v.Site, rest)
	case "term":
		val = termBind(v.Term, rest)
	case "ext":
		val = v.extValue(src)
	case "loop":
		if v.scope != nil && v.scope.item != nil {
			val = itemBind(v.scope.item, strings.TrimPrefix(rest, "item."))
		}
	}
	return val, val != ""
}

func entityBind(v *PageView, f string) string {
	ent, attrs := v.Entity, v.Attrs
	if ent != nil && ent.Attributes != nil {
		attrs = ent.Attributes
	}
	switch f {
	case "name":
		return v.Page.Name
	case "title":
		if ent != nil && ent.Title != "" {
			return ent.Title
		}
		return firstNonEmpty(attrString(attrs, "title"), v.Page.Name)
	case "excerpt":
		if ent != nil && ent.Excerpt != "" {
			return ent.Excerpt
		}
		return firstAttr(attrs, excerptAttrs)
	case "image":
		if ent != nil && ent.Image != "" {
			return ent.Image
		}
		return firstAttr(attrs, imageAttrs)
	case "url":
		if ent != nil {
			return ent.URL
		}
	case "published_at":
		if ent != nil && !ent.PublishedAt.IsZero() {
			return formatDate(ent.PublishedAt, dateLayout)
		}
		return formatDate(v.PublishedAt, dateLayout)
	case "updated_at":
		if ent != nil && !ent.UpdatedAt.IsZero() {
			return formatDate(ent.UpdatedAt, dateLayout)
		}
		return formatDate(v.UpdatedAt, dateLayout)
	case "author.name":
		if ent != nil && ent.Author != nil {
			return ent.Author.Name
		}
	case "author.url":
		if ent != nil && ent.Author != nil {
			return ent.Author.URL
		}
	default:
		if path, ok := strings.CutPrefix(f, "attributes."); ok {
			return attrPath(attrs, path)
		}
	}
	return ""
}

func siteBind(s *SiteView, f string) string {
	switch f {
	case "name":
		return s.Name
	case "tagline":
		return s.Tagline
	case "logo":
		return s.LogoURL
	case "phone":
		return s.Phone
	case "hours":
		return s.Hours
	case "address.street":
		return s.Address.Street
	case "address.city":
		return s.Address.City
	case "address.state":
		return s.Address.State
	case "address.postal_code":
		return s.Address.PostalCode
	case "address.country":
		return s.Address.Country
	}
	if path, ok := strings.CutPrefix(f, "settings."); ok {
		return attrPath(s.Settings, path)
	}
	return ""
}

func termBind(t *TermView, f string) string {
	if t == nil {
		return ""
	}
	switch f {
	case "name":
		return t.Name
	case "slug":
		return t.Slug
	case "url":
		return t.URL
	case "description":
		return t.Description
	case "kind":
		return t.Kind
	}
	return ""
}

func itemBind(it *RelatedItem, f string) string {
	switch f {
	case "title":
		return it.Title
	case "name":
		return it.Name
	case "url":
		return it.URL
	case "excerpt":
		return it.Excerpt
	case "image":
		return it.Image
	case "category":
		return it.Category
	case "author":
		return it.Author
	case "author_url":
		return it.AuthorURL
	case "published_at":
		return formatDate(it.PublishedAt, dateLayout)
	case "reading_time":
		if it.ReadingTime > 0 {
			return strconv.Itoa(it.ReadingTime) + " min read"
		}
		return ""
	}
	if path, ok := strings.CutPrefix(f, "attributes."); ok {
		return attrPath(it.Attrs, path)
	}
	return ""
}

// attrPath walks a dotted path through nested maps and returns a scalar as
// text; anything else (missing, list, object) is "", never JSON.
func attrPath(m map[string]any, path string) string {
	var cur any = m
	for _, seg := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[seg]
	}
	switch x := cur.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

func attrString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func firstAttr(m map[string]any, keys []string) string {
	for _, k := range keys {
		if s := attrString(m, k); s != "" {
			return s
		}
	}
	return ""
}
