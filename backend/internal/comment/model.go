// Package comment stores visitor comments on entities: statuses, threading,
// commenter trust and the site/schema/entity switches. It holds no spam logic
// (callers pass an already formatted and scored draft) and no HTTP code.
package comment

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Status is where a comment is in its life.
type Status string

// Comment statuses.
const (
	StatusDraft    Status = "draft"
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusSpam     Status = "spam"
	StatusTrash    Status = "trash"
	// StatusDead is a comment a plugin marked dead: public as a placeholder,
	// never counted, never purged.
	StatusDead Status = "dead"
)

// TrustLevel is how far a commenter is trusted.
type TrustLevel string

// Trust levels. Blocked is set by a spam mark or a moderator, never computed.
const (
	TrustNew     TrustLevel = "new"
	TrustKnown   TrustLevel = "known"
	TrustTrusted TrustLevel = "trusted"
	TrustBlocked TrustLevel = "blocked"
)

// rank orders the computed levels; blocked is handled separately.
func (t TrustLevel) rank() int {
	switch t {
	case TrustKnown:
		return 1
	case TrustTrusted:
		return 2
	}
	return 0
}

// Mode is the resolved comment mode of one entity.
type Mode string

// Resolved modes.
const (
	ModeOff    Mode = "off"
	ModeOpen   Mode = "open"
	ModeClosed Mode = "closed"
	ModeHidden Mode = "hidden"
)

// Sort orders the top-level comments of a thread.
type Sort string

// Sort orders.
const (
	SortNewest Sort = "newest"
	SortOldest Sort = "oldest"
)

// Thresholds are the four numbers behind the known and trusted levels.
type Thresholds struct {
	KnownApproved   int `json:"known_approved"`
	KnownDays       int `json:"known_days"`
	TrustedApproved int `json:"trusted_approved"`
	TrustedDays     int `json:"trusted_days"`
}

// SpamQuietPeriod is how long a spam mark blocks the trusted level. It is
// fixed, not one of the four editable thresholds.
const SpamQuietPeriod = 90 * 24 * time.Hour

// EditWindow is how long a commenter may edit or delete their own comment
// through the device cookie. Enforced by the public part, not by Service.Edit.
const EditWindow = 15 * time.Minute

// DraftTTL is how long an unconfirmed draft lives before the retention job
// purges it. This package only names the number.
const DraftTTL = 24 * time.Hour

// NotifyMode is how moderators hear about held comments.
type NotifyMode string

const (
	NotifyPerComment NotifyMode = "per_comment" // at most one mail per moderator per 10 min, the rest in a digest
	NotifyDigest     NotifyMode = "digest"      // at most one mail per moderator per hour
	NotifyOff        NotifyMode = "off"
)

// Settings are the site-wide comment settings. The stored JSON decodes over
// Defaults, so a later release can add a key without a migration.
type Settings struct {
	Enabled          bool       `json:"-"` // its own column
	Thresholds       Thresholds `json:"thresholds"`
	AutoCloseDays    int        `json:"auto_close_days"` // 0 = never
	MaxDepth         int        `json:"max_depth"`       // 1 = flat
	PageSize         int        `json:"page_size"`
	Sort             Sort       `json:"sort"`
	SlowModeSeconds  int        `json:"slow_mode_seconds"`
	Captcha          bool       `json:"captcha"`
	DeviceCookieDays int        `json:"device_cookie_days"`
	PurgeDays        int        `json:"purge_days"`
	NotifyMode       NotifyMode `json:"notify_mode"`
}

// Defaults returns the settings of a site that never changed them: comments off.
func Defaults() Settings {
	return Settings{
		Thresholds:       Thresholds{KnownApproved: 3, KnownDays: 7, TrustedApproved: 15, TrustedDays: 30},
		AutoCloseDays:    30,
		MaxDepth:         5,
		PageSize:         50,
		Sort:             SortNewest,
		SlowModeSeconds:  120,
		DeviceCookieDays: 90,
		PurgeDays:        30,
		NotifyMode:       NotifyPerComment,
	}
}

func (m NotifyMode) valid() bool {
	return m == NotifyPerComment || m == NotifyDigest || m == NotifyOff
}

// Normalize replaces out-of-range values with defaults so a bad stored value
// never breaks a read.
func (s Settings) Normalize() Settings {
	d := Defaults()
	if s.MaxDepth < 1 || s.MaxDepth > 10 {
		s.MaxDepth = d.MaxDepth
	}
	if s.PageSize < 1 || s.PageSize > 200 {
		s.PageSize = d.PageSize
	}
	if s.Sort != SortNewest && s.Sort != SortOldest {
		s.Sort = d.Sort
	}
	t := &s.Thresholds
	if t.KnownApproved < 1 {
		t.KnownApproved = d.Thresholds.KnownApproved
	}
	if t.TrustedApproved < 1 {
		t.TrustedApproved = d.Thresholds.TrustedApproved
	}
	if t.KnownDays < 0 {
		t.KnownDays = d.Thresholds.KnownDays
	}
	if t.TrustedDays < 0 {
		t.TrustedDays = d.Thresholds.TrustedDays
	}
	if s.AutoCloseDays < 0 {
		s.AutoCloseDays = d.AutoCloseDays
	}
	if s.SlowModeSeconds < 0 {
		s.SlowModeSeconds = d.SlowModeSeconds
	}
	if s.DeviceCookieDays < 1 {
		s.DeviceCookieDays = d.DeviceCookieDays
	}
	if s.PurgeDays < 1 {
		s.PurgeDays = d.PurgeDays
	}
	if !s.NotifyMode.valid() {
		s.NotifyMode = d.NotifyMode
	}
	return s
}

