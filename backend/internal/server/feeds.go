package server

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

const (
	// feedPostSchema is the schema whose published entities make up the feeds.
	feedPostSchema = "post"
	// feedItemLimit is how many newest posts a feed lists.
	feedItemLimit    = 20
	feedSummaryRunes = 300
)

// feedMeta describes the feed channel; feedItem one post. URLs are absolute.
type feedMeta struct {
	Title, Description, Language string
	Home, RSSURL, AtomURL        string
	Updated                      time.Time
}

type feedItem struct {
	Title, URL, ID, Summary, ContentHTML, Author string
	Published, Updated                           time.Time
}

// handleFeed serves /feed.xml (RSS 2.0) and /atom.xml (Atom 1.0): the newest
// published posts with their sanitised bodies. 404 when the site has no post
// schema.
func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := s.publicBaseURL(r)
	// Under the render: prefix, so every content or settings change drops it.
	key := cache.RenderedPageKey("feed:"+base, r.URL.Path)
	ctype := "application/rss+xml; charset=utf-8"
	if r.URL.Path == "/atom.xml" {
		ctype = "application/atom+xml; charset=utf-8"
	}
	if s.infra != nil && s.infra.Cache != nil {
		if body, err := s.infra.Cache.Get(ctx, key); err == nil && body != nil {
			writeFeed(w, ctype, body)
			return
		}
	}
	snap := s.urlSnapshot(ctx)
	if _, ok := snap.Config(feedPostSchema); !ok {
		http.NotFound(w, r)
		return
	}
	site, err := s.buildSiteView(ctx)
	if err != nil {
		s.logger.Error("feed: site config", "error", err)
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	slugs := []string{feedPostSchema}
	if sl := snap.Router().SchemaSlugs(feedPostSchema); len(sl) > 0 {
		slugs = sl
	}
	pubRepo := entity.NewPostgresPublicRepository(s.db)
	posts, err := pubRepo.ListPublishedBySchemasNoCount(ctx, slugs, feedItemLimit)
	if err != nil {
		s.logger.Error("feed: list posts", "error", err)
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	locate := relatedLocator(ctx, snap, pubRepo, feedPostSchema)
	links := s.resolveEntityLinks(ctx, linkIDs(feedContents(posts)...))
	meta := feedMeta{
		Title: site.Name, Description: site.Tagline, Language: site.Language,
		Home: base + "/", RSSURL: base + "/feed.xml", AtomURL: base + "/atom.xml", Updated: time.Now(),
	}
	ids := make([]uuid.UUID, len(posts))
	for i, e := range posts {
		ids[i] = e.ID
	}
	items := feedItems(posts, base, locate, links, s.bylinesOf(ctx, ids...).creditedNames(ids))
	body := buildRSS(meta, items)
	if r.URL.Path == "/atom.xml" {
		body = buildAtom(meta, items)
	}
	if s.infra != nil && s.infra.Cache != nil {
		_ = s.infra.Cache.Set(ctx, key, body, time.Hour)
	}
	writeFeed(w, ctype, body)
}

func writeFeed(w http.ResponseWriter, ctype string, body []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=300, s-maxage=300")
	_, _ = w.Write(body)
}

// feedItems maps published posts to feed items. Posts without a public URL are
// left out. Bodies get absolute links so they read correctly in a feed reader.
// credited are the names of the person or organisation a post credits (never an
// email address); a post credited to nobody keeps its plain-text byline.
func feedItems(posts []*entity.Entity, base string, locate func(*entity.Entity) string, links map[string]string, credited map[uuid.UUID]string) []feedItem {
	var out []feedItem
	for _, e := range posts {
		if e == nil || e.Status != entity.StatusPublished || entityNoIndex(e) {
			continue
		}
		path := locate(e)
		if path == "" {
			continue
		}
		attrs := attrsForView(e.Attributes)
		content := render.RewriteEntityLinks(stripInvalidXML(entity.SanitizeRichText(strAttr(attrs, "content"))), links)
		it := feedItem{
			Title:       firstNonEmpty(strAttr(attrs, "title"), e.Name),
			URL:         base + path,
			ContentHTML: absolutizeHTML(content, base),
			ID:          "urn:uuid:" + e.ID.String(),
			Author:      firstNonEmpty(credited[e.ID], firstNonEmpty(strAttr(attrs, "author"), strAttr(attrs, "author_name"))),
			Updated:     e.UpdatedAt,
		}
		it.Summary = firstNonEmpty(truncateRunes(strAttr(attrs, "excerpt"), feedSummaryRunes), plainText(content, feedSummaryRunes))
		if e.PublishedAt != nil {
			it.Published = *e.PublishedAt
		}
		if it.Published.IsZero() {
			it.Published = e.CreatedAt
		}
		out = append(out, it)
	}
	return out
}

// entityNoIndex mirrors entity.NoIndexSQL: noindex posts stay out of feeds as
// they do out of sitemaps and search.
func entityNoIndex(e *entity.Entity) bool {
	var m struct {
		NoIndex bool `json:"no_index"`
	}
	_ = json.Unmarshal(e.Metadata, &m)
	return m.NoIndex || attrsNoIndex(e.Attributes)
}

// stripInvalidXML drops runes XML 1.0 forbids (e.g. a pasted \v): CDATA, unlike
// chardata, is not escaped by encoding/xml, so one would invalidate the feed.
func stripInvalidXML(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF && !(r >= 0xD800 && r <= 0xDFFF)) {
			return r
		}
		return -1
	}, s)
}

