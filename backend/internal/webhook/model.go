// Package webhook sends signed outgoing webhooks. It is a port of the former
// first-party webhooks plugin; the receiver contract (body, headers,
// signatures, secrets, retry schedule) is unchanged, see docs/WEBHOOKS.md.
package webhook

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Event names a subscription may list.
const (
	EventEntityCreated   = "entity.created"
	EventEntityUpdated   = "entity.updated"
	EventEntityPublished = "entity.published"
	EventEntityDeleted   = "entity.deleted"
	EventFormSubmitted   = "form.submitted"
	EventImportCompleted = "import.completed"
	// EventTest is the synthetic event of "Send test"; it is never subscribable.
	EventTest = "webhook.test"
)

const (
	SecretPrefix     = "whsec_"
	MaxSubscriptions = 50
	// MaxResponseBody is how many bytes of a receiver's answer are stored.
	MaxResponseBody = 1024
	// MaxTimeoutSeconds is the longest request timeout a subscription may ask for.
	MaxTimeoutSeconds = 5
	MaxBackoff        = time.Hour
	// MaxPending stops queueing for a subscription whose backlog is this long.
	MaxPending = 5000
	// LogPerSubscription is how many finished deliveries each subscription keeps.
	LogPerSubscription = 1000
	// LeaseSeconds is how long a claimed delivery is left alone.
	LeaseSeconds = 60
)

var eventTypes = []string{
	EventEntityCreated, EventEntityUpdated, EventEntityPublished,
	EventEntityDeleted, EventFormSubmitted, EventImportCompleted,
}

// ValidEvents returns the subscribable events in a stable order.
func ValidEvents() []string { return append([]string(nil), eventTypes...) }

// Subscription is a row of hatamoto_webhooks.
type Subscription struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	URL               string            `json:"url"`
	Secret            string            `json:"secret,omitempty"`
	Events            []string          `json:"events"`
	Headers           map[string]string `json:"headers"`
	Active            bool              `json:"active"`
	MaxRetries        int               `json:"max_retries"`
	RetryDelaySeconds int               `json:"retry_delay_seconds"`
	TimeoutSeconds    int               `json:"timeout_seconds"`
	AllowHTTP         bool              `json:"allow_http"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// NewSecret returns a signing secret: "whsec_" and 64 hex characters.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return SecretPrefix + hex.EncodeToString(b), nil
}
