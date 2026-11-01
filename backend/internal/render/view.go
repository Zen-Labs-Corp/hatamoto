package render

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/seo"
)

// PageView is everything a layout needs to render one page. The server builds
// it from stored entities; the render package never touches the database.
type PageView struct {
	// Ctx is the request context, used only so render failures are logged with
	// it (the smoke check attributes errors by it). Nil means no request.
	Ctx context.Context
	// Kind selects the template hierarchy (see layoutCandidates). Empty means
	// KindSingle for an entity page (EntityID set) and KindPage otherwise.
	Kind string
	// Entity is the entity behind a single or home view; nil elsewhere.
	Entity *EntityView
	// Items and Pagination are set on archive, taxonomy, author and search views.
	Items      []RelatedItem
	Pagination *Pagination
	// Term is set on taxonomy views, Author on author views, Query on search views.
	Term   *TermView
	Author *AuthorView
	Query  string
	// Registry is set on registry detail views (KindRegistry).
	Registry *RegistryPageView
	// SearchType is the schema slug a search is limited to ("" for the whole site).
	SearchType string
	// Dynamic marks a view built from per-request data (taxonomy, author,
	// search) that no entity event invalidates: the server never caches it.
	Dynamic bool
	Site    SiteView
	Page    PageMeta
	// Locale is the locale tag of the page being rendered ("" on a one-locale
	// site).
	Locale string
	// Segment is the stored URL segment of Locale ("" when unprefixed or on a
	// one-locale site).
	Segment string
	// Alternates lists the page in every configured locale, in settings order.
	// It is nil on a one-locale site. XDefaultHref is the absolute URL for the
	// hreflang x-default entry ("" for none).
	Alternates   []Alternate
	XDefaultHref string
	// Business is the resolved local business of the page's locale, printed as
	// a LocalBusiness node in the site graph. Nil on shared pages, on one-locale
	// sites and for locales without an office of their own.
	Business *BusinessView
	// GeoRegion is the region of the page's locale, printed as a geo.region
	// meta tag. It is set whether or not the locale has an office, and empty
	// on shared pages and one-locale sites.
	GeoRegion string
	// SEO holds the site-wide title and description templates. Nil keeps the
	// built-in "<title> | <site>" head.
	SEO      *seo.Settings
	Sections []Section
	// Doc is a page_doc v2 tree. When set it replaces Sections: v1-shaped nodes
	// render through the theme section partial, the rest natively.
	Doc *builder.Doc
	// anchors are the element anchors the current render has written.
	anchors    *anchorSet
	BaseURL    string
	Path       string
	Breadcrumb []Crumb
	// JSONLD holds backend-built JSON-LD documents, emitted verbatim (escaped).
	JSONLD []json.RawMessage
	// AICrawlerComment is an optional HTML comment body for AI crawlers.
	AICrawlerComment string
	EntityID         string
	SchemaSlug       string
	// Attrs is a read-only copy of the entity's attributes (page_sections
	// omitted; it is in Sections). Numbers are float64. Entity pages only. Treat strings as
	// plain text: html/template escapes them; use sanitisedHTML for rich text.
	Attrs map[string]any
	// EntityLinks maps the data-entity-id of internal links in the page content to
	// the target's current public path. RewriteEntityLinks applies it at render so
	// links survive slug changes; ids missing from the map keep their stored href.
	EntityLinks map[string]string
	// PageNum is the request's ?page= (1..MaxQueryPage, 0 reads as 1); the
	// pageNum func reads it on every view.
	PageNum int
	// Params is the request's query string; the queryParam func reads it on
	// every view. Pages with a query string are never page-cached.
	Params url.Values
	// Related lists up to limit (clamped to 1..12) other published entities of
	// the page's schema, newest first, excluding the page itself. Lazy: the
	// query only runs when a layout calls it ({{range call .Related 3}}). Never
	// nil in a layout (a no-op returning nil on archive, 404, error and
	// unsaved-preview views, and when nothing is found).
	Related func(limit int) []RelatedItem
	// Series returns the editorial series the entity belongs to
	// (attributes.series_slug): its published and scheduled parts in part
	// order, or nil when the entity has no series or it has no parts. Lazy and memoised per
	// render ({{with call .Series}}...{{end}}). Never nil in a layout.
	Series func() *SeriesView
	// PostsByAuthor lists a page of the published posts written by this
	// team-member entity, newest first. limit is clamped to 1..50 and page to
	// >= 1 (pass .PageNum). Lazy and memoised per (limit, page). Never nil in a
	// layout: a zero PostsPage on any other entity.
	PostsByAuthor func(limit, page int) PostsPage
	// Uncacheable, when non-nil, is set during rendering if a lazy data source
	// (Related) failed, so the caller serves the page but does not cache it.
	Uncacheable *bool
	// PagedLoop and LoopOutOfRange, when non-nil, are set during rendering when
	// the page has a paged builder loop and when the requested ?page= is past
	// its last page. The server caches ?page=N only for the former and answers
	// 404 for the latter.
	PagedLoop, LoopOutOfRange *bool
	// PublishedAt and UpdatedAt are zero when unknown.
	PublishedAt time.Time
	UpdatedAt   time.Time
	// Forms is keyed by form slug, filled for each contact_form section.
	Forms map[string]FormView
	// PluginDirectory feeds plugin_directory sections; nil when the page has
	// none.
	PluginDirectory *PluginDirectoryView
	// ThemeDirectory feeds theme_directory sections; nil when the page has
	// none.
	ThemeDirectory *ThemeDirectoryView
	// MaxCacheAge, when > 0, caps how long the rendered page may be cached,
	// whatever the configured page cache TTL.
	MaxCacheAge time.Duration
	// Preview marks an unsaved-draft render (noindex, data-hm-preview).
	Preview bool
	// Canvas marks an admin canvas render: html-kind bodies carry data-hm-text
	// editor markers (see canvas_text.go).
	Canvas bool
	// NoParts turns the theme parts off for this render: the canvas of a part
	// itself, so the assigned part is not drawn twice.
	NoParts bool
	// Submitted is set after a no-JS form post redirect (?submitted=1).
	Submitted bool
	// FormError is set after a failed no-JS form post redirect (?form_error=1).
	FormError bool

	// scope is the loop item being rendered (bindings "loop.item.*"); nil
	// outside a loop. Render is single-goroutine per request.
	scope *bindScope
	// loopPaged is the first paged loop of this render (see widget_loop.go).
	loopPaged *loopPaged
	// loops counts the loops started in this render, parts and refs included.
	loops int
	// ext is the external (plugin) data of this render; nil until a source is asked for.
	ext *extState
}

