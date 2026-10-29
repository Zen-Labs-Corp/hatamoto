// Package taxonomy implements the Phase-2 Hatamoto first-class taxonomy
// system: tags and categories as a proper many-to-many model that works
// across every entity schema (not just blog posts), backed by the
// hatamoto_terms and hatamoto_entity_terms tables introduced in
// migration 113.
//
// The package is intentionally additive — existing JSONB `attributes.tags`
// remain valid and unchanged. New code paths (admin term picker, public
// /tag and /category archives) read and write through this package; the
// old per-entity JSONB tags still render in JSON-LD and on legacy pages.
// Migrating the old data into the new store is a deliberate non-goal of
// Phase 2 (see plans/archive/completed/hatamoto-core-incremental-plan.md).
package taxonomy

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Term kinds. "tag" and "category" are built in; any other kind is a custom
// taxonomy (for example "genre") that exists as soon as its first term does.
// There is no registry: a kind is a short lower-case word, and the CHECK
// constraint on hatamoto_terms.kind enforces the same format as ValidKind.
const (
	KindTag      = "tag"
	KindCategory = "category"
)

// MaxKindLen mirrors the kind column width.
const MaxKindLen = 16

// MaxDepth caps how deep terms nest (a root is depth 1), so a term's public
// path stays a sane URL.
const MaxDepth = 8

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)

// reservedKinds are first URL segments the site already uses: a term archive
// of that kind would be unreachable or would shadow a built-in route.
var reservedKinds = map[string]struct{}{
	"author": {}, "search": {}, "api": {}, "admin": {}, "p": {}, "feed": {}, "health": {}, "ready": {},
	"static": {}, "media": {}, "mcp": {}, "sitemap": {}, "robots": {}, "llms": {}, "preview": {}, "page": {},
	"theme": {}, "forms": {},
}

// reservedKindPrefixes are first URL segments the proxy hands to another
// service by prefix: the reverse proxy sends /auth* to the sign-in app, so a
// kind that merely starts with it ("authors") would never reach this server.
var reservedKindPrefixes = []string{"auth"}

// MaxNameLen caps the human-readable name a term may carry. Mirrors the
// VARCHAR(128) column width.
const MaxNameLen = 128

// MaxSlugLen mirrors the slug column width.
const MaxSlugLen = 128

// MaxTermsPerEntity caps how many terms can be attached to a single
// entity in one PUT replace call. Sane upper bound — even taxonomy-heavy
// content rarely uses more than ~30 terms.
const MaxTermsPerEntity = 64

// slugPattern enforces lowercase ASCII alphanumerics + hyphens. Matches
// what Slugify produces, so manual edits and auto-slugs converge.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Term is the Go projection of a hatamoto_terms row.
type Term struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Path is the slash-joined slugs from the root term down to this one
	// ("tutorials/go"); set by Repository.FillPaths, empty otherwise.
	Path string `json:"path,omitempty"`
}

// TermWithCount augments a term with the count of entities currently
// attached to it. Used by the admin list endpoint so the UI can show
// usage at a glance.
type TermWithCount struct {
	Term
	EntityCount int `json:"entity_count"`
}

// TermListResult is the paginated admin list response.
type TermListResult struct {
	Items   []*TermWithCount `json:"items"`
	Total   int              `json:"total"`
	Page    int              `json:"page"`
	PerPage int              `json:"per_page"`
}

// EntityRef is the lightweight entity projection returned by the public
// archive endpoint. We avoid leaking the full entity row — public
// consumers only need enough to build a card link.
type EntityRef struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	SchemaSlug  string     `json:"schema_slug"`
	Excerpt     string     `json:"excerpt,omitempty"`
	ImageURL    string     `json:"image_url,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// TermArchiveResult is the public archive payload.
type TermArchiveResult struct {
	Term    *Term        `json:"term"`
	Items   []*EntityRef `json:"items"`
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	PerPage int          `json:"per_page"`
	HasNext bool         `json:"has_next"`
	HasPrev bool         `json:"has_prev"`
}

// Slugify converts a free-form name to a slug. Lowercases, replaces
// runs of non-alphanumerics with single hyphens, strips leading/trailing
// hyphens. Returns "" for inputs that contain no slug-able characters
// (caller decides whether to error or fall back).
func Slugify(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	prevHyphen := false
	for _, r := range strings.TrimSpace(strings.ToLower(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// ASCII fold for common Latin diacritics is out of scope —
			// names in non-ASCII scripts are rare in admin term creation
			// and admins can hand-edit slugs.
			if r > 127 {
				continue
			}
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	s := b.String()
	s = strings.TrimRight(s, "-")
	if len(s) > MaxSlugLen {
		s = s[:MaxSlugLen]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// ValidSlug reports whether the supplied slug is well-formed.
func ValidSlug(slug string) bool {
	if slug == "" || len(slug) > MaxSlugLen {
		return false
	}
	return slugPattern.MatchString(slug)
}

// ValidKind reports whether kind is a usable term kind: the built-in tag and
// category, or a custom lower-case word that is not a reserved URL segment.
func ValidKind(kind string) bool {
	return kindPattern.MatchString(kind) && !ReservedSegment(kind)
}

// ReservedSegment reports whether seg, as a first URL segment, is one the site
// routes itself (or the proxy hands to another service), so a term kind or a
// locale on it would be unreachable or would shadow that route.
func ReservedSegment(seg string) bool {
	if _, reserved := reservedKinds[seg]; reserved {
		return true
	}
	for _, p := range reservedKindPrefixes {
		if strings.HasPrefix(seg, p) {
			return true
		}
	}
	return false
}

// Archive URL paths: the built-in kinds keep /tag/ and /category/; a custom
// kind lives at /{kind}/. Below the kind prefix a term sits at the slugs of
// its ancestors and its own, joined by '/'.
func ArchivePrefix(kind string) string { return "/" + kind + "/" }
