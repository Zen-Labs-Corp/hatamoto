package redirect

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFoundEntryNotFound = errors.New("404 entry not found")
	ErrRedirectNotFound      = errors.New("redirect not found")
	ErrDuplicateSourcePath   = errors.New("redirect with this source path already exists")
	ErrInvalidStatusCode     = errors.New("invalid redirect status code")
	// ErrExternalTarget is returned in the public demo, which only redirects within the site.
	ErrExternalTarget = errors.New("redirects can only point to a path on this site in the public demo")
)

// ValidStatusCode returns true if the code is an allowed redirect status.
func ValidStatusCode(code int) bool {
	return code == 301 || code == 302 || code == 307 || code == 308
}

// NotFoundEntry represents a logged 404 hit.
type NotFoundEntry struct {
	ID            uuid.UUID  `json:"id"`
	Path          string     `json:"path"`
	HitCount      int        `json:"hit_count"`
	LastReferrer  *string    `json:"last_referrer,omitempty"`
	LastUserAgent *string    `json:"last_user_agent,omitempty"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	Ignored       bool       `json:"ignored"`
	RedirectID    *uuid.UUID `json:"redirect_id,omitempty"`
}

// Redirect represents a URL redirect rule.
type Redirect struct {
	ID         uuid.UUID `json:"id"`
	SourcePath string    `json:"source_path"`
	TargetURL  string    `json:"target_url"`
	StatusCode int       `json:"status_code"`
	HitCount   int       `json:"hit_count"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// Expanded marks a wildcard match whose target was computed from the
	// request path; the public handlers keep the query string on it.
	Expanded bool `json:"-"`
}

// CreateRedirectInput is the input for creating a redirect.
type CreateRedirectInput struct {
	SourcePath    string     `json:"source_path"`
	TargetURL     string     `json:"target_url"`
	StatusCode    int        `json:"status_code"`
	NotFoundLogID *uuid.UUID `json:"not_found_log_id,omitempty"`
}

// UpdateRedirectInput is the input for updating a redirect.
type UpdateRedirectInput struct {
	SourcePath *string `json:"source_path,omitempty"`
	TargetURL  *string `json:"target_url,omitempty"`
	StatusCode *int    `json:"status_code,omitempty"`
	IsActive   *bool   `json:"is_active,omitempty"`
}

// NotFoundListParams controls 404 log listing.
type NotFoundListParams struct {
	Search  string `json:"search"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
}

// RedirectListParams controls redirect listing.
type RedirectListParams struct {
	Search  string `json:"search"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
}