// Loops and SetLoops read and restore the render's loop counter. The canvas
// renders a nested loop a second time and must count it where the first pass did.
func (v *PageView) Loops() int     { return v.loops }
func (v *PageView) SetLoops(n int) { v.loops = n }

// RelatedItem is the whitelisted view of a listed entity (recent, related,
// query and archive/taxonomy/author/search items).
type RelatedItem struct {
	Title, URL, Excerpt, Image, Category string
	// Name is the entity's own name (Title is attributes.title, else Name).
	Name string
	// Schema is the slug of the entity's schema.
	Schema      string
	PublishedAt time.Time
	// UpdatedAt is zero for scheduled items.
	UpdatedAt time.Time
	// Author and AuthorURL are empty when the entity has no named author.
	Author, AuthorURL string
	// WordCount is the number of words of the entity's attributes.content and
	// ReadingTime the minutes to read it, both as the readingTime func counts
	// (0 when there is no content).
	WordCount, ReadingTime int
	// Attrs is a read-only copy of the entity's attributes without
	// page_sections and content (the body would make every card heavy). Numbers
	// are float64; strings are plain text that html/template escapes.
	Attrs map[string]any
}

// SeriesView is an editorial series, as GET /api/v1/public/series/{slug} served
// it. Scheduled parts are listed (Published false, empty URL) so an index can
// show what is coming; only published parts carry a URL, so a link can never
// point at a post that is not live.
type SeriesView struct {
	Slug, Name string
	// Total is the number of numbered parts (the index page is not one).
	Total    int
	IndexURL string
	// Parts is ordered by Part; the index page has Part 0 and Index true.
	// IndexURL is empty until the index page is published.
	Parts []SeriesPartView
}

