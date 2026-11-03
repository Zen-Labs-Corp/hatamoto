// Package changeset stages content writes of a plugin run and applies them
// all at once: one transaction, one savepoint per item, side effects only
// after the commit. Every content table carries an origin ("owner" or
// "plugin:<name>") so a changeset can be limited to what its plugin made.
package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Tx is the transaction the engine hands to writers.
type Tx = pgx.Tx

// TTL is how long a staged changeset waits for a decision before it expires.
const TTL = 72 * time.Hour

// Kind is the content kind of an item.
type Kind string

// The content kinds a changeset can carry.
const (
	KindEntity   Kind = "entity"
	KindTerm     Kind = "term"
	KindMenu     Kind = "menu"
	KindRedirect Kind = "redirect"
	KindMedia    Kind = "media"
)

// Item operations.
const (
	OpUpsert = "upsert"
	OpDelete = "delete"
)

// Changeset states.
const (
	StateStaged    = "staged"
	StatePublished = "published"
	StateDiscarded = "discarded"
	StateReverted  = "reverted"
	StateExpired   = "expired"
)

// Item results.
const (
	ResultApplied         = "applied"
	ResultSkippedConflict = "skipped_conflict"
	ResultSkippedOther    = "skipped_other"
)

// Errors of the engine.
var (
	ErrNotFound      = errors.New("changeset not found")
	ErrNotStaged     = errors.New("changeset is not staged")
	ErrNotPublished  = errors.New("changeset is not published")
	ErrExpired       = errors.New("changeset expired")
	ErrUnknownKind   = errors.New("unknown changeset item kind")
	ErrInvalidItem   = errors.New("invalid changeset item")
	ErrNoWriter      = errors.New("no writer for this kind")
	ErrUnsupportedOp = errors.New("operation not supported for this kind")

	// ErrRevertConflict is what a writer's Restore returns, before it writes
	// anything, for an object the revert must not touch (something was built on
	// it or it changed since the publish). Revert skips the item and reports it.
	ErrRevertConflict = errors.New("object changed since the changeset was published")
)

// Origin is the origin recorded on content a plugin's changeset creates. An
// empty plugin is the site owner.
func Origin(plugin string) string {
	if plugin == "" {
		return "owner"
	}
	return "plugin:" + plugin
}

// Item is one staged write. BaseToken is the token the plugin read the live
// object with ("version:updated_at" for entities, "updated_at" for the other
// kinds, RFC 3339); empty skips the conflict check. TargetID names the object
// to update or delete; an upsert without it creates, or updates the object
// the source key maps to. BlobKeys are the staged files the item publishes.
type Item struct {
	Kind      Kind
	Op        string
	SourceKey string
	TargetID  string
	BaseToken string
	Payload   json.RawMessage
	BlobKeys  []string
}

// StoredItem is an Item as saved, with its publish outcome.
type StoredItem struct {
	Item
	ID           int64
	Seq          int
	Before       json.RawMessage
	Result       string
	ResultDetail string
}

// Outcome is what a KindWriter did with one item. Conflict skips the item
// (Detail says why). Before is the pre-image for Restore (nil for a create).
// Undo, when set, reverts effects outside the transaction (copied files) and
// runs if the transaction does not commit.
type Outcome struct {
	TargetID string
	Before   json.RawMessage
	Created  bool
	Deleted  bool
	Conflict bool
	Detail   string
	Undo     func(ctx context.Context)
}

// AppliedItem is a change that committed, for the writers' After hooks.
type AppliedItem struct {
	Kind     Kind
	TargetID string
	Created  bool
	Deleted  bool
}

// KindWriter applies and restores items of one kind inside the engine's
// transaction. Implementations must not emit events, call observers or touch
// the file system beyond what Undo/After undo or finish; After runs once,
// after the commit, with the items of its kind.
type KindWriter interface {
	Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error)
	// Restore puts back the pre-image of an applied item (nil Before: the item
	// created the object, so remove it). The returned func, if any, runs after
	// the commit.
	Restore(ctx context.Context, tx Tx, it StoredItem) (func(context.Context), error)
	After(ctx context.Context, applied []AppliedItem)
}

// ConflictItem is an item publish skipped.
type ConflictItem struct {
	Seq       int
	Kind      Kind
	SourceKey string
	TargetID  string
	Reason    string
}

// Result is the outcome of Publish or Revert.
type Result struct {
	Applied   int
	Skipped   int
	Conflicts []ConflictItem
	Items     []AppliedItem
}
