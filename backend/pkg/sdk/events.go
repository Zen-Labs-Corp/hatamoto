package sdk

import (
	"encoding/json"
	"time"
)

// All IDs are strings (UUID text) so the SDK needs no uuid dependency.

// EntityRef identifies an entity inside hook payloads and builders.
type EntityRef struct {
	ID         string `json:"id"`
	SchemaSlug string `json:"schema_slug"`
	Slug       string `json:"slug"`
	Locale     string `json:"locale"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
}

// EntityEvent is the payload of the entity.* actions.
type EntityEvent struct {
	ID         string `json:"id"`
	SchemaSlug string `json:"schema_slug"`
	Slug       string `json:"slug"`
	Locale     string `json:"locale"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
}

// ImportEvent is the payload of import.completed: one per imported batch,
// instead of an entity.created or entity.published per row.
type ImportEvent struct {
	Source  string `json:"source"`
	Created int    `json:"created"`
	Updated int    `json:"updated"`
	Failed  int    `json:"failed"`
}

// HeadContext is the payload of the render.head filter. Path, Language,
// SchemaSlug and EntityID describe the page; a filter appends to Tags.
// A filter that supplies its own Organization/WebSite graph as jsonld tags
// sets ReplaceSiteGraph so the host omits its generic site graph. The flag
// is preserved as long as later filters return the context they were given.
// It is fail-open: if the chain times out or panics, or no valid jsonld tag
// results, the generic graph is kept. Since 0.2.2.
type HeadContext struct {
	Path             string    `json:"path"`
	Language         string    `json:"language"`
	SchemaSlug       string    `json:"schema_slug"`
	EntityID         string    `json:"entity_id"`
	Tags             []HeadTag `json:"tags,omitempty"`
	ReplaceSiteGraph bool      `json:"replace_site_graph"`
}

// HeadTag is one head tag. Kind is meta, link, script-src or jsonld. The host
// drops attributes outside a per-kind allowlist (meta: name, property,
// content; link: rel, href, type, hreflang, crossorigin; script-src: src,
// crossorigin) and unsafe URL schemes. For jsonld, JSONLD holds the document.
type HeadTag struct {
	Kind   string            `json:"kind"`
	Attrs  map[string]string `json:"attrs,omitempty"`
	JSONLD json.RawMessage   `json:"jsonld,omitempty"`
}

