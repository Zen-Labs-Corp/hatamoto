package admin

import (
	"context"
	"errors"
	"time"
)

// ErrCommentsUnsupported means the wired comment service lacks the feature
// (see CommentsService.Supports).
var ErrCommentsUnsupported = errors.New("comments: not supported")

// ErrCommentNotFound means no comment (or commenter, entity, type) has that id.
var ErrCommentNotFound = errors.New("comments: not found")

// ErrCommentBlocked means a vouch was refused because the commenter is blocked.
// Vouching never lifts a block.
var ErrCommentBlocked = errors.New("comments: commenter is blocked")

// ErrCommentEmpty and ErrCommentTooLong mean the body formatter refused the
// text of a reply or an edit.
var (
	ErrCommentEmpty   = errors.New("comments: empty body")
	ErrCommentTooLong = errors.New("comments: body too long")
)

// maxCommentBodyBytes is the formatter's body limit, so a form can refuse
// early with the same number.
const maxCommentBodyBytes = 16 << 10

// CommentFieldError names the settings field the store refused, using the
// admin form field names.
type CommentFieldError struct{ Field, Message string }

func (e *CommentFieldError) Error() string { return "comments: " + e.Field + ": " + e.Message }

// CommentSettings is the site-wide comment configuration.
type CommentSettings struct {
	Enabled                                                                  bool
	KnownMinApproved, KnownMinAgeDays, TrustedMinApproved, TrustedMinAgeDays int    // 3, 7, 15, 30
	AutoCloseDays, MaxDepth, PageSize                                        int    // 30 (0 = never), 5 (1..10), 50 (1..200)
	Sort                                                                     string // "newest" or "oldest"
	SlowModeSeconds                                                          int
	Captcha                                                                  bool
	CookieDays, PurgeDays                                                    int    // cookie 120 days, delete spam and trash after N days
	NotifyMode                                                               string // "per_comment", "digest" or "off"
}

// CommentFilter narrows the moderation queue. PerPage is at most 200.
type CommentFilter struct {
	Status, Query, EntityID, CommenterID, Trust string
	HasLinks                                    bool
	Page, PerPage                               int
}

// CommentCounts are the site-wide totals behind the status tabs (never filtered).
type CommentCounts struct{ Pending, Approved, Spam, Trash int }

// CommentRow is one comment as the moderation screen shows it. AuthorUserID
// is set for a site reply (the "Author" tag); CommenterID is empty then.
// Reasons are the human strings the model produced for why it was held.
type CommentRow struct {
	ID, EntityID, EntityTitle, ParentID, Status, BodyHTML, BodyRaw string
	AuthorName, AuthorUserID, CommenterID, Trust                   string
	ApprovedCount                                                  int
	HasLinks                                                       bool
	Score                                                          int
	Reasons                                                        []string
	CreatedAt                                                      time.Time
	EditedAt                                                       *time.Time
}

// CommentPage is one page of the queue. The store keeps no total, so there is
// only HasMore.
type CommentPage struct {
	Rows    []CommentRow
	HasMore bool
}

// SpamTeach picks what marking spam also teaches the blocklists. The
// commenter block and the hashes are always applied.
type SpamTeach struct{ LinkDomains, EmailDomain bool }

// EntityComments is the comment state of one entity. Override is "", "open",
// "closed" or "hidden"; Resolved is "off", "open", "closed" or "hidden".
type EntityComments struct {
	Override, Resolved string
	Total, Pending     int
}

// CommenterRow is one commenter in the list. It never carries an email, an IP
// or a hash.
type CommenterRow struct {
	ID, Name, Trust string
	ApprovedCount   int
	LastSeen        time.Time
}

// CommenterPage is one page of commenters.
type CommenterPage struct {
	Rows    []CommenterRow
	HasMore bool
}

// Blocklist is one blocklist kind. Entries are only listed for the email and
// link domain kinds; the hash kinds report a Count only.
type Blocklist struct {
	Kind    string
	Entries []string
	Count   int
}

// EntityOption is one entry of the entity filter.
type EntityOption struct{ ID, Title string }

