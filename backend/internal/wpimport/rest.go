package wpimport

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RESTOptions configures a REST source.
type RESTOptions struct {
	ClientOptions
	// MaxItems caps posts, pages and custom post types together (0: 50000).
	MaxItems int
	// MaxCollection caps each of terms, users, media and menu items (0: DefaultMaxCollection).
	MaxCollection int
	// MaxDuration bounds the whole read (0: 4 hours).
	MaxDuration time.Duration
	// Concurrency bounds the per-item requests (SEO data); 0 means 4.
	Concurrency int
	// Progress receives short status lines.
	Progress func(string)
}

// RESTSource reads a site through its WordPress REST API.
type RESTSource struct {
	c    *Client
	opts RESTOptions
}

// NewRESTSource builds the source; nothing is fetched yet.
func NewRESTSource(o RESTOptions) (*RESTSource, error) {
	c, err := NewClient(o.ClientOptions)
	if err != nil {
		return nil, err
	}
	if o.MaxItems <= 0 {
		o.MaxItems = 50000
	}
	if o.MaxCollection <= 0 {
		o.MaxCollection = DefaultMaxCollection
	}
	if o.MaxDuration <= 0 {
		o.MaxDuration = 4 * time.Hour
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
	return &RESTSource{c: c, opts: o}, nil
}

// Client exposes the HTTP client (media downloads and the audit reuse it).
func (s *RESTSource) Client() *Client { return s.c }

// Ref identifies the source in import runs.
func (s *RESTSource) Ref() string { return s.c.Base.String() }

// coreNamespaces are REST namespaces that belong to WordPress itself.
var coreNamespaces = map[string]bool{
	"oembed/1.0": true, "wp/v2": true, "wp/v3": true, "wp-site-health/v1": true, "wp-block-editor/v1": true,
	"wp-abilities/v1": true, "wp-abilities/v2": true,
}

// skipTypes are post types that are not site content.
func skipType(name string) bool {
	if strings.HasPrefix(name, "wp_") || strings.HasPrefix(name, "acf-") || strings.HasPrefix(name, "e-") {
		return true
	}
	switch name {
	case "attachment", "nav_menu_item", "revision", "custom_css", "customize_changeset", "oembed_cache",
		"user_request", "elementor_library", "wpcf7_contact_form", "shop_order", "shop_coupon":
		return true
	}
	return false
}

func skipTaxonomy(name string) bool {
	switch name {
	case "nav_menu", "link_category", "post_format", "wp_theme", "wp_template_part_area", "wp_pattern_category", "language", "post_translations", "term_language", "term_translations":
		return true
	}
	return false
}

type rendered struct {
	Rendered string `json:"rendered"`
	Raw      string `json:"raw"`
}

type restPost struct {
	ID            int         `json:"id"`
	DateGMT       string      `json:"date_gmt"`
	ModifiedGMT   string      `json:"modified_gmt"`
	Slug          string      `json:"slug"`
	Status        string      `json:"status"`
	Type          string      `json:"type"`
	Link          string      `json:"link"`
	Password      string      `json:"password"`
	Title         rendered    `json:"title"`
	Content       restContent `json:"content"`
	Excerpt       rendered    `json:"excerpt"`
	Author        int         `json:"author"`
	FeaturedMedia int         `json:"featured_media"`
	Parent        int         `json:"parent"`
	Lang          string      `json:"lang"`
	Yoast         *yoastHead  `json:"yoast_head_json"`
}

type restContent struct {
	Rendered  string `json:"rendered"`
	Raw       string `json:"raw"`
	Protected bool   `json:"protected"`
}

type yoastHead struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Canonical   string `json:"canonical"`
	Robots      struct {
		Index string `json:"index"`
	} `json:"robots"`
	OGImage []struct {
		URL string `json:"url"`
	} `json:"og_image"`
}

func wpTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Load reads the whole site.
func (s *RESTSource) Load(ctx context.Context) (*Dump, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opts.MaxDuration)
	defer cancel()
	d := &Dump{}
	say := s.opts.Progress
	var idx struct {
		Name       string   `json:"name"`
		Desc       string   `json:"description"`
		URL        string   `json:"url"`
		Home       string   `json:"home"`
		Timezone   string   `json:"timezone_string"`
		GMTOffset  float64  `json:"gmt_offset"`
		Namespaces []string `json:"namespaces"`
	}
	if _, err := s.c.JSON(ctx, "/", nil, &idx); err != nil {
		// Plain permalinks: the REST API answers on ?rest_route=.
		s.c.routeParam = true
		if _, err2 := s.c.JSON(ctx, "/", nil, &idx); err2 != nil {
			return nil, fmt.Errorf("the site does not answer on its WordPress REST API (%v): is the URL right and the REST API enabled?", err)
		}
	}
	home := strings.TrimRight(firstNonEmpty(idx.Home, idx.URL, s.c.Base.String()), "/")
	d.Site = Site{URL: home, Title: html.UnescapeString(idx.Name), Tagline: html.UnescapeString(idx.Desc), Timezone: idx.Timezone}
	if d.Site.Timezone == "" && idx.GMTOffset == float64(int(idx.GMTOffset)) && idx.GMTOffset != 0 {
		// A fixed offset: Etc/GMT zones count the other way round (UTC+2 is Etc/GMT-2).
		d.Site.Timezone = fmt.Sprintf("Etc/GMT%+d", -int(idx.GMTOffset))
	}
	d.Detect.Namespaces = idx.Namespaces
	say(fmt.Sprintf("connected to %s (%s)", d.Site.URL, d.Site.Title))

	if s.c.Authed() {
		var st struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Language    string `json:"language"`
			Timezone    string `json:"timezone"`
			ShowOnFront string `json:"show_on_front"`
			PageOnFront int    `json:"page_on_front"`
			PageForPost int    `json:"page_for_posts"`
		}
		if _, err := s.c.JSON(ctx, "/wp/v2/settings", nil, &st); err != nil {
			d.Warnings = append(d.Warnings, "could not read settings with the application password: "+err.Error())
		} else {
			d.Site.Language = strings.ReplaceAll(st.Language, "_", "-")
			d.Site.ShowOnFront, d.Site.FrontPageID, d.Site.PostsPageID = st.ShowOnFront, st.PageOnFront, st.PageForPost
			if st.Timezone != "" {
				d.Site.Timezone = st.Timezone
			}
		}
	} else {
		d.Warnings = append(d.Warnings, "no application password: drafts, private posts, menus and settings are not read")
	}
	if d.Site.ShowOnFront == "" {
		var fp struct {
			ShowOnFront string `json:"show_on_front"`
			PageOnFront int    `json:"page_on_front"`
			PageForPost int    `json:"page_for_posts"`
		}
		_, _ = s.c.JSON(ctx, "/", nil, &fp) // newer WordPress lists them in the index
		d.Site.ShowOnFront, d.Site.FrontPageID, d.Site.PostsPageID = fp.ShowOnFront, fp.PageOnFront, fp.PageForPost
	}

	// Multilingual: ask Polylang for its languages and read every one, tagging each item.
	for _, ns := range idx.Namespaces {
		switch {
		case strings.HasPrefix(ns, "pll/"):
			d.Detect.Multilingual = appendUnique(d.Detect.Multilingual, "Polylang")
		case strings.HasPrefix(ns, "wpml/"):
			d.Detect.Multilingual = appendUnique(d.Detect.Multilingual, "WPML")
		}
	}
	langs := []string{""} // one unfiltered pass unless Polylang lists its languages
	if hasString(d.Detect.Multilingual, "Polylang") {
		var list []struct {
			Slug    string `json:"slug"`
			Default bool   `json:"is_default"`
		}
		if _, err := s.c.JSON(ctx, "/pll/v1/languages", nil, &list); err == nil && len(list) > 0 {
			langs = langs[:0]
			for _, l := range list {
				langs = append(langs, l.Slug)
				if l.Default {
					d.Detect.DefaultLang = l.Slug
				}
			}
		} else {
			d.Warnings = append(d.Warnings, "Polylang found but its languages could not be read: items are imported without a language tag")
		}
	}

	// Content types.
	var types map[string]struct {
		Name         string `json:"name"`
		Slug         string `json:"slug"`
		RESTBase     string `json:"rest_base"`
		Hierarchical bool   `json:"hierarchical"`
		HasArchive   any    `json:"has_archive"`
	}
	type typeRef struct{ slug, base string }
	var pts []typeRef
	if _, err := s.c.JSON(ctx, "/wp/v2/types", nil, &types); err != nil {
		d.Warnings = append(d.Warnings, "could not list content types, importing posts and pages only: "+err.Error())
		pts = []typeRef{{"post", "posts"}, {"page", "pages"}}
	} else {
		names := make([]string, 0, len(types))
		for n := range types {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			t := types[n]
			if skipType(n) || t.RESTBase == "" {
				continue
			}
			pts = append(pts, typeRef{n, t.RESTBase})
			if n != "post" && n != "page" {
				d.Detect.CustomTypes = append(d.Detect.CustomTypes, CustomType{Slug: n, Name: t.Name, RESTBase: t.RESTBase, Hierarchical: t.Hierarchical, HasArchive: t.HasArchive == true})
			}
		}
	}

	// Taxonomies and terms.
	var taxes map[string]struct {
		Slug     string `json:"slug"`
		RESTBase string `json:"rest_base"`
	}
	taxBase := map[string]string{} // taxonomy -> rest base
	if _, err := s.c.JSON(ctx, "/wp/v2/taxonomies", nil, &taxes); err != nil {
		taxBase = map[string]string{"category": "categories", "post_tag": "tags"}
		d.Warnings = append(d.Warnings, "could not list taxonomies, importing categories and tags only: "+err.Error())
	} else {
		for n, t := range taxes {
			if !skipTaxonomy(n) && t.RESTBase != "" {
				taxBase[n] = t.RESTBase
			}
		}
	}
	type termKey struct{ tax, slug string }
	termByID := map[string]map[int]termKey{} // taxonomy -> id -> key
	taxNames := make([]string, 0, len(taxBase))
	for n := range taxBase {
		taxNames = append(taxNames, n)
	}
	sort.Strings(taxNames)
	for _, tax := range taxNames {
		termByID[tax] = map[int]termKey{}
		type rt struct {
			ID          int    `json:"id"`
			Slug        string `json:"slug"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Parent      int    `json:"parent"`
			Link        string `json:"link"`
		}
		var all []rt
		err := s.c.Paged(ctx, "/wp/v2/"+taxBase[tax], url.Values{"hide_empty": {"false"}}, s.opts.MaxCollection, func(raw json.RawMessage) error {
			var t rt
			if err := json.Unmarshal(raw, &t); err != nil {
				return err
			}
			all = append(all, t)
			termByID[tax][t.ID] = termKey{tax, t.Slug}
			return nil
		})
		if err != nil {
			d.Warnings = append(d.Warnings, fmt.Sprintf("could not read %s terms: %v", tax, err))
			continue
		}
		for _, t := range all {
			parent := ""
			if t.Parent != 0 {
				parent = termByID[tax][t.Parent].slug
			}
			d.Terms = append(d.Terms, Term{Taxonomy: tax, Slug: t.Slug, Name: html.UnescapeString(t.Name), Description: html.UnescapeString(t.Description), ParentSlug: parent, Link: t.Link})
		}
	}
	say(fmt.Sprintf("read %d terms", len(d.Terms)))

	// Authors.
	userSlug := map[int]string{}
	err := s.c.Paged(ctx, "/wp/v2/users", nil, s.opts.MaxCollection, func(raw json.RawMessage) error {
		var u struct {
			ID          int               `json:"id"`
			Name        string            `json:"name"`
			Slug        string            `json:"slug"`
			Description string            `json:"description"`
			Link        string            `json:"link"`
			Avatars     map[string]string `json:"avatar_urls"`
		}
		if err := json.Unmarshal(raw, &u); err != nil {
			return err
		}
		userSlug[u.ID] = u.Slug
		d.Authors = append(d.Authors, Author{Key: u.Slug, Name: html.UnescapeString(u.Name), Slug: u.Slug, Bio: u.Description, Link: u.Link, AvatarURL: ownAvatar(u.Avatars, d.Site.URL)})
		return nil
	})
	if err != nil {
		d.Warnings = append(d.Warnings, "could not read authors: "+err.Error())
	}
	say(fmt.Sprintf("read %d authors", len(d.Authors)))

	// Media library.
	err = s.c.Paged(ctx, "/wp/v2/media", nil, s.opts.MaxCollection, func(raw json.RawMessage) error {
		var m struct {
			ID      int      `json:"id"`
			DateGMT string   `json:"date_gmt"`
			Src     string   `json:"source_url"`
			Mime    string   `json:"mime_type"`
			Alt     string   `json:"alt_text"`
			Title   rendered `json:"title"`
			Caption rendered `json:"caption"`
			Details struct {
				Sizes map[string]struct {
					URL string `json:"source_url"`
				} `json:"sizes"`
			} `json:"media_details"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		med := Media{ID: strconv.Itoa(m.ID), URL: m.Src, Mime: m.Mime, Alt: m.Alt, Title: html.UnescapeString(m.Title.Rendered),
			Caption: stripTags(m.Caption.Rendered), Date: wpTime(m.DateGMT)}
		for _, sz := range m.Details.Sizes {
			if sz.URL != "" && sz.URL != m.Src {
				med.Sizes = append(med.Sizes, sz.URL)
			}
		}
		sort.Strings(med.Sizes)
		d.Media = append(d.Media, med)
		return nil
	})
	if err != nil {
		d.Warnings = append(d.Warnings, "could not read the media library: "+err.Error())
	}
	say(fmt.Sprintf("read %d media files", len(d.Media)))

	// Items.
	q := url.Values{}
	if s.c.Authed() {
		q.Set("context", "edit")
		q.Set("status", "publish,future,draft,pending,private")
	}
	seen := 0
	// Polylang ignores the lang parameter for post types it does not translate,
	// so those come back in full on every language pass: keep the first copy.
	got := map[string]bool{}
	for _, lang := range langs {
		if lang != "" {
			q.Set("lang", lang)
		}
		for _, pt := range pts {
			route := "/wp/v2/" + pt.base
			err := s.c.Paged(ctx, route, q, s.opts.MaxItems-seen, func(raw json.RawMessage) error {
				var p restPost
				if err := json.Unmarshal(raw, &p); err != nil {
					return err
				}
				key := pt.slug + "/" + strconv.Itoa(p.ID)
				if got[key] {
					return nil
				}
				got[key] = true
				var extra map[string]json.RawMessage
				_ = json.Unmarshal(raw, &extra)
				seen++
				it := Item{
					ID: strconv.Itoa(p.ID), Type: pt.slug, Status: p.Status, Slug: p.Slug,
					Title: html.UnescapeString(p.Title.Rendered), Content: p.Content.Rendered, Raw: p.Content.Raw,
					Excerpt: stripTags(p.Excerpt.Rendered), Link: p.Link,
					Date: wpTime(p.DateGMT), Modified: wpTime(p.ModifiedGMT),
					Protected: p.Content.Protected || p.Password != "",
					Lang:      p.Lang, // empty (untranslated type): the site default, not the pass language
				}
				if it.Protected && p.Content.Raw != "" {
					it.Content = p.Content.Raw // the rendered body of a protected post is the password form
				}
				if p.Parent != 0 {
					it.ParentID = strconv.Itoa(p.Parent)
				}
				if p.FeaturedMedia != 0 {
					it.FeaturedMedia = strconv.Itoa(p.FeaturedMedia)
				}
				if p.Author != 0 {
					it.AuthorKey = userSlug[p.Author]
					if it.AuthorKey == "" {
						it.AuthorKey = s.fetchAuthor(ctx, d, userSlug, p.Author)
					}
				}
				for tax, base := range taxBase {
					ids := intList(extra[base])
					for _, id := range ids {
						if k, ok := termByID[tax][id]; ok {
							it.Terms = append(it.Terms, TermRef{tax, k.slug})
						}
					}
				}
				sort.Slice(it.Terms, func(i, j int) bool {
					a, b := it.Terms[i], it.Terms[j]
					return a.Taxonomy+"/"+a.Slug < b.Taxonomy+"/"+b.Slug
				})
				if p.Yoast != nil && p.Yoast.Title != "" {
					it.SEO = seoFromYoast(p.Yoast)
				}
				d.Items = append(d.Items, it)
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", pt.slug, err)
			}
			say(fmt.Sprintf("read %s (%d items so far)", pt.slug, len(d.Items)))
		}
	}

	s.loadSEO(ctx, d)
	s.loadMenus(ctx, d)
	d.OldURLs = s.sitemapURLs(ctx, d)
	say(fmt.Sprintf("old sitemap lists %d URLs", len(d.OldURLs)))
	d.Site.Permalink = inferStructure(d.Items)
	return d, nil
}

