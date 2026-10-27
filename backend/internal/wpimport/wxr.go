package wpimport

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// WXR limits.
const (
	DefaultWXRMaxBytes = 512 << 20
	DefaultMaxItems    = 50000
	// MaxWXRDepth is how deep elements may nest. A real export is five or six
	// levels deep; encoding/xml keeps every open element on a stack with no
	// limit of its own, so a crafted file would otherwise cost memory far
	// beyond its size.
	MaxWXRDepth = 64
	// DefaultMaxWXRElements bounds the total number of elements read.
	DefaultMaxWXRElements = 20_000_000
)

// ErrTooComplex is returned when an export nests too deeply or has too many elements.
var ErrTooComplex = errors.New("export is too deeply nested or has too many elements")

// ErrTooLarge is returned when an upload goes over the size cap.
var ErrTooLarge = errors.New("export file is over the size limit")

// WXROptions configures ParseWXR.
type WXROptions struct {
	MaxBytes int64 // 0: DefaultWXRMaxBytes
	MaxItems int   // 0: DefaultMaxItems
	// MaxElements caps the elements of the whole file (0: DefaultMaxWXRElements).
	MaxElements int
}

// wxrDecoder is an xml.Decoder that fails on deep nesting and on too many
// elements. Every token of the file passes through Token or decode.
type wxrDecoder struct {
	*xml.Decoder
	depth, elems, maxElems int
}

func (d *wxrDecoder) Token() (xml.Token, error) {
	tok, err := d.Decoder.Token()
	if err != nil {
		return tok, err
	}
	switch tok.(type) {
	case xml.StartElement:
		d.depth++
		d.elems++
		if d.depth > MaxWXRDepth {
			return nil, fmt.Errorf("%w: elements nest more than %d levels deep", ErrTooComplex, MaxWXRDepth)
		}
		if d.elems > d.maxElems {
			return nil, fmt.Errorf("%w: more than %d elements", ErrTooComplex, d.maxElems)
		}
	case xml.EndElement:
		d.depth--
	}
	return tok, nil
}

// decode reads the character data of start (already returned by Token) into
// *v, skipping child elements, like DecodeElement into a string. It reads
// through Token so nesting inside the element is bounded too: DecodeElement
// skips children without any depth limit.
func (d *wxrDecoder) decode(v *string, start xml.StartElement) error {
	var b strings.Builder
	level := 1
	for level > 0 {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			level++
		case xml.CharData:
			if level == 1 {
				b.Write(t)
			}
		case xml.EndElement:
			level--
		}
	}
	*v = b.String()
	return nil
}

// capReader fails with ErrTooLarge instead of truncating silently.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= int64(n)
	return n, err
}

type wxrItem struct {
	fields map[string]string
	terms  []TermRef
	meta   map[string]string
	nav    string // nav_menu slug
}

// ParseWXR reads a WordPress export as a stream and returns the Dump. The file
// is never held as a whole, but the parsed items are kept until the Dump is
// built, so memory grows with the size of the content (at most MaxBytes).
func ParseWXR(r io.Reader, o WXROptions) (*Dump, error) {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultWXRMaxBytes
	}
	if o.MaxItems <= 0 {
		o.MaxItems = DefaultMaxItems
	}
	if o.MaxElements <= 0 {
		o.MaxElements = DefaultMaxWXRElements
	}
	dec := &wxrDecoder{Decoder: xml.NewDecoder(&capReader{r: r, n: o.MaxBytes}), maxElems: o.MaxElements}
	dec.Strict = false
	d := &Dump{RawBodies: true}
	var lang string
	termByID := map[string]TermRef{}
	var navNames = map[string]string{}
	var raw []wxrItem
	sawChannel := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, ErrTooComplex) {
				return nil, err
			}
			if errors.Is(err, ErrTooLarge) {
				return nil, fmt.Errorf("%w (%d MB): split the export or raise the limit", ErrTooLarge, o.MaxBytes>>20)
			}
			return nil, fmt.Errorf("not a WordPress export (WXR): %w", err)
		}
		st, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch st.Name.Local {
		case "channel":
			sawChannel = true
		case "title":
			if d.Site.Title == "" {
				var v string
				_ = dec.decode(&v, st)
				d.Site.Title = html.UnescapeString(strings.TrimSpace(v))
			}
		case "description":
			if d.Site.Tagline == "" {
				var v string
				_ = dec.decode(&v, st)
				d.Site.Tagline = html.UnescapeString(strings.TrimSpace(v))
			}
		case "language":
			_ = dec.decode(&lang, st)
		case "base_site_url":
			var v string
			_ = dec.decode(&v, st)
			d.Site.URL = strings.TrimRight(strings.TrimSpace(v), "/")
		case "author":
			f, err := flat(dec, st)
			if err != nil {
				return nil, err
			}
			if f["author_login"] != "" {
				d.Authors = append(d.Authors, Author{Key: f["author_login"], Slug: f["author_login"],
					Name: firstNonEmpty(f["author_display_name"], strings.TrimSpace(f["author_first_name"]+" "+f["author_last_name"]), f["author_login"])})
			}
		case "category", "tag", "term":
			f, err := flat(dec, st)
			if err != nil {
				return nil, err
			}
			t := wxrTerm(st.Name.Local, f)
			if t.Slug == "" {
				continue
			}
			if t.Taxonomy == "nav_menu" {
				navNames[t.Slug] = t.Name
				continue
			}
			if id := f["term_id"]; id != "" {
				termByID[id] = TermRef{t.Taxonomy, t.Slug}
			}
			d.Terms = append(d.Terms, t)
		case "item":
			if len(raw) >= o.MaxItems {
				return nil, fmt.Errorf("export has more than %d items: split it or raise the limit", o.MaxItems)
			}
			it, err := readItem(dec, st)
			if err != nil {
				return nil, err
			}
			raw = append(raw, it)
		}
	}
	if !sawChannel {
		return nil, errors.New("not a WordPress export (WXR): no <channel> found")
	}
	d.Site.Language = lang
	buildFromWXR(d, raw, termByID, navNames)
	return d, nil
}