// absolutizeHTML makes root-relative href and src attributes absolute. All
// other markup is copied unchanged.
func absolutizeHTML(markup, base string) string {
	if !strings.Contains(markup, `="/`) && !strings.Contains(markup, `='/`) {
		return markup
	}
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(markup))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return b.String()
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			b.Write(z.Raw())
			continue
		}
		raw := append([]byte(nil), z.Raw()...)
		tok := z.Token()
		changed := false
		for i, a := range tok.Attr {
			if (a.Key == "href" || a.Key == "src") && strings.HasPrefix(a.Val, "/") && !strings.HasPrefix(a.Val, "//") {
				tok.Attr[i].Val, changed = base+a.Val, true
			}
		}
		if changed {
			b.WriteString(tok.String())
		} else {
			b.Write(raw)
		}
	}
}

func rfc1123(t time.Time) string { return t.UTC().Format(time.RFC1123Z) }
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// latest is the newest update time across the items, or zero.
func latest(items []feedItem) time.Time {
	var t time.Time
	for _, it := range items {
		if it.Updated.After(t) {
			t = it.Updated
		}
		if it.Published.After(t) {
			t = it.Published
		}
	}
	return t
}

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	Content string     `xml:"xmlns:content,attr"`
	DC      string     `xml:"xmlns:dc,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language,omitempty"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Self          atomLink  `xml:"atom:link"`
	Items         []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	PubDate     string   `xml:"pubDate,omitempty"`
	Author      string   `xml:"dc:creator,omitempty"`
	Description string   `xml:"description,omitempty"`
	Encoded     rssCDATA `xml:"content:encoded"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssCDATA struct {
	Text string `xml:",cdata"`
}

// buildRSS renders an RSS 2.0 document.
func buildRSS(m feedMeta, items []feedItem) []byte {
	doc := rssDoc{
		Version: "2.0",
		Atom:    "http://www.w3.org/2005/Atom",
		Content: "http://purl.org/rss/1.0/modules/content/",
		DC:      "http://purl.org/dc/elements/1.1/",
		Channel: rssChannel{
			Title: m.Title, Link: m.Home, Description: firstNonEmpty(m.Description, m.Title), Language: m.Language,
			Self: atomLink{Href: m.RSSURL, Rel: "self", Type: "application/rss+xml"},
		},
	}
	if t := latest(items); !t.IsZero() {
		doc.Channel.LastBuildDate = rfc1123(t)
	}
	for _, it := range items {
		ri := rssItem{
			Title: it.Title, Link: it.URL, GUID: rssGUID{Value: it.ID},
			Author: it.Author, Description: it.Summary, Encoded: rssCDATA{it.ContentHTML},
		}
		if !it.Published.IsZero() {
			ri.PubDate = rfc1123(it.Published)
		}
		doc.Channel.Items = append(doc.Channel.Items, ri)
	}
	return marshalFeed(doc)
}

type atomDoc struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Lang     string      `xml:"xml:lang,attr,omitempty"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle,omitempty"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Author   atomName    `xml:"author"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string    `xml:"title"`
	ID        string    `xml:"id"`
	Link      atomLink  `xml:"link"`
	Published string    `xml:"published,omitempty"`
	Updated   string    `xml:"updated"`
	Author    *atomName `xml:"author"`
	Summary   string    `xml:"summary,omitempty"`
	Content   atomHTML  `xml:"content"`
}

type atomName struct {
	Name string `xml:"name"`
}

type atomHTML struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}

// buildAtom renders an Atom 1.0 document.
func buildAtom(m feedMeta, items []feedItem) []byte {
	updated := firstTime(latest(items), m.Updated)
	doc := atomDoc{
		Lang: m.Language, Title: m.Title, Subtitle: m.Description, ID: m.Home, Updated: rfc3339(updated), Author: atomName{Name: m.Title},
		Links: []atomLink{
			{Href: m.AtomURL, Rel: "self", Type: "application/atom+xml"},
			{Href: m.Home, Rel: "alternate", Type: "text/html"},
		},
	}
	for _, it := range items {
		e := atomEntry{
			Title: it.Title, ID: it.ID, Link: atomLink{Href: it.URL, Rel: "alternate", Type: "text/html"},
			Updated: rfc3339(firstTime(it.Updated, it.Published)), Summary: it.Summary,
			Content: atomHTML{Type: "html", Text: it.ContentHTML},
		}
		if !it.Published.IsZero() {
			e.Published = rfc3339(it.Published)
		}
		if it.Author != "" {
			e.Author = &atomName{Name: it.Author}
		}
		doc.Entries = append(doc.Entries, e)
	}
	return marshalFeed(doc)
}

func firstTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Unix(0, 0)
}

func marshalFeed(doc any) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", "  ")
	_ = enc.Encode(doc)
	b.WriteByte('\n')
	return b.Bytes()
}

// feedContents returns the post bodies, for collecting their internal links.
func feedContents(posts []*entity.Entity) []any {
	var out []any
	for _, e := range posts {
		if e != nil {
			out = append(out, strAttr(attrsForView(e.Attributes), "content"))
		}
	}
	return out
}
