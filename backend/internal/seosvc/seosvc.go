// Package seosvc is the contract of the SEO settings API: the document the
// REST endpoint and the MCP tools read and patch, the error they share, and the
// service both call. The implementation lives in the server package, so the
// adapters depend on this package and never on the admin.
package seosvc

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seopanel"
)

// Doc is the SEO settings document: one entry per section of seo.Settings under
// its JSON name (stored polarity, e.g. monitor404.disabled), plus type_settings
// (per content type, keyed by slug) and ai_crawlers. The IndexNow key is never
// part of it.
type Doc map[string]any

// Preview is the SEO part of a page's public head for one entry.
type Preview = render.SEOPreviewTags

// Panel is the analysis the entry's SEO panel shows: checks, counts, score.
type Panel = seopanel.Panel

// Actor says who made a change, for the audit row.
type Actor struct {
	// Via is "api" or "mcp".
	Via string
	// KeyName and KeyID name the API key; both empty for a signed-in owner.
	KeyName, KeyID string
	// UserID and Email identify a signed-in owner.
	UserID, Email string
}

// Label is the audit actor: "api-key:<name>" for a key, else the owner's email.
func (a Actor) Label() string {
	if a.KeyName != "" {
		return "api-key:" + a.KeyName
	}
	return a.Email
}

// ErrNotFound means the entry does not exist or has no public page to preview.
var ErrNotFound = errors.New("seo: entry not found")

// ValidationError is a patch that was refused. Nothing was changed. Fields maps
// a setting name (as the admin forms name it, e.g. "type[doc][title]") to what
// is wrong with it.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e.Fields[k])
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Service reads and changes the SEO settings and answers the read-only SEO
// questions about one entry.
type Service interface {
	// Get returns the whole document.
	Get(ctx context.Context) (Doc, error)
	// Update applies a partial patch (top-level section name -> JSON) and
	// returns the new document. A refused patch returns *ValidationError and
	// changes nothing.
	Update(ctx context.Context, actor Actor, patch map[string]json.RawMessage) (Doc, error)
	// Preview returns the title, description, canonical, robots and social tags
	// the public page of a published entry carries.
	Preview(ctx context.Context, entityID uuid.UUID) (Preview, error)
	// Analyze returns the SEO panel of an entry as stored.
	Analyze(ctx context.Context, entityID uuid.UUID) (Panel, error)
}