func wxrTerm(kind string, f map[string]string) Term {
	switch kind {
	case "category":
		return Term{Taxonomy: "category", Slug: f["category_nicename"], Name: html.UnescapeString(f["cat_name"]), Description: f["category_description"], ParentSlug: f["category_parent"]}
	case "tag":
		return Term{Taxonomy: "post_tag", Slug: f["tag_slug"], Name: html.UnescapeString(f["tag_name"]), Description: f["tag_description"]}
	}
	return Term{Taxonomy: f["term_taxonomy"], Slug: f["term_slug"], Name: html.UnescapeString(f["term_name"]), Description: f["term_description"], ParentSlug: f["term_parent"]}
}

// flat reads the simple child elements of start into a map keyed by local name.
func flat(dec *wxrDecoder, start xml.StartElement) (map[string]string, error) {
	out := map[string]string{}
	depth := 1
	var cur string
	var buf strings.Builder
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				cur = t.Name.Local
				buf.Reset()
			}
		case xml.CharData:
			if depth == 2 {
				buf.Write(t)
			}
		case xml.EndElement:
			if depth == 2 && cur != "" {
				out[cur] = strings.TrimSpace(buf.String())
				cur = ""
			}
			depth--
		}
	}
	return out, nil
}

func readItem(dec *wxrDecoder, start xml.StartElement) (wxrItem, error) {
	it := wxrItem{fields: map[string]string{}, meta: map[string]string{}}
	for {
		tok, err := dec.Token()
		if err != nil {
			return it, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "category":
				var domain, nice string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "domain":
						domain = a.Value
					case "nicename":
						nice = a.Value
					}
				}
				var name string
				if err := dec.decode(&name, t); err != nil {
					return it, err
				}
				switch domain {
				case "nav_menu":
					it.nav = nice
				case "":
				default:
					it.terms = append(it.terms, TermRef{domain, nice})
				}
			case "postmeta":
				f, err := flat(dec, t)
				if err != nil {
					return it, err
				}
				if k := f["meta_key"]; k != "" {
					it.meta[k] = f["meta_value"]
				}
			case "encoded":
				var v string
				if err := dec.decode(&v, t); err != nil {
					return it, err
				}
				if strings.Contains(t.Name.Space, "excerpt") {
					it.fields["excerpt"] = v
				} else {
					it.fields["content"] = v
				}
			default:
				var v string
				if err := dec.decode(&v, t); err != nil {
					return it, err
				}
				if _, dup := it.fields[t.Name.Local]; !dup {
					it.fields[t.Name.Local] = strings.TrimSpace(v)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "item" {
				return it, nil
			}
		}
	}
}