func seoFromYoast(y *yoastHead) SEO {
	out := SEO{Title: html.UnescapeString(y.Title), Description: html.UnescapeString(y.Description), Canonical: y.Canonical,
		NoIndex: strings.Contains(strings.ToLower(y.Robots.Index), "noindex"), Source: "yoast"}
	if len(y.OGImage) > 0 {
		out.OGImage = y.OGImage[0].URL
	}
	return out
}

// loadSEO fills Item.SEO. Yoast data is read per item (a collection request
// can return the same head for every item on some installs), other SEO
// plugins are read from the page's own <head>, which every plugin writes.
func (s *RESTSource) loadSEO(ctx context.Context, d *Dump) {
	plugin := ""
	for _, ns := range d.Detect.Namespaces {
		switch {
		case strings.HasPrefix(ns, "yoast/"):
			plugin = "yoast"
		case strings.HasPrefix(ns, "rankmath/") && plugin == "":
			plugin = "rankmath"
		case strings.HasPrefix(ns, "seopress/") && plugin == "":
			plugin = "seopress"
		case strings.HasPrefix(ns, "aioseo/") && plugin == "":
			plugin = "aioseo"
		}
	}
	d.Detect.SEOPlugin = plugin
	if plugin == "" {
		return
	}
	bases := map[string]string{}
	for _, ct := range d.Detect.CustomTypes {
		bases[ct.Slug] = ct.RESTBase
	}
	bases["post"], bases["page"] = "posts", "pages"
	sem := make(chan struct{}, s.opts.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	for i := range d.Items {
		it := &d.Items[i]
		if it.Status == wpTrash {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var seo SEO
			var err error
			if plugin == "yoast" {
				seo, err = s.yoastItem(ctx, bases[it.Type], it.ID)
			} else if it.Status == wpPublish {
				seo, err = s.headSEO(ctx, it.Link, plugin)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				return
			}
			if seo.Title != "" || seo.Description != "" || seo.NoIndex {
				it.SEO = seo
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		d.Warnings = append(d.Warnings, fmt.Sprintf("SEO data could not be read for %d items", failed))
	}
	s.opts.Progress(fmt.Sprintf("read %s SEO data", plugin))
}

func (s *RESTSource) yoastItem(ctx context.Context, base, id string) (SEO, error) {
	var out struct {
		Y *yoastHead `json:"yoast_head_json"`
	}
	q := url.Values{"_fields": {"id,yoast_head_json"}}
	if s.c.Authed() {
		q.Set("context", "edit")
	}
	if _, err := s.c.JSON(ctx, "/wp/v2/"+base+"/"+id, q, &out); err != nil {
		return SEO{}, err
	}
	if out.Y == nil {
		return SEO{}, nil
	}
	return seoFromYoast(out.Y), nil
}

var (
	titleRe     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	metaTagRe   = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
	linkTagRe   = regexp.MustCompile(`(?is)<link\s+[^>]*>`)
	attrPairRe  = regexp.MustCompile(`(?is)([a-zA-Z:_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	canonicalRe = regexp.MustCompile(`(?i)rel=["']canonical["']`)
)

func attrsOf(tag string) map[string]string {
	m := map[string]string{}
	for _, p := range attrPairRe.FindAllStringSubmatch(tag, -1) {
		v := p[2]
		if v == "" {
			v = p[3]
		}
		m[strings.ToLower(p[1])] = html.UnescapeString(v)
	}
	return m
}

// ParseHead extracts the SEO fields from a page's HTML head.
func ParseHead(page, source string) SEO {
	if i := strings.Index(strings.ToLower(page), "</head>"); i > 0 {
		page = page[:i]
	}
	out := SEO{Source: source}
	if m := titleRe.FindStringSubmatch(page); m != nil {
		out.Title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	for _, tag := range metaTagRe.FindAllString(page, -1) {
		a := attrsOf(tag)
		switch strings.ToLower(a["name"]) {
		case "description":
			out.Description = a["content"]
		case "robots":
			out.NoIndex = strings.Contains(strings.ToLower(a["content"]), "noindex")
		}
		if a["property"] == "og:image" && out.OGImage == "" {
			out.OGImage = a["content"]
		}
	}
	for _, tag := range linkTagRe.FindAllString(page, -1) {
		if canonicalRe.MatchString(tag) {
			out.Canonical = attrsOf(tag)["href"]
		}
	}
	return out
}

func (s *RESTSource) headSEO(ctx context.Context, link, source string) (SEO, error) {
	body, _, err := s.c.Do(ctx, link, 4<<20)
	if err != nil {
		return SEO{}, err
	}
	return ParseHead(string(body), source), nil
}

func (s *RESTSource) fetchAuthor(ctx context.Context, d *Dump, userSlug map[int]string, id int) string {
	var u struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		Link        string `json:"link"`
	}
	if _, err := s.c.JSON(ctx, "/wp/v2/users/"+strconv.Itoa(id), nil, &u); err != nil || u.Slug == "" {
		return ""
	}
	userSlug[id] = u.Slug
	d.Authors = append(d.Authors, Author{Key: u.Slug, Name: html.UnescapeString(u.Name), Slug: u.Slug, Bio: u.Description, Link: u.Link})
	return u.Slug
}

func (s *RESTSource) loadMenus(ctx context.Context, d *Dump) {
	if !s.c.Authed() {
		return
	}
	var menus []struct {
		ID   int      `json:"id"`
		Name string   `json:"name"`
		Slug string   `json:"slug"`
		Locs []string `json:"locations"`
	}
	if _, err := s.c.JSON(ctx, "/wp/v2/menus", url.Values{"per_page": {"100"}}, &menus); err != nil {
		d.Warnings = append(d.Warnings, "could not read menus: "+err.Error())
		return
	}
	for _, m := range menus {
		menu := Menu{ID: strconv.Itoa(m.ID), Name: html.UnescapeString(m.Name), Slug: m.Slug}
		if len(m.Locs) > 0 {
			menu.Location = m.Locs[0]
		}
		err := s.c.Paged(ctx, "/wp/v2/menu-items", url.Values{"menus": {strconv.Itoa(m.ID)}, "orderby": {"menu_order"}, "order": {"asc"}}, s.opts.MaxCollection, func(raw json.RawMessage) error {
			var it struct {
				ID       int      `json:"id"`
				Title    rendered `json:"title"`
				URL      string   `json:"url"`
				Parent   int      `json:"parent"`
				Order    int      `json:"menu_order"`
				Type     string   `json:"type"`
				Object   string   `json:"object"`
				ObjectID int      `json:"object_id"`
				Target   string   `json:"target"`
			}
			if err := json.Unmarshal(raw, &it); err != nil {
				return err
			}
			mi := MenuItem{ID: strconv.Itoa(it.ID), Title: html.UnescapeString(it.Title.Rendered), URL: it.URL, Type: it.Type, Object: it.Object,
				ObjectID: strconv.Itoa(it.ObjectID), NewTab: it.Target == "_blank", Order: it.Order}
			if it.Parent != 0 {
				mi.ParentID = strconv.Itoa(it.Parent)
			}
			menu.Items = append(menu.Items, mi)
			return nil
		})
		if err != nil {
			d.Warnings = append(d.Warnings, fmt.Sprintf("could not read the items of menu %q: %v", m.Name, err))
			continue
		}
		d.Menus = append(d.Menus, menu)
	}
}

// sitemapURLs lists the URLs of the old site's sitemap (an SEO plugin's, the
// WordPress core one, or the one robots.txt names).
func (s *RESTSource) sitemapURLs(ctx context.Context, d *Dump) []string {
	candidates := []string{"/sitemap_index.xml", "/wp-sitemap.xml", "/sitemap.xml"}
	if body, _, err := s.c.Do(ctx, s.c.Base.String()+"/robots.txt", 1<<20); err == nil {
		for _, line := range strings.Split(string(body), "\n") {
			if rest, ok := cutPrefixFold(strings.TrimSpace(line), "sitemap:"); ok {
				candidates = append([]string{strings.TrimSpace(rest)}, candidates...)
			}
		}
	}
	for _, c := range candidates {
		u := c
		if strings.HasPrefix(c, "/") {
			u = s.c.Base.String() + c
		}
		urls, err := s.readSitemap(ctx, u, 0, &sitemapBudget{maps: 200, urls: 200000})
		if err == nil && len(urls) > 0 {
			return urls
		}
	}
	d.Warnings = append(d.Warnings, "no sitemap found: the URL audit uses the links of the imported items")
	return nil
}

type sitemapBudget struct{ maps, urls int }

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// readSitemap reads a sitemap or sitemap index, following indexes.
func (s *RESTSource) readSitemap(ctx context.Context, u string, depth int, b *sitemapBudget) ([]string, error) {
	if depth > 3 || b.maps <= 0 || b.urls <= 0 {
		return nil, nil
	}
	b.maps--
	body, _, err := s.c.Do(ctx, u, 50<<20)
	if err != nil {
		return nil, err
	}
	kind, locs, err := ParseSitemap(body, b.urls)
	if err != nil {
		return nil, err
	}
	b.urls -= len(locs)
	if kind == "index" {
		var all []string
		for _, l := range locs {
			sub, err := s.readSitemap(ctx, l, depth+1, b)
			if err != nil {
				var he *HTTPError
				if errors.As(err, &he) && he.Status == http.StatusNotFound {
					continue
				}
				return nil, err
			}
			all = append(all, sub...)
		}
		return all, nil
	}
	return locs, nil
}

// ParseSitemap returns "index" or "urlset" and the <loc> of each entry, reading
// the XML as a stream and stopping at limit entries. A limit of zero or less
// reads no entries (it is a budget that is used up, never "unlimited").
func ParseSitemap(data []byte, limit int) (kind string, locs []string, err error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if limit <= 0 {
		return "", nil, nil
	}
	var stack []string
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return kind, locs, nil
			}
			return kind, locs, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// Only <loc> directly under <url> or <sitemap>: image extensions
			// carry their own <image:loc> inside <image:image>.
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch {
			case t.Name.Local == "sitemapindex":
				kind = "index"
			case t.Name.Local == "urlset":
				kind = "urlset"
			case t.Name.Local == "loc" && (parent == "url" || parent == "sitemap"):
				var v string
				if err := dec.DecodeElement(&v, &t); err != nil {
					return kind, locs, err
				}
				locs = append(locs, strings.TrimSpace(v))
				if len(locs) >= limit {
					return kind, locs, nil
				}
				continue
			}
			if len(stack) >= 16 {
				return kind, locs, fmt.Errorf("sitemap nests more than 16 levels deep")
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

func ownAvatar(avatars map[string]string, site string) string {
	best, bestN := "", -1
	for k, v := range avatars {
		n, _ := strconv.Atoi(k)
		if strings.HasPrefix(v, site) && n > bestN {
			best, bestN = v, n
		}
	}
	return best
}

func intList(raw json.RawMessage) []int {
	var out []int
	_ = json.Unmarshal(raw, &out)
	return out
}

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

func stripTags(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func appendUnique(list []string, v string) []string {
	if hasString(list, v) {
		return list
	}
	return append(list, v)
}

func hasString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// inferStructure finds the permalink structure the published posts share.
func inferStructure(items []Item) string {
	counts := map[string]int{}
	n := 0
	for _, it := range items {
		if it.Type != "post" || it.Status != wpPublish || it.Link == "" {
			continue
		}
		if s := InferPermalink(it.Link, it.Slug, it.ID, it.Date); s != "" {
			counts[s]++
		}
		if n++; n >= 50 {
			break
		}
	}
	best, bestN := "", 0
	for s, c := range counts {
		if c > bestN || (c == bestN && s < best) {
			best, bestN = s, c
		}
	}
	return best
}