// SeriesPartView is one published post in a series.
type SeriesPartView struct {
	Title, URL  string
	Part        int
	Index       bool
	Published   bool
	PublishDate time.Time
}

// PostsPage is one page of PostsByAuthor.
type PostsPage struct {
	Items []RelatedItem
	// Total counts every matching post; Page is the page served; Pages is the
	// page count (at least 1).
	Total, Page, Pages int
}

// Template kinds. A view's Kind picks the layout (see layoutCandidates).
const (
	KindPage     = "page"
	KindSingle   = "single"
	KindHome     = "home"
	KindArchive  = "archive"
	KindTaxonomy = "taxonomy"
	KindAuthor   = "author"
	KindSearch   = "search"
	// KindNotFound is set by RenderNotFound so the head uses the 404 title template.
	KindNotFound = "notfound"
	// KindRegistry is a plugin or theme detail page built from the verified
	// registry index (see PageView.Registry).
	KindRegistry = "registry"
)

// EntityView is the whitelisted view of the entity a page shows.
type EntityView struct {
	ID, Title, Slug, URL, Schema string
	// Excerpt and Image are the first of the common attribute spellings.
	Excerpt, Image string
	// Attributes is a read-only copy of the stored attributes (page_sections omitted).
	Attributes  map[string]any
	Terms       []TermView
	Author      *AuthorView
	PublishedAt time.Time
	UpdatedAt   time.Time
}

// Categories are the entity's terms of kind "category".
func (e *EntityView) Categories() []TermView { return e.termsOfKind("category") }

// Tags are the entity's terms of kind "tag".
func (e *EntityView) Tags() []TermView { return e.termsOfKind("tag") }

