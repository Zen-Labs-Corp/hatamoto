package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// RunInputDecl is one input a plugin's run declares.
type RunInputDecl struct {
	Name, Label, Type string
	Required          bool
	Options           []string
}

// RunDecl is one run a plugin declares in its manifest.
type RunDecl struct {
	Name, Title string
	Inputs      []RunInputDecl
}

// RunView is one plugin run as the run pages show it.
type RunView struct {
	ID                                 uuid.UUID
	Plugin, Name, State, Phase, Reason string
	Done, Total, Steps                 int
	Message                            string
	Hosts                              []string
	ChangesetID                        int64
	StagedBytes                        int64
	StagedItems                        int
	CreatedAt, UpdatedAt               time.Time
	ChangesetState                     string    // state of its changeset; empty until the run has one
	ExpiresAt                          time.Time // when its changeset is deleted; zero until the run has one
}

// RunReader reads plugin runs (nil = the run pages are unavailable).
type RunReader interface {
	Run(ctx context.Context, id uuid.UUID) (RunView, error)
	Recent(ctx context.Context, plugin string, limit int) ([]RunView, error)
}

// RunCanceller ends a running plugin run.
type RunCanceller interface {
	CancelRun(ctx context.Context, plugin string, id uuid.UUID) error
}

// RunStepLimits are the limits one step of a plugin's run is held to.
type RunStepLimits struct {
	StepTimeout time.Duration // the guest's time for one step
	MemoryBytes uint64        // cap on the guest's memory
}

// RunLimiter reports a loaded plugin's step limits (ok is false when the
// plugin is not loaded).
type RunLimiter interface {
	RunLimitsFor(plugin string) (RunStepLimits, bool)
}

// ReviewItem is one staged item the owner decides on.
type ReviewItem struct {
	Seq                                               int
	Kind, Op, Label, Risk, Reason, Decision, Old, New string
}

// SuggestionField is one setting a suggestion would change.
type SuggestionField struct{ Name, Old, New string }

// Suggestion is a plugin's proposal that is applied only on approval.
type Suggestion struct {
	ID                    int64
	Kind, Title, Decision string
	Applicable            bool
	Fields                []SuggestionField
}

// KindRow counts the staged items of one kind.
type KindRow struct {
	Label                 string
	New, Updated, Removed int
}

// SkippedRow is something the run left out, and why.
type SkippedRow struct{ Label, Why string }

// RedirectGroup is the redirects that point at staged pages, decided as one.
type RedirectGroup struct {
	Count    int
	Decision string
	Seqs     []int
}

// ReviewData is everything the review page shows for one staged changeset.
type ReviewData struct {
	ID                         int64
	Plugin, RunName, State     string
	ExpiresAt                  time.Time
	Items, Low, High, Critical int
	HighDecided                int
	Suggestions                []Suggestion
	Risky                      []ReviewItem // one page of the itemised risky rows
	RiskyPage, RiskyPerPage    int          // the page shown, and the rows a page holds
	RiskyTotal                 int          // itemised risky rows over all pages
	RedirectGroup              *RedirectGroup
	Kinds                      []KindRow
	Skipped                    []SkippedRow
	Pending                    int
	CanPublish                 bool
	PublishCount, DroppedCount int
	PayloadBytes               int64
	Previewable                bool
}

// HistoryRow is one changeset in Settings > Changes.
type HistoryRow struct {
	ID                        int64
	Plugin, RunName, State    string
	By                        string
	Building                  bool // the run has not finished: nothing to review yet
	When                      time.Time
	Applied, Skipped, Dropped int
}

// UndoPreview is what undoing a published changeset would do.
type UndoPreview struct{ Applied, EditedSince, Settings int }

// ChangesetReviewer reviews, publishes, discards and undoes changesets
// (nil = the review pages are unavailable).
type ChangesetReviewer interface {
	Review(ctx context.Context, id int64, page int) (ReviewData, error)
	Decide(ctx context.Context, id int64, seqs []int, decision string) error
	// DecideSuggestion records the decision; "approved" applies the suggestion.
	DecideSuggestion(ctx context.Context, id, proposalID int64, decision string) error
	Publish(ctx context.Context, id int64) (applied, dropped int, err error)
	Discard(ctx context.Context, id int64) error
	History(ctx context.Context, limit, offset int) ([]HistoryRow, error)
	UndoPreview(ctx context.Context, id int64) (UndoPreview, error)
	Undo(ctx context.Context, id int64) (restored, keptEdited int, err error)
}

// What the reviewer answers when an action cannot go ahead.
var (
	ErrReviewUndecided = errors.New("some changes are not decided yet")
	ErrReviewNotStaged = errors.New("the changes are not waiting for review")
	// ErrReviewRunning: the run is still staging (or did not finish), so the
	// changes cannot be reviewed or published yet.
	ErrReviewRunning      = errors.New("the run has not finished")
	ErrReviewExpired      = errors.New("the changes expired")
	ErrReviewNotFound     = errors.New("changes not found")
	ErrReviewNotPublished = errors.New("the changes are not published")
	// ErrReviewSuggestionDecided: the suggestion already has a decision.
	ErrReviewSuggestionDecided = errors.New("that suggestion was already decided")
	// ErrReviewNotApplicable: the suggestion can only be rejected.
	ErrReviewNotApplicable = errors.New("that suggestion can only be rejected")
)
