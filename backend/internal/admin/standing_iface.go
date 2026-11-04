package admin

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StandingRule is the automatic update rule a plugin declares for one of its
// cron runs. It only describes the rule: the owner's approval is separate.
type StandingRule struct {
	Run        string
	Fields     []string
	Schemas    []string
	MaxPerHour int
}

// StandingApproval is the owner's active approval of a rule.
type StandingApproval struct {
	By string
	At time.Time
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
}

// StandingApprovals reads and records standing approvals (nil = the
// Automatic updates tab is unavailable).
type StandingApprovals interface {
	// Rule is the rule the installed plugin declares, if any.
	Rule(plugin string) (StandingRule, bool)
	// Active is the plugin's active approval for the run, nil when none.
	Active(ctx context.Context, plugin, run string) (*StandingApproval, error)
	// Approve records the approval of the rule the plugin declares now. The
	// caller passes no rule: the implementation reads it from the plugin.
	Approve(ctx context.Context, plugin, run, by string) error
	// Waiting lists the plugin's runs held back for review.
	Waiting(ctx context.Context, plugin string) ([]StandingWaiting, error)
	// Digest lists the runs that published themselves since the given time.
	Digest(ctx context.Context, since time.Time) ([]StandingDigestRow, error)
}