func (e *EntityView) termsOfKind(kind string) []TermView {
	var out []TermView
	for _, t := range e.Terms {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

// TermView is a taxonomy term (kind "category", "tag" or a custom kind). Count
// is 0 unless a listing fills it. Parent is the slug of the parent term ("" for
// a root) and Description its optional text; URL is the term's archive path,
// which for a nested term runs through its ancestors' slugs.
type TermView struct {
	Kind, Slug, Name, URL string
	Count                 int
	Description, Parent   string
}

// AuthorView is a public author: name, avatar and archive URL only (never an
// email address).
type AuthorView struct {
	Name, Slug, URL, Avatar string
	// Bio is the author's short biography ("" when none).
	Bio string
	// JobTitle is the person's role ("" when none).
	JobTitle string
	// IsOrg is true for a byline that credits the site organisation: Name is
	// the site name and there is no URL.
	IsOrg bool
	// SameAs are the person's public profile links (website, LinkedIn, ...).
	SameAs []string
	// Links are the same links with a visible label ("LinkedIn", "X", ...).
	Links []AuthorLink
}

// AuthorLink is a profile link and the name shown for it.
type AuthorLink struct{ Label, URL string }

// Pagination describes one page of a listing. URLs are root-relative and keep
// the other query parameters (?page=N, page 1 has no parameter).
type Pagination struct {
	Page, PerPage, Total, TotalPages int
	HasPrev, HasNext                 bool
	PrevURL, NextURL                 string
	// Path and Query (the other, already encoded parameters) build page URLs.
	Path  string
	Query string
}

// DataSource feeds the recent, terms, menu and srcset template funcs. It
// takes no context: the implementation owns its timeouts and is fail-open
// (an error yields no items). A nil DataSource yields no data.
type DataSource interface {
	Recent(schema string, limit int) []RelatedItem
	Terms(kind string, limit int) []TermView
	Menu(location string) []NavItem
	// ImageVariants returns the resized copies of a local media URL, widest last.
	ImageVariants(url string) []ImageVariant
	// Query lists published entities matching q (see QuerySpec); the spec the
	// funcs pass is already clamped.
	Query(q QuerySpec) QueryResult
	// AttrValues lists the distinct non-empty string values of an attribute of
	// a schema's published entities, sorted by value.
	AttrValues(schema, attr string, limit int) []AttrCount
}

// VariantPrefetcher is an optional DataSource extension: PrefetchVariants
// loads the image variants of urls in one lookup, so the ImageVariants calls
// that follow are served from the cache. Fail-open, no result.
type VariantPrefetcher interface {
	PrefetchVariants(urls []string)
}

// ImageSizer is an optional DataSource extension: ImageSize returns the
// intrinsic pixel size of a local media URL, which an image writes as its
// width and height attributes so the browser reserves the space (no layout
// shift). ok is false for unknown, external or size-less files; the image then
// has no size attributes.
type ImageSizer interface {
	ImageSize(url string) (w, h int, ok bool)
}

// Limits of the query func.
const (
	// DefaultQueryLimit is the page size when a query gives none.
	DefaultQueryLimit = 12
	// MaxQueryLimit caps a query's limit.
	MaxQueryLimit = 100
	// MaxQueryPage caps a query's page (and PageNum).
	MaxQueryPage = 1000
)

// QuerySpec is what the query func asks of a DataSource. Every field but
// Schema is optional ("" = no filter).
type QuerySpec struct {
	// Schema is the schema slug (aliases are included by the implementation).
	Schema string
	// Attr and Eq filter on attributes[Attr] == Eq; Attr alone requires the
	// attribute to exist. EqSet says Eq was given (an empty Eq then matches
	// the empty string, not "exists").
	Attr, Eq string
	EqSet    bool
	// IncludeScheduled adds scheduled entities (their date, no URL).
	IncludeScheduled bool
	// Author is an author slug, Term a "<kind>:<slug>" taxonomy term. A slug
	// that names nothing gives no items.
	Author, Term string
	// Order is "-published_at" (default), "published_at", "name", "-name",
	// "attr:<key>" or "-attr:<key>".
	Order string
	// Limit is 1..MaxQueryLimit and Page 1..MaxQueryPage.
	Limit, Page int
	// Offset skips this many items before the first page (0..1000).
	Offset int
	// ExcludeID is the uuid (text) of an entity to leave out; an invalid
	// value is ignored.
	ExcludeID string
}

// QueryResult is one page of a query. Pages is at least 1; Page is clamped to
// Pages. Items is empty (never an error) when nothing matches or a lookup
// failed.
type QueryResult struct {
	Items                     []RelatedItem
	Total, Page, Pages, Limit int
}

// AttrCount is a distinct attribute value and the number of published
// entities that have it.
type AttrCount struct {
	Value string
	Count int
}

// ImageVariant is one resized copy of an image.
type ImageVariant struct {
	URL   string
	Width int
}

// Section is one stored page section, as in attributes.page_sections[].
type Section struct {
	ID        string
	Type      string
	Title     string
	Content   map[string]any
	IsVisible *bool
	SortOrder int
}

// SiteView is the site-wide data shared by every page.
type SiteView struct {
	Name        string
	Tagline     string
	LogoURL     string
	Nav         []NavItem
	FooterNav   []NavItem
	SocialLinks []Link
	// HeaderCTA is the optional header button; nil renders none.
	HeaderCTA *Link
	// Feeds are the site's syndication feeds, advertised in the head as
	// <link rel="alternate">. Empty when the site has none.
	Feeds  []FeedLink
	Tokens ThemeTokens
	// Language is the BCP 47 site language (default "en" when unset).
	Language string
	// Phone, Hours and Address are the public contact details from the site
	// settings; empty when unset. They also feed the Organization JSON-LD.
	Phone, Hours string
	Address      Address
	// Org holds the site-wide business details that only the Organization
	// JSON-LD node uses. The zero value adds nothing to the node.
	Org OrgView
	// Settings are the site's theme settings (theme_config extras), read-only.
	Settings map[string]any
	// Design is the site design system (tokens and style presets) read from the
	// theme settings; the zero value means none.
	Design builder.Design
}

// OrgView is the optional site-wide detail of the Organization node. Every
// field prints only when set.
type OrgView struct {
	LegalName, TaxID, Fax, Email string
	// POBox prints in the Organization's postal address.
	POBox string
	// SameAs are extra profile URLs, added after the site's social links.
	SameAs []string
	Rating Rating
}

// Rating is an aggregate rating as the owner states it; Count may be empty.
type Rating struct {
	Value, Count string
}

// BusinessView is the resolved local business of one locale. Plain data: the
// server fills it, the renderer never reads site settings for it.
type BusinessView struct {
	// ID is the suffix of the node's @id (the locale's URL segment).
	ID string
	// Type is the schema.org type; empty means LocalBusiness.
	Type string
	// Name overrides the site name for this node; empty uses the site name.
	Name                            string
	Phone, Fax, Email, Hours        string
	TaxID                           string
	Address                         Address
	POBox, Lat, Lng                 string
	MapURL, GBPURL                  string
	ContactPoints                   []ContactPointView
	OpeningHours                    []HoursView
	SpecialHours                    []SpecialHoursView
	AreaServed                      []string
	Currency, PriceRange, GeoRegion string
	SameAs                          []string
	Rating                          Rating
}

// ContactPointView is one department or channel of a business.
type ContactPointView struct {
	Type, Phone, Email string
}

// HoursView is one block of regular opening hours (opens and closes are HH:MM).
type HoursView struct {
	Days          []string
	Opens, Closes string
}

// SpecialHoursView is a dated exception. From and To are YYYY-MM-DD (To may be
// empty); a Closed entry has no hours.
type SpecialHoursView struct {
	From, To      string
	Closed        bool
	Opens, Closes string
}

// Address is the business postal address of the site settings.
type Address struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// ThemeTokens are the site theme_config values mapped onto --hm-* variables.
// Empty values fall back to the theme's own defaults.
type ThemeTokens struct {
	Primary, Background, Text    string
	Font, FontHeading            string
	RadiusSm, RadiusMd, RadiusLg string
}

// NavItem is a navigation entry, optionally with children.
//
// Href is "#" for an item without a link and NoLink is set, so a theme that
// ignores NoLink keeps today's behaviour. Class already joins the item's own
// classes with the theme's style, colour and show-on classes. Badge is the
// badge text and BadgeClass the theme's class for it; Icon is a preset id the
// theme declares; Show is "desktop", "mobile" or empty (always).
type NavItem struct {
	Label      string
	Href       string
	Target     string
	Children   []NavItem
	Class      string
	ID         string
	Title      string
	AriaLabel  string
	Rel        string
	Badge      string
	BadgeClass string
	Icon       string
	Show       string
	NoLink     bool
}

// Link is a labelled URL.
type Link struct {
	Label string
	URL   string
}

// PageMeta is the SEO metadata of the page being rendered.
type PageMeta struct {
	Name         string
	Title        string
	Description  string
	OGImage      string
	OGType       string
	CanonicalURL string
	NoIndex      bool
	// TitleSet and DescriptionSet say an editor wrote the meta title or
	// description on the entry. Title and Description merge in fallbacks, so
	// without these flags a written value cannot be told from a fallback; the
	// SEO templates only fill in values that were not written.
	TitleSet, DescriptionSet bool
	// OGTitle and OGDescription are the social title and description an editor
	// wrote on the entry. They win over the social templates and the meta
	// description; empty means unset.
	OGTitle, OGDescription string
}

// Alternate is one locale version of the current page. It is the contract
// between the server and the theme's language switcher and hreflang head.
// Href is absolute. Lang is the BCP 47 tag written to hreflang. Current marks
// the page being rendered. Missing marks a locale with no copy of this page:
// Href is then that locale's home, and no hreflang is emitted for it. Default
// marks the site's default locale.
type Alternate struct {
	Locale  string
	Lang    string
	Href    string
	Current bool
	Missing bool
	Default bool
}

// Crumb is one breadcrumb step.
type Crumb struct {
	Name string
	URL  string
}

// Plugin directory states.
const (
	PluginDirectoryOK          = "ok"
	PluginDirectoryEmpty       = "empty"
	PluginDirectoryUnavailable = "unavailable"
	PluginDirectoryOff         = "off" // no registry is configured
)

// PluginDirectoryView is the verified registry's plugin list for
// plugin_directory sections. State is one of the PluginDirectory* constants.
type PluginDirectoryView struct {
	State string
	Cards []PluginCard
	// Linked is set when the page serves the detail pages (/plugins); card
	// titles link to them only then.
	Linked bool
}

// ThemeDirectoryView is the verified registry's theme list for
// theme_directory sections. State is one of the PluginDirectory* constants.
type ThemeDirectoryView struct {
	State string
	Cards []ThemeCard
	// Linked is set when the page serves the detail pages (/themes); card
	// titles link to them only then.
	Linked bool
}

// ThemeCard is one theme entry of the directory. Source and Screenshot are
// already restricted to safe https URLs; Search is the lowercased text the
// search box matches against.
//
// About holds the optional manifest text as plain paragraphs and Features its
// bullets; both are untrusted text, to be rendered escaped.
type ThemeCard struct {
	Name, Title, Version, Description, Source, Screenshot, Tier, TierLabel, Author, Search string
	About, Features                                                                        []string
}

// PluginCard is one plugin entry of the directory. Source is already
// restricted to safe https URLs; Search is the lowercased text the search box
// matches against.
//
// PermCount is the number of permissions the plugin asks for, PermWarn the
// short labels of the risky ones (empty: no warning badge) and PermLines the
// plain-English lines shown in the collapsed list. Permissions holds the raw
// names; the default theme no longer reads it, it is kept for theme overrides
// written against older cores.
type PluginCard struct {
	Name, Title, Version, Tier, TierLabel, Author, Description, Source string
	PermCount                                                          int
	PermWarn                                                           []string
	PermLines                                                          []PermLine
	// Permissions lists the raw permission names. The default theme no longer
	// reads it; it is kept for theme overrides written against older cores.
	Permissions []string
	Search      string
	// About holds the optional manifest text as plain paragraphs and Features
	// its bullets; both are untrusted text, to be rendered escaped.
	About, Features []string
}

// RegistryPageView is the plugin or theme behind a registry detail page. Type
// is "plugin" or "theme"; Plugin or Theme is set accordingly. ListName and
// ListURL name the directory page the breadcrumb points back to.
type RegistryPageView struct {
	Type              string
	ListName, ListURL string
	Plugin            *PluginCard
	Theme             *ThemeCard
}

// PermLine is one permission of a plugin card: its name, the plain-English
// text and whether it is a risky one.
type PermLine struct {
	Name, Text string
	Warn       bool
}

// FormView is a public form definition used by contact_form sections.
type FormView struct {
	Slug          string
	Name          string
	Fields        []FormField
	SubmitLabel   string
	SuccessNotice string
	// Captcha asks for a spam-check widget above the submit button. It is set
	// only when the form requires the check and a provider is usable.
	// CaptchaSiteKey is public by design; the secret never reaches a view.
	Captcha         bool
	CaptchaProvider string // turnstile|recaptcha|hcaptcha
	CaptchaSiteKey  string
}

// FormField is one input of a FormView.
type FormField struct {
	Name        string
	Label       string
	Type        string // text|email|tel|textarea|select|checkbox|radio
	Required    bool
	Placeholder string
	HelpText    string
	Options     []FormOption
}

// FormOption is one choice of a select or radio field.
type FormOption struct {
	Value string
	Label string
}

// FeedLink is one advertised feed: Type is its MIME type, Href its URL.
type FeedLink struct {
	Title, Type, Href string
}