func parseWXRTime(s string) time.Time {
	if s == "" || strings.HasPrefix(s, "0000") {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

func buildFromWXR(d *Dump, raw []wxrItem, termByID map[string]TermRef, navNames map[string]string) {
	menus := map[string]*Menu{}
	var menuOrder []string
	seen := map[string]bool{}
	polylangItems := false
	for _, w := range raw {
		f := w.fields
		typ, status := f["post_type"], f["status"]
		id := f["post_id"]
		switch {
		case status == "auto-draft" || status == wpTrash:
			continue
		case typ == "attachment":
			url := f["attachment_url"]
			if url == "" {
				continue
			}
			d.Media = append(d.Media, Media{ID: id, URL: url, Title: html.UnescapeString(f["title"]), Alt: w.meta["_wp_attachment_image_alt"],
				Caption: stripTags(f["excerpt"]), Date: parseWXRTime(f["post_date_gmt"])})
			continue
		case typ == "nav_menu_item":
			m := menus[w.nav]
			if m == nil {
				m = &Menu{ID: w.nav, Slug: w.nav, Name: firstNonEmpty(navNames[w.nav], w.nav)}
				menus[w.nav] = m
				menuOrder = append(menuOrder, w.nav)
			}
			mi := MenuItem{ID: id, Title: html.UnescapeString(f["title"]), URL: w.meta["_menu_item_url"], Type: w.meta["_menu_item_type"],
				Object: w.meta["_menu_item_object"], ObjectID: w.meta["_menu_item_object_id"], NewTab: w.meta["_menu_item_target"] == "_blank"}
			if p := w.meta["_menu_item_menu_item_parent"]; p != "" && p != "0" {
				mi.ParentID = p
			}
			fmt.Sscanf(f["menu_order"], "%d", &mi.Order)
			if mi.Type == "taxonomy" {
				if ref, ok := termByID[mi.ObjectID]; ok {
					mi.ObjectID = ref.Taxonomy + ":" + ref.Slug
				}
			}
			m.Items = append(m.Items, mi)
			continue
		case typ == "" || skipType(typ):
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		polylang := ""
		for _, t := range w.terms {
			if t.Taxonomy == "language" {
				polylang = t.Slug
				polylangItems = true
			}
		}
		it := Item{
			ID: id, Type: typ, Status: status, Slug: f["post_name"], Title: html.UnescapeString(f["title"]),
			Content: f["content"], Raw: f["content"], Excerpt: f["excerpt"], Link: f["link"],
			Date: parseWXRTime(f["post_date_gmt"]), Modified: parseWXRTime(f["post_modified_gmt"]),
			AuthorKey: f["creator"], Protected: f["post_password"] != "", Meta: map[string]string{},
			Lang: itemLang(w.meta, polylang),
		}
		if p := f["post_parent"]; p != "" && p != "0" {
			it.ParentID = p
		}
		if th := w.meta["_thumbnail_id"]; th != "" {
			it.FeaturedMedia = th
		}
		for _, t := range w.terms {
			if t.Taxonomy != "language" && !skipTaxonomy(t.Taxonomy) {
				it.Terms = append(it.Terms, t)
			}
		}
		for k, v := range w.meta {
			if !strings.HasPrefix(k, "_oembed") && len(v) < 2048 {
				it.Meta[k] = v
			}
		}
		it.SEO = seoFromMeta(w.meta, d.Site, it)
		d.Items = append(d.Items, it)
		if typ != "post" && typ != "page" && !hasCustomType(d.Detect.CustomTypes, typ) {
			d.Detect.CustomTypes = append(d.Detect.CustomTypes, CustomType{Slug: typ, Name: typ})
		}
	}
	for _, k := range menuOrder {
		sort.SliceStable(menus[k].Items, func(i, j int) bool { return menus[k].Items[i].Order < menus[k].Items[j].Order })
		d.Menus = append(d.Menus, *menus[k])
	}
	if polylangItems && d.Detect.DefaultLang == "" {
		// A WXR export has no language list; the site language is the default.
		d.Detect.DefaultLang = strings.ToLower(strings.SplitN(d.Site.Language, "-", 2)[0])
	}
	detectFromMeta(d)
	for _, it := range d.Items {
		if it.Status == wpPublish && it.Link != "" {
			d.OldURLs = append(d.OldURLs, it.Link)
		}
	}
	d.Site.Permalink = inferStructure(d.Items)
	if d.Site.URL == "" {
		for _, it := range d.Items {
			if u := originOf(it.Link); u != "" {
				d.Site.URL = u
				break
			}
		}
	}
}

func hasCustomType(list []CustomType, slug string) bool {
	for _, c := range list {
		if c.Slug == slug {
			return true
		}
	}
	return false
}

func originOf(link string) string {
	i := strings.Index(link, "://")
	if i < 0 {
		return ""
	}
	rest := link[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return link[:i+3] + rest
}

// detectFromMeta finds the SEO plugin and builders from post meta keys.
func detectFromMeta(d *Dump) {
	keys := map[string]bool{}
	for _, it := range d.Items {
		for k := range it.Meta {
			keys[k] = true
		}
	}
	has := func(prefix string) bool {
		for k := range keys {
			if strings.HasPrefix(k, prefix) {
				return true
			}
		}
		return false
	}
	switch {
	case has("_yoast_wpseo_"):
		d.Detect.SEOPlugin = "yoast"
	case has("rank_math_"):
		d.Detect.SEOPlugin = "rankmath"
	case has("_seopress_"):
		d.Detect.SEOPlugin = "seopress"
	case has("_aioseo_"):
		d.Detect.SEOPlugin = "aioseo"
	}
	if has("_elementor_") {
		d.Detect.PageBuilders = appendUnique(d.Detect.PageBuilders, "Elementor")
	}
	if has("_et_pb_") {
		d.Detect.PageBuilders = appendUnique(d.Detect.PageBuilders, "Divi")
	}
	if has("_wpb_vc_") {
		d.Detect.PageBuilders = appendUnique(d.Detect.PageBuilders, "WPBakery")
	}
	if has("_wpml_") || has("_icl_") {
		d.Detect.Multilingual = appendUnique(d.Detect.Multilingual, "WPML")
	}
	for _, t := range d.Terms {
		if t.Taxonomy == "language" {
			d.Detect.Multilingual = appendUnique(d.Detect.Multilingual, "Polylang")
		}
	}
}

var (
	varRe    = regexp.MustCompile(`%{1,2}[a-z_]+%{1,2}`)
	spaceRe  = regexp.MustCompile(`\s{2,}`)
	phpStrRe = regexp.MustCompile(`noindex`)
)

// expandTemplate resolves the variables of an SEO title or description
// template (Yoast %%title%%, Rank Math and SEOPress %title%); unknown ones are
// removed.
func expandTemplate(tpl string, site Site, it Item) string {
	vals := map[string]string{
		"title": it.Title, "post_title": it.Title, "page_title": it.Title,
		"sitename": site.Title, "sitetitle": site.Title, "site_title": site.Title,
		"sitedesc": site.Tagline, "tagline": site.Tagline, "site_tagline": site.Tagline,
		"sep": "-", "separator": "-", "excerpt": it.Excerpt,
	}
	out := varRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := strings.Trim(m, "%")
		if name == "sep" || name == "separator" {
			return " - "
		}
		return vals[name]
	})
	out = spaceRe.ReplaceAllString(strings.TrimSpace(out), " ")
	return html.UnescapeString(strings.TrimSpace(out))
}

// seoFromMeta maps the post meta of Yoast, Rank Math, SEOPress and All in One
// SEO to SEO fields.
func seoFromMeta(meta map[string]string, site Site, it Item) SEO {
	var s SEO
	switch {
	case meta["_yoast_wpseo_title"] != "" || meta["_yoast_wpseo_metadesc"] != "" || meta["_yoast_wpseo_canonical"] != "" || meta["_yoast_wpseo_meta-robots-noindex"] != "":
		s = SEO{Source: "yoast", Title: meta["_yoast_wpseo_title"], Description: meta["_yoast_wpseo_metadesc"], Canonical: meta["_yoast_wpseo_canonical"],
			NoIndex: meta["_yoast_wpseo_meta-robots-noindex"] == "1", OGImage: meta["_yoast_wpseo_opengraph-image"]}
	case meta["rank_math_title"] != "" || meta["rank_math_description"] != "" || meta["rank_math_robots"] != "":
		s = SEO{Source: "rankmath", Title: meta["rank_math_title"], Description: meta["rank_math_description"], Canonical: meta["rank_math_canonical_url"],
			NoIndex: phpStrRe.MatchString(meta["rank_math_robots"]), OGImage: meta["rank_math_facebook_image"]}
	case meta["_seopress_titles_title"] != "" || meta["_seopress_titles_desc"] != "" || meta["_seopress_robots_index"] != "":
		s = SEO{Source: "seopress", Title: meta["_seopress_titles_title"], Description: meta["_seopress_titles_desc"], Canonical: meta["_seopress_robots_canonical"],
			NoIndex: meta["_seopress_robots_index"] == "yes", OGImage: meta["_seopress_social_fb_img"]}
	case meta["_aioseo_title"] != "" || meta["_aioseo_description"] != "":
		s = SEO{Source: "aioseo", Title: meta["_aioseo_title"], Description: meta["_aioseo_description"]}
	default:
		return SEO{}
	}
	s.Title = expandTemplate(s.Title, site, it)
	s.Description = expandTemplate(s.Description, site, it)
	return s
}