// CommentPolicy is a content type's community policy (the admin-side copy of
// the comment package's policy; defaults are filled).
type CommentPolicy struct {
	Enabled                                      bool
	MaxDepth, EditWindowMinutes, SlowModeSeconds int
}

// SchemaCommentConfig is one content type's comment settings.
type SchemaCommentConfig struct {
	ID, Slug, Name string
	Builtin        bool
	Mode           string // "", open, closed, hidden
	Order          string // "", newest, oldest, rank
	Policy         CommentPolicy
}

// CommentsService is the comment store behind the Comments screen, Settings >
// Comments, the entity and schema cards, the sidebar badge and the dashboard
// card. nil hides the sidebar item and the cards and answers the pages with
// 503.
type CommentsService interface {
	// Supports reports an optional feature: "privacy", "blocklists",
	// "entity-options", "schema-mode", "schema-mode-builtin", "schema-policy".
	Supports(feature string) bool
	Settings(ctx context.Context) (CommentSettings, error)
	// SaveSettings returns a *CommentFieldError for a refused value.
	SaveSettings(ctx context.Context, s CommentSettings, actorID string) error
	SetEnabled(ctx context.Context, on bool, actorID string) (was bool, err error)
	Counts(ctx context.Context) (CommentCounts, error)
	Queue(ctx context.Context, f CommentFilter) (CommentPage, error)
	Comment(ctx context.Context, id string) (CommentRow, error)
	Approve(ctx context.Context, ids []string, actorID string) (int, error)
	// Vouch returns ErrCommentBlocked for a blocked commenter.
	Vouch(ctx context.Context, commenterID, actorID string) (newTrust string, err error)
	MarkSpam(ctx context.Context, ids []string, teach SpamTeach, actorID string) (int, error)
	Trash(ctx context.Context, ids []string, actorID string) (int, error)
	// Delete removes comments for good; their replies go too.
	Delete(ctx context.Context, ids []string, actorID string) (int, error)
	// Edit returns ErrCommentEmpty or ErrCommentTooLong for a refused body.
	Edit(ctx context.Context, id, bodyRaw, actorID string) error
	Reply(ctx context.Context, parentID, bodyRaw, actorID, authorName string) (id string, err error)
	EntityComments(ctx context.Context, entityID string) (EntityComments, error)
	SetEntityMode(ctx context.Context, entityID, mode string) error
	SchemaMode(ctx context.Context, schemaID string) (string, error)
	// SetSchemaMode returns ErrCommentsUnsupported for a built-in type unless
	// Supports("schema-mode-builtin").
	SetSchemaMode(ctx context.Context, schemaID, mode string) error
	// SchemaCommentConfigs lists every content type with its comment settings.
	SchemaCommentConfigs(ctx context.Context) ([]SchemaCommentConfig, error)
	// SetSchemaComments writes settings.comments.order and .policy and keeps
	// every other key. It returns ErrCommentsUnsupported for a built-in type
	// unless Supports("schema-mode-builtin"), a *CommentFieldError for an
	// out-of-range value and ErrCommentNotFound for an unknown id.
	SetSchemaComments(ctx context.Context, schemaID, order string, p CommentPolicy) error
	EntityOptions(ctx context.Context, limit int) ([]EntityOption, error)
	Commenters(ctx context.Context, q string, page int) (CommenterPage, error)
	Block(ctx context.Context, commenterID, actorID string) error
	RevokeDevices(ctx context.Context, commenterID string) error
	Blocklists(ctx context.Context) ([]Blocklist, error)
	// SaveBlocklist replaces a kind's entries; nil entries clear it.
	SaveBlocklist(ctx context.Context, kind string, entries []string, actorID string) error
	ExportCommenter(ctx context.Context, commenterID string) ([]byte, error)
	// EraseCommenter has one behaviour: comments without replies are deleted,
	// the others are anonymised.
	EraseCommenter(ctx context.Context, commenterID string) (deleted, anonymised int, err error)
}

// validCommentMode reports whether m is a per-entity or per-type comment mode
// ("" inherits).
func validCommentMode(m string) bool {
	switch m {
	case "", "open", "closed", "hidden":
		return true
	}
	return false
}
