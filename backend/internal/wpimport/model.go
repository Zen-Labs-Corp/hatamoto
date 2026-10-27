// Package wpimport imports a WordPress site into Hatamoto from its REST API or
// a WXR export: scan report, runner (settings, terms, authors, media, pages,
// posts, custom post types, menus, redirects, link rewrite) and URL audit.
// See docs/IMPORT-WORDPRESS.md.
package wpimport

import "time"

// SourceName is the import source label stored in runs, the id map and entity metadata.
const SourceName = "wordpress"

// Item statuses as WordPress names them.
const (
	wpPublish = "publish"
	wpFuture  = "future"
	wpDraft   = "draft"
	wpPending = "pending"
	wpPrivate = "private"
	wpTrash   = "trash"
)

// Dump is everything read from the source, in one neutral shape. Both sources
// (REST and WXR) fill it; the scan report and the runner only read it.
type Dump struct {
	Site    Site
	Authors []Author
	Terms   []Term
	Media   []Media
	Items   []Item
	Menus   []Menu
	// OldURLs are the public URLs the old site lists in its sitemap (REST), or
	// the links of its published items (WXR); the audit checks each.
	OldURLs []string
	// Detect holds what the scan found out about plugins and builders.
	Detect Detection
	// RawBodies is true when item bodies are as typed (WXR): block comments,
	// unexpanded shortcodes, paragraphs not yet wrapped.
	RawBodies bool
	// Warnings are non-fatal problems met while reading.
	Warnings []string
}

// Site is the site-level data.
type Site struct {
	URL         string // home URL, no trailing slash
	Title       string
	Tagline     string
	Language    string // BCP 47-ish ("en-US"); empty when unknown
	Timezone    string
	ShowOnFront string // "posts" or "page"
	FrontPageID int
	PostsPageID int
	// Permalink is the WordPress structure, for example "/%year%/%monthnum%/%postname%/",
	// inferred from the links of posts ("" when unknown).
	Permalink string
}

// Author is a WordPress user shown as a byline.
type Author struct {
	Key       string // login or slug: what items refer to
	Name      string
	Slug      string
	Bio       string
	Link      string // old author archive URL
	AvatarURL string // only set for avatars hosted on the site itself
}

// Term is a category, tag or custom taxonomy term.
type Term struct {
	Taxonomy    string // "category", "post_tag" or custom
	Slug        string
	Name        string
	Description string
	ParentSlug  string
	Link        string
}

// Media is an attachment of the media library.
type Media struct {
	ID      string
	URL     string
	Sizes   []string // URLs of generated sizes, mapped to the same media
	Mime    string
	Title   string
	Alt     string
	Caption string
	Date    time.Time
}

// TermRef points at a term of an item.
type TermRef struct{ Taxonomy, Slug string }

// Item is a post, page or custom post type entry.
type Item struct {
	ID       string
	Type     string // post, page or a custom post type slug
	Status   string // WordPress status
	Slug     string
	Title    string
	Content  string // HTML as the reader sees it (REST) or raw (WXR)
	Raw      string // raw content with block comments and shortcodes, when known
	Excerpt  string
	Link     string // old public URL
	Date     time.Time
	Modified time.Time
	ParentID string
	// AuthorKey refers to Author.Key.
	AuthorKey string
	// FeaturedMedia is a Media.ID.
	FeaturedMedia string
	Terms         []TermRef
	Protected     bool   // password protected
	Lang          string // language tag as the source gave it (WPML or Polylang); empty when it gave none
	SEO           SEO
	Meta          map[string]string
}

// SEO is the search metadata the SEO plugin held for an item.
type SEO struct {
	Title       string
	Description string
	Canonical   string
	OGImage     string
	NoIndex     bool
	Source      string // yoast, rankmath, seopress, aioseo, html
}

// Menu is a navigation menu.
type Menu struct {
	ID       string
	Name     string
	Slug     string
	Location string
	Items    []MenuItem
}

// MenuItem is one entry of a menu.
type MenuItem struct {
	ID       string
	ParentID string
	Title    string
	URL      string
	Type     string // post_type, taxonomy or custom
	Object   string // page, post, category, ...
	ObjectID string // item id, or "taxonomy:slug" for terms
	NewTab   bool
	Order    int
}

// Detection is what the scan found about the old site's plugins.
type Detection struct {
	SEOPlugin     string
	PageBuilders  []string
	Forms         []string
	Multilingual  []string
	DefaultLang   string
	OtherPlugins  []string // REST namespaces or plugin markers nothing maps
	Shortcodes    map[string]int
	Namespaces    []string
	CustomTypes   []CustomType
	PermalinkNote string
}

// CustomType is a public custom post type of the source.
type CustomType struct {
	Slug         string
	Name         string
	RESTBase     string
	Hierarchical bool
	HasArchive   bool
}