// FieldError names the setting that failed validation.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return "comment settings: " + e.Field + ": " + e.Message }

// Validate reports the first out-of-range field, for SaveSettings.
func (s Settings) Validate() error {
	bad := func(field, msg string) error { return &FieldError{Field: field, Message: msg} }
	switch {
	case s.MaxDepth < 1 || s.MaxDepth > 10:
		return bad("max_depth", "must be between 1 and 10")
	case s.PageSize < 1 || s.PageSize > 200:
		return bad("page_size", "must be between 1 and 200")
	case s.Sort != SortNewest && s.Sort != SortOldest:
		return bad("sort", "must be newest or oldest")
	case s.Thresholds.KnownApproved < 1:
		return bad("thresholds.known_approved", "must be at least 1")
	case s.Thresholds.TrustedApproved < 1:
		return bad("thresholds.trusted_approved", "must be at least 1")
	case s.Thresholds.KnownDays < 0:
		return bad("thresholds.known_days", "must not be negative")
	case s.Thresholds.TrustedDays < 0:
		return bad("thresholds.trusted_days", "must not be negative")
	case s.AutoCloseDays < 0:
		return bad("auto_close_days", "must not be negative")
	case s.SlowModeSeconds < 0:
		return bad("slow_mode_seconds", "must not be negative")
	case s.DeviceCookieDays < 1:
		return bad("device_cookie_days", "must be at least 1")
	case s.PurgeDays < 1:
		return bad("purge_days", "must be at least 1")
	case !s.NotifyMode.valid():
		return bad("notify_mode", "must be per_comment, digest or off")
	}
	return nil
}

// Comment is one stored comment. It holds hashes, never a raw IP or email.
type Comment struct {
	ID, EntityID uuid.UUID
	ParentID     *uuid.UUID
	Path         string
	Depth        int
	Status       Status

	BodyRaw, BodyHTML string
	HasLinks          bool
	LinkDomains       []string
	BodyHash          string

	CommenterID  *uuid.UUID
	AuthorName   string
	AuthorUserID *uuid.UUID

	IPHash, NetHash, UserAgent string

	SpamScore   int
	SpamVerdict string
	SpamReasons []string

	CreatedAt, UpdatedAt time.Time
	ConfirmedAt          *time.Time
	ApprovedAt           *time.Time
	EditedAt             *time.Time
	ApprovedBy           *uuid.UUID
	Locale               string
	NotifyReplies        bool    // the commenter asked for reply mail; effective only with ConfirmedAt set
	Rank                 float64 // stored order key, higher first; plugin-written
	DeadBy               string  // plugin that marked the comment dead, "" otherwise
}

// Commenter is a visitor identified by a hashed email address.
type Commenter struct {
	ID              uuid.UUID
	EmailHash       string
	Trust           TrustLevel
	ApprovedCount   int
	FirstApprovedAt *time.Time
	LastSpamAt      *time.Time
	TokenVersion    int
	CreatedAt       time.Time
	LastSeenAt      time.Time
}

// Draft is an already formatted and scored submission (Service.Store input).
type Draft struct {
	EntityID     uuid.UUID
	ParentID     *uuid.UUID
	Email        string // raw; hashed and sealed by the service. Empty for admin replies.
	AuthorName   string
	AuthorUserID *uuid.UUID

	BodyRaw, BodyHTML string
	HasLinks          bool
	LinkDomains       []string
	BodyHash          string

	IP        string // raw; only ip_hash and net_hash are stored
	UserAgent string // truncated to 256 runes by the service

	SpamScore   int
	SpamVerdict string
	SpamReasons []string

	Status Status // draft, or the final status chosen with Decide
	Locale string

	NotifyReplies bool // the form's opt-in box; ignored without Email (admin posts)
}

// Edited is a re-formatted body for Service.Edit.
type Edited struct {
	BodyRaw, BodyHTML string
	HasLinks          bool
	LinkDomains       []string
	BodyHash          string
	ByAdmin           bool
	// Hold sends an approved comment back to moderation (the content checks
	// of a new post held the edited text).
	Hold bool
}

// ThreadComment is the public view: never body_raw, hashes or email.
type ThreadComment struct {
	ID           uuid.UUID
	ParentID     *uuid.UUID
	Depth        int
	AuthorName   string
	IsSiteAuthor bool
	BodyHTML     string
	CreatedAt    time.Time
	EditedAt     *time.Time
	Status       Status
	Rank         float64
}

// ThreadPage is one page of a thread in render order.
type ThreadPage struct {
	Comments []ThreadComment
	Page     int
	Pages    int // pages of top-level comments; Page when the page is past the end
	HasMore  bool
}

// Errors returned by the service.
var (
	ErrNotFound      = errors.New("comment: not found")
	ErrBadTransition = errors.New("comment: status transition not allowed")
)

// CanTransition reports whether a comment may move from one status to another.
func CanTransition(from, to Status) bool {
	allowed := map[Status][]Status{
		StatusDraft:    {StatusPending, StatusApproved, StatusSpam, StatusTrash},
		StatusPending:  {StatusApproved, StatusSpam, StatusTrash},
		StatusApproved: {StatusPending, StatusSpam, StatusTrash, StatusDead},
		StatusSpam:     {StatusPending, StatusApproved, StatusTrash},
		StatusTrash:    {StatusPending, StatusApproved, StatusSpam},
		StatusDead:     {StatusApproved, StatusSpam, StatusTrash},
	}
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}
