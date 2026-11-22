// Package llmstxt builds the /llms.txt site summary and the /llms-full.txt
// Markdown dump. It is pure: callers pass in plain model values and base
// relative paths, so nothing here knows about HTTP, URL patterns or storage
// beyond the state store.
package llmstxt

// Site is the file-level data shared by both files.
type Site struct {
	Name    string // H1
	Summary string // blockquote; omitted when empty
	Details string // paragraph under the summary; omitted when empty
	BaseURL string // absolute origin, no trailing slash needed
	FullOn  bool   // list /llms-full.txt under Optional
	HasFeed bool   // list /feed.xml under Optional
}

// Item is one entry of a section.
type Item struct {
	Key      string // snapshot key, "<locale>|<entity id>"
	EntityID string
	Name     string
	Path     string // base-relative, e.g. "/services/web-design"
	Desc     string // one-line description
	Job      string // people only
}

// Section kinds.
const (
	KindKey    = "key"
	KindPages  = "pages"
	KindType   = "type"
	KindPosts  = "posts"
	KindPeople = "people"
)

// Section is one H2 list. Title is final (already carries a locale suffix on
// multi-locale sites); Locale is "" for the default locale.
type Section struct {
	Title  string
	Locale string
	Kind   string
	Items  []Item
}