// AgentHitEvent is the payload of agent.hit. It deliberately carries no IP
// address (and no user agent): Verification is the verifier's verdict at
// storage time, normally "pending". EntityID is empty when the path is not an
// entity page; Status is 0 when unknown.
type AgentHitEvent struct {
	AgentID      string    `json:"agent_id"`
	Operator     string    `json:"operator"`
	Purpose      string    `json:"purpose"`
	Verification string    `json:"verification"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	EntityID     string    `json:"entity_id"`
	Source       string    `json:"source"`
	Status       int       `json:"status"`
	Bytes        int       `json:"bytes"`
	DurationMS   int       `json:"duration_ms"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// AgentFirstCrawlEvent is the payload of agent.first_crawl. PublishedAt is nil
// when the entity has no publish time.
type AgentFirstCrawlEvent struct {
	EntityID    string     `json:"entity_id"`
	AgentID     string     `json:"agent_id"`
	Operator    string     `json:"operator"`
	Purpose     string     `json:"purpose"`
	FirstAt     time.Time  `json:"first_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// JSONLDDoc is the value filtered by jsonld.enrich. Mutate Doc (or return a
// modified copy).
type JSONLDDoc struct {
	Entity  EntityRef      `json:"entity"`
	BaseURL string         `json:"base_url"`
	Doc     map[string]any `json:"doc"`
}

// Alternate is an hreflang alternate of a sitemap URL.
type Alternate struct {
	Rel      string `json:"rel"`
	HrefLang string `json:"hreflang"`
	Href     string `json:"href"`
}

// SitemapURL is one <url> entry.
type SitemapURL struct {
	Loc        string      `json:"loc"`
	LastMod    string      `json:"lastmod"`
	ChangeFreq string      `json:"changefreq"`
	Priority   float64     `json:"priority"`
	Alternates []Alternate `json:"alternates,omitempty"`
}

// MaxSitemapAdditions is the most URLs one plugin may add to one sitemap file
// per call; the host drops the whole answer beyond it.
const MaxSitemapAdditions = 5000

// SitemapURLs is the value filtered by sitemap.urls for one sitemap file.
//
// The host sends only Group and Language, and in Add the entries that
// sitemap.urls filters before this one already added (empty for the first):
// never the file's own URL list, because a Go guest spends about 0.5 s per
// 5,000 URLs to decode and encode one. A plugin appends the entries it wants
// to add to Add and returns it; the host merges them into the file and drops
// any whose Loc the file already has. A plugin cannot remove or change
// existing entries.
//
// Group identifies which file: a group slug for a group sitemap and "misc"
// for the misc sitemap holding static routes. Language is the BCP 47 site
// language. Filters that add a site-wide URL should target one file (usually
// Group == "misc") or it appears in many.
type SitemapURLs struct {
	Group    string       `json:"group"`
	Language string       `json:"language"`
	Add      []SitemapURL `json:"add"`
}

// EntityDraft is the value filtered by entity.before_save. Operation is one
// of "create", "update" or "restore"; ID is empty for create.
type EntityDraft struct {
	Operation  string `json:"operation"`
	ID         string `json:"id"`
	SchemaSlug string `json:"schema_slug"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	// Locale is the entity's BCP 47 tag; the host does not read it back.
	Locale     string          `json:"locale,omitempty"`
	Attributes json.RawMessage `json:"attributes"`
}

// FormSubmittedEvent is the payload of form.submitted. It carries the
// submitted fields, so the host only delivers it to plugins granted
// forms.read. No IP address or user agent is included.
type FormSubmittedEvent struct {
	FormID       string         `json:"form_id"`
	FormSlug     string         `json:"form_slug"`
	SubmissionID string         `json:"submission_id"`
	Fields       map[string]any `json:"fields"`
	SourcePath   string         `json:"source_path,omitempty"`
	SubmittedAt  time.Time      `json:"submitted_at"`
}

// PageContext is the value filtered by render.page for one public page view.
// The result may reorder or replace Blocks, hide blocks by id (Hide) and set
// cookies (SetCookies). Cookies holds only the cookie names the plugin
// declared in its manifest (cookies); SetCookies may only use declared names.
//
// Since SDK 0.9 a plugin gets slim blocks (id, type, title, hidden and the
// scalar Fields in PageBlockFields) unless it asks for the whole page with
// `render_page.full_blocks` in its manifest or was built for an SDK before 0.9.
type PageContext struct {
	Path       string            `json:"path"`
	Cookies    map[string]string `json:"cookies"`
	Entity     *EntitySummary    `json:"entity,omitempty"`
	Blocks     []Block           `json:"blocks"`
	Hide       []string          `json:"hide,omitempty"`
	SetCookies []SetCookie       `json:"set_cookies,omitempty"`
}

// PageBlockFields are the content keys a slim block carries as Fields, when
// their value is a short string, number or boolean.
var PageBlockFields = []string{"variant", "anchor", "label"}

// EntitySummary describes the entity behind a page view.
type EntitySummary struct {
	ID         string `json:"id"`
	SchemaSlug string `json:"schema_slug"`
	Slug       string `json:"slug"`
	Locale     string `json:"locale"`
	Name       string `json:"name"`
}

// Block is one section of a page. A block the plugin returns with Hidden set
// is dropped from the page, like one whose id is listed in Hide.
//
// Slim mode (the default since SDK 0.9): the plugin receives Fields and no
// Content. A returned block without Content is untouched: the host puts the
// original content back by id, so return it as received (or reorder it).
// A returned block with Content replaces the block's content (and takes the
// returned Type and Title); the host sanitises it when rendering, like any
// block. Fields in a result are ignored. In full mode (`render_page.full_blocks`)
// the plugin receives Content and the returned blocks are used as they are.
type Block struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Title   string         `json:"title,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Content map[string]any `json:"content,omitempty"`
	Hidden  bool           `json:"hidden,omitempty"`
}

// SetCookie is a cookie a render.page filter asks the host to set. The host
// forces Path=/ unless set, HttpOnly, Secure on HTTPS and SameSite=Lax;
// MaxAge is seconds (capped at one year).
type SetCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	MaxAge int    `json:"max_age,omitempty"`
	Path   string `json:"path,omitempty"`
}

// Caps of the builder.data contract. The host drops a whole result that
// breaks them.
const (
	MaxBuilderDataRequests = 16   // distinct requests per render
	MaxBuilderDataItems    = 48   // items per result (and per request limit)
	MaxBuilderDataString   = 2048 // bytes per string, valid UTF-8
	MaxBuilderDataAttrs    = 16   // Attrs keys per item
)

// BuilderDataRequest is the builder.data filter payload. The host sends
// Requests (only those naming the plugin's own ext.<plugin>.<name> sources)
// and the plugin returns the same struct with Results set, one per request it
// answers, matched by ID. Results for ids that were not sent are ignored.
type BuilderDataRequest struct {
	Path       string              `json:"path,omitempty"`
	Language   string              `json:"language,omitempty"`
	SchemaSlug string              `json:"schema_slug,omitempty"`
	EntityID   string              `json:"entity_id,omitempty"`
	Requests   []BuilderDataQuery  `json:"requests,omitempty"`
	Results    []BuilderDataResult `json:"results,omitempty"`
}

// BuilderDataQuery asks one source for data. Source is "ext.<plugin>.<name>";
// Limit bounds the items of an items source (at most MaxBuilderDataItems).
type BuilderDataQuery struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Limit  int    `json:"limit,omitempty"`
}

// BuilderDataResult answers one query: Value for a value source, Items for an
// items source. A non-empty Error marks the request failed (empty output).
type BuilderDataResult struct {
	ID    string            `json:"id"`
	Value string            `json:"value,omitempty"`
	Items []BuilderDataItem `json:"items,omitempty"`
	Error string            `json:"error,omitempty"`
}

// BuilderDataItem is one entry of an items source. It feeds the loop item
// scope (loop.item.title, .url, ..., and loop.item.attributes.<k> from Attrs).
// URLs and images go through the host's usual URL safety checks.
type BuilderDataItem struct {
	Title       string            `json:"title,omitempty"`
	URL         string            `json:"url,omitempty"`
	Excerpt     string            `json:"excerpt,omitempty"`
	Image       string            `json:"image,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	AuthorURL   string            `json:"author_url,omitempty"`
	PublishedAt string            `json:"published_at,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
}
