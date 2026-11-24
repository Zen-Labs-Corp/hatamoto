package sdk

import (
	"encoding/json/jsontext"
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
// SchemaSlug and EntityID describe the page; Locale and Segment name its locale
// on a multi-locale site (since 0.15.0); a filter appends to Tags.
// A filter that supplies its own Organization/WebSite graph as jsonld tags
// sets ReplaceSiteGraph so the host omits its generic site graph. The flag
// is preserved as long as later filters return the context they were given.
// It is fail-open: if the chain times out or panics, or no valid jsonld tag
// results, the generic graph is kept. Since 0.2.2.
type HeadContext struct {
	Path       string `json:"path"`
	Language   string `json:"language"`
	SchemaSlug string `json:"schema_slug"`
	EntityID   string `json:"entity_id"`
	// Locale is the BCP 47 tag of the page; empty on a one-language site.
	Locale string `json:"locale,omitempty"`
	// Segment is the stored URL segment of Locale; empty for an unprefixed
	// default locale and on a one-language site.
	Segment          string    `json:"segment,omitempty"`
	Tags             []HeadTag `json:"tags,omitempty"`
	ReplaceSiteGraph bool      `json:"replace_site_graph"`
}

// SiteGraph is the payload of the render.site_graph filter (since 0.16.0).
// Nodes are the JSON-LD nodes of the page's generic site graph. A filter may
// add keys to the Organization, LocalBusiness and WebSite nodes and append
// nodes with a new unique @id; it cannot change an @id, a @type or an existing
// key. It is fail-open: on an error, timeout or oversized result the graph is
// unchanged and the page is not cached.
type SiteGraph struct {
	Path       string `json:"path"`
	Language   string `json:"language"`
	SchemaSlug string `json:"schema_slug"`
	EntityID   string `json:"entity_id"`
	// Locale and Segment name the page's locale on a multi-locale site.
	Locale  string           `json:"locale,omitempty"`
	Segment string           `json:"segment,omitempty"`
	Nodes   []map[string]any `json:"nodes"`
}

// HeadTag is one head tag. Kind is meta, link, script-src or jsonld. The host
// drops attributes outside a per-kind allowlist (meta: name, property,
// content; link: rel, href, type, hreflang, crossorigin; script-src: src,
// crossorigin) and unsafe URL schemes. For jsonld, JSONLD holds the document.
type HeadTag struct {
	Kind   string            `json:"kind"`
	Attrs  map[string]string `json:"attrs,omitempty"`
	JSONLD jsontext.Value    `json:"jsonld,omitempty"`
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
// The host sends only Group, Language, Segment and File, and in Add the entries that
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
//
// Since 0.15.0, on a multi-locale site Segment is the stored URL segment of
// the file's locale (empty for an unprefixed default locale) and File is the
// full file key ("xx-misc"; Group stays the bare name). Both are empty on a
// one-language site.
//
// With the site setting sitemap_naming "legacy" a prefixed locale has one file
// holding all its groups: File is that file's key ("xx", not "xx-misc") and the
// filter runs once per group in it, with Group the bare group name each time,
// so a plugin that switches on Group == "misc" keeps working. The bare files
// (File "blog", "misc") hold the shared rows and an unprefixed default locale;
// the shared ones carry no Language or Segment. The group-mode payload is
// unchanged.
type SitemapURLs struct {
	Group    string       `json:"group"`
	Language string       `json:"language"`
	Segment  string       `json:"segment,omitempty"`
	File     string       `json:"file,omitempty"`
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
	Locale     string         `json:"locale,omitempty"`
	Attributes jsontext.Value `json:"attributes"`
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
	// Attribution is where the submitter came from (first and last touch of
	// the browser tab). Absent when none was recorded. Since SDK 0.14.
	Attribution *FormAttribution `json:"attribution,omitzero"`
}

// FormTouch is one classified visit source of a submission: channel, source,
// medium, campaign and landing path. It never holds a URL, address or identifier.
type FormTouch struct {
	Channel  string `json:"channel,omitempty"`
	Source   string `json:"source,omitempty"`
	Medium   string `json:"medium,omitempty"`
	Campaign string `json:"campaign,omitempty"`
	Landing  string `json:"landing,omitempty"`
}

// FormAttribution is the first and last touch carried by FormSubmittedEvent.
type FormAttribution struct {
	First *FormTouch `json:"first,omitzero"`
	Last  *FormTouch `json:"last,omitzero"`
}

// CommentEvent is the payload of comment.created|approved|spam|deleted (since 0.25.0)
// and of comment.updated (SDK 0.27).
// It never carries the commenter's email, any hash, IP address, user agent,
// spam score/reasons or the raw body. comment.deleted carries ids and status only.
type CommentEvent struct {
	ID         string    `json:"id"`
	EntityID   string    `json:"entity_id"`
	ParentID   string    `json:"parent_id,omitempty"`
	AuthorName string    `json:"author_name,omitempty"`
	Staff      bool      `json:"staff,omitempty"`     // written by a signed-in owner/editor
	BodyHTML   string    `json:"body_html,omitempty"` // sanitised once on write
	Status     string    `json:"status"`              // pending|approved|spam|trash|dead
	Locale     string    `json:"locale,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitzero"` // left out on comment.deleted
	// CommenterKey (SDK 0.27, needs comments.read) is the plugin's pseudonymous
	// key of the signed-in commenter; absent for staff and never sent to webhooks.
	CommenterKey string `json:"commenter_key,omitempty"`
}

// CommentCheckMaxScore is the most one comment.check plugin can add; larger
// values are cut to it, negative ones count as 0. The host maps the sum of all
// plugins (cut to the same maximum) onto its own spam scale: CommentCheckMaxScore
// alone is enough to mark a comment as spam.
const CommentCheckMaxScore = 100

// CommentCheck is the value of the comment.check filter: the raw body text,
// the domains it links to and a site-keyed hash of the normalised body. Never
// an IP address, a network hash, an email, a name or the entity id; since SDK
// 0.27 also the entity schema slug and per-plugin pseudonymous keys.
// Score and Reasons are output only and arrive empty; the host keeps at most
// 8 reasons of 200 bytes each, prefixed with the plugin name.
type CommentCheck struct {
	Body        string   `json:"body"`
	LinkDomains []string `json:"link_domains,omitempty"`
	BodyHash    string   `json:"body_hash,omitempty"`
	Score       int      `json:"score,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
	// CommenterKey (needs comments.read), AddrKey and AreaKey (need client.key) are
	// per-plugin pseudonymous keys of the commenter, the address and the address's
	// /24 (IPv4) or /64 (IPv6) area (SDK 0.27), absent without the grant. Schema is the entity schema slug. Output stays score-only.
	CommenterKey string `json:"commenter_key,omitempty"`
	Schema       string `json:"schema,omitempty"`
	AddrKey      string `json:"addr_key,omitempty"`
	AreaKey      string `json:"area_key,omitempty"`
}

// CommentItem is one comment in comments.thread, comments.by_author and comments.subtree.
type CommentItem struct {
	ID         string    `json:"id"`
	ParentID   string    `json:"parent_id,omitempty"`
	Depth      int       `json:"depth,omitempty"`
	AuthorName string    `json:"author_name"`
	Staff      bool      `json:"staff,omitempty"`
	BodyHTML   string    `json:"body_html"`
	CreatedAt  time.Time `json:"created_at"`
	// CommenterKey, Rank and Status are SDK 0.27: the commenter's pseudonymous key
	// (needs comments.read), the rank set by comments.set_rank, and "approved" or "dead".
	CommenterKey string  `json:"commenter_key,omitempty"`
	Rank         float64 `json:"rank,omitempty"`
	Status       string  `json:"status,omitempty"`
}

// CommentThread is the result of comments.thread.
type CommentThread struct {
	Items   []CommentItem `json:"items"`
	Page    int           `json:"page"`
	HasMore bool          `json:"has_more,omitempty"`
}

// CommentSubtree is the result of comments.subtree (SDK 0.27): the comment and
// its replies in path order (Items, paged like CommentThread) and its
// ancestors, root first.
type CommentSubtree struct {
	Ancestors []CommentItem `json:"ancestors"`
	Items     []CommentItem `json:"items"`
	Page      int           `json:"page"`
	HasMore   bool          `json:"has_more,omitempty"`
}

// CommentDraftMaxReject is the longest public message a comment.draft filter may return.
const CommentDraftMaxReject = 200

// CommentDraft is the value of the comment.draft filter (SDK 0.27), called only
// for schemas whose community policy locks the author name. In: CommenterKey,
// the typed AuthorName and Schema. Out: AuthorName set to the canonical name, or
// Reject set to a public message of at most CommentDraftMaxReject bytes. Any
// failure or timeout rejects the draft ("Sign-in name unavailable, try again").
type CommentDraft struct {
	CommenterKey string `json:"commenter_key"`
	AuthorName   string `json:"author_name"`
	Schema       string `json:"schema"`
	Reject       string `json:"reject,omitempty"`
}

// CommentCounts is the result of comments.counts: approved comments per entity id.
type CommentCounts struct {
	Counts map[string]int `json:"counts"`
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
	Entity     *EntitySummary    `json:"entity,omitzero"`
	Blocks     []Block           `json:"blocks"`
	Hide       []string          `json:"hide,omitempty"`
	SetCookies []SetCookie       `json:"set_cookies,omitempty"`
	// Query (SDK 0.27) holds the first value of each key the manifest declares in page_query.
	Query map[string]string `json:"query,omitempty"`
	// CommenterKey (needs comments.read), AddrKey and NetKey (need client.key) are
	// per-plugin pseudonymous keys (SDK 0.27), absent without the grant.
	CommenterKey string `json:"commenter_key,omitempty"`
	AddrKey      string `json:"addr_key,omitempty"`
	NetKey       string `json:"net_key,omitempty"`
}

// RedirectContext is the value filtered by request.redirect for one GET or
// HEAD request to a path the plugin declared in its manifest (redirect_paths).
// Path is the exact request path and Query the raw query string. Cookies holds
// only the cookie names the plugin declared (cookies), by their short names.
// Country is the visitor's ISO 3166-1 alpha-2 code from the country database,
// or empty when unknown; the visitor's address is never passed.
//
// Redirect and SetCookies are output only and arrive empty. A Redirect uses
// status 302 or 307 and a same-site Location (a path with an optional query);
// SetCookies may only use declared names. A result that breaks a rule is
// dropped whole and the request is handled as if the plugin had not answered.
type RedirectContext struct {
	Path       string            `json:"path"`
	Query      string            `json:"query,omitempty"`
	Cookies    map[string]string `json:"cookies,omitempty"`
	Country    string            `json:"country,omitempty"`
	Redirect   *Redirect         `json:"redirect,omitempty"`
	SetCookies []SetCookie       `json:"set_cookies,omitempty"`
}

// Redirect is the answer of a request.redirect filter: Status is 302 or 307.
type Redirect struct {
	Status   int    `json:"status"`
	Location string `json:"location"`
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
	Hidden  bool           `json:"hidden,omitzero"`
}

// SetCookie is a cookie a render.page filter asks the host to set. The host
// forces Path=/ unless set, HttpOnly, Secure on HTTPS and SameSite=Lax;
// MaxAge is seconds (capped at one year).
type SetCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	MaxAge int    `json:"max_age,omitzero"`
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
	Limit  int    `json:"limit,omitzero"`
	// Params are the loop's filter values, already sanitized against the
	// source's declared params. Nil (and absent on the wire) when none apply.
	Params map[string]any `json:"params,omitempty"`
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
