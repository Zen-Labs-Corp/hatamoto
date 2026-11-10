package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// StandingRule is the automatic update rule a plugin declares for one of its
// cron runs. It only describes the rule: the owner's approval is separate.
type StandingRule struct {
	// Version and Hash name the release the rule was read from; the form
	// posts them back so Approve can tell the owner approved what was read.
	Version, Hash string
	Run           string
	Fields        []string
	Schemas       []string
	MaxPerHour    int
	// CreateSchemas and CreateMaxPerHour are set only when the rule also
	// lets the run publish new entries of those schemas.
	CreateSchemas    []string
	CreateMaxPerHour int
}

// ErrStandingChanged means the plugin changed between showing the rule and
// the owner allowing it.
var ErrStandingChanged = errors.New("the plugin changed; review the rule again")

// StandingApproval is the owner's active approval of a rule.
type StandingApproval struct {
	By string
	At time.Time
}

// StandingPaused says an approval ended and why ("plugin updated", "schema
// changed", "plugin uninstalled" or "turned off").
type StandingPaused struct {
	At     time.Time
	Reason string
}

// StandingWaiting is a finished automatic run whose changes wait for review.
type StandingWaiting struct {
	RunID       uuid.UUID
	Run         string
	ChangesetID int64
	Items       int
	At          time.Time
}

// StandingDigestRow is one run that published itself.
type StandingDigestRow struct {
	Plugin  string
	Changes int
	Created int // new entries the run published on its own
}

// StandingApprovals reads and records standing approvals (nil = the
// Automatic updates tab is unavailable).
type StandingApprovals interface {
	// Rule is the rule the installed plugin declares, if any.
	Rule(plugin string) (StandingRule, bool)
	// Active is the plugin's active approval for the run, nil when none.
	Active(ctx context.Context, plugin, run string) (*StandingApproval, error)
	// Paused says when and why the run's approval ended; nil while it is
	// active and when it never existed.
	Paused(ctx context.Context, plugin, run string) (*StandingPaused, error)
	// Approve records the approval of the rule the plugin declares now. The
	// caller passes no rule: the implementation reads it from the plugin. The
	// version and hash are those the form showed (StandingRule); a plugin that
	// no longer matches them is refused with ErrStandingChanged.
	Approve(ctx context.Context, plugin, run, by, version, hash string) error
	// TurnOff ends the plugin's approval at the owner's request.
	TurnOff(ctx context.Context, plugin string) error
	// Waiting lists the plugin's runs held back for review.
	Waiting(ctx context.Context, plugin string) ([]StandingWaiting, error)
	// Digest lists the runs that published themselves since the given time.
	Digest(ctx context.Context, since time.Time) ([]StandingDigestRow, error)
}
