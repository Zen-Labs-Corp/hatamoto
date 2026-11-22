package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
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

// HistoryRow is one changeset in Settings > Backups & recovery > Changes.
type HistoryRow struct {
	ID                        int64
	Plugin, RunName, State    string
	By                        string
	Auto                      bool // published automatically under a standing approval
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

// ReviewFlow is the admin's view of changes to published content that wait
// for a reviewer. Nil hides every review screen (503) and leaves the editors
// as they are with review off.
type ReviewFlow interface {
	// Enabled reads the site's review switch from the cached settings (cosmetic: the server decides).
	Enabled(ctx context.Context) bool
	SetEnabled(ctx context.Context, on bool) error
	// Plan reports whether a save or publish by the viewer would wait for review (the button labels).
	Plan(ctx context.Context, e *entity.Entity) (held bool)
	// Pending reads the change waiting (or sent back) for one entity.
	Pending(ctx context.Context, entityID string) (PendingEdit, bool, error)
	// Queue lists the changes waiting for a decision, oldest first, with their total.
	Queue(ctx context.Context, limit, offset int) ([]PendingEdit, int, error)
	// Count is the number of changes in the queue.
	Count(ctx context.Context) (int, error)
	// Detail reads one change waiting (or sent back) with what the page decides on.
	// ErrReviewNotFound when there is none, ErrReviewNotStaged when it is closed.
	Detail(ctx context.Context, id int64) (ReviewDetail, error)
	// Approve decides the viewer's stage and publishes once every stage is approved.
	Approve(ctx context.Context, id int64, comment string) (ReviewOutcome, error)
	// SendBack records a rejection with a comment; the change stays with its requester.
	SendBack(ctx context.Context, id int64, comment string) error
	// Publish retries the publish of a change that is approved but not live.
	Publish(ctx context.Context, id int64) (ReviewOutcome, error)
	// Withdraw drops the viewer's own change; Discard drops any change (decide right).
	Withdraw(ctx context.Context, id int64) error
	Discard(ctx context.Context, id int64) error
}

// What the review actions answer when a decision cannot go ahead.
var (
	ErrReviewSelf         = errors.New("you cannot decide your own change")
	ErrReviewNotDecider   = errors.New("you cannot decide this change")
	ErrReviewStale        = errors.New("the entry changed after this was sent for review")
	ErrReviewNoComment    = errors.New("a comment is required")
	ErrReviewNotRequester = errors.New("only the person who sent this change can withdraw it")
)

// ReviewDetail is one change for the detail page.
type ReviewDetail struct {
	PendingEdit
	Live *entity.Entity // the entry as it is now; nil when it was deleted
	// Stale: the entry changed after the change was sent, so approving is refused.
	Stale bool
	// Approved: every stage approved but the change is not live (the publish failed).
	Approved bool
	// NeedsGroup names a stage group this site does not define; nobody can decide it.
	NeedsGroup string
}

// ReviewOutcome is what an approval or publish did to the live entry.
type ReviewOutcome struct {
	Published  bool
	NotApplied bool // the entry moved meanwhile: nothing was written, the change stays readable
	Pending    bool // stages remain for another reviewer
}

// PendingEdit is one change to a published entry that waits for review or was
// sent back.
type PendingEdit struct {
	ID                       int64
	EntityID, EntityName     string
	Requester, RequesterName string // the requester's user ID and display name
	Since                    time.Time
	Rejected                 bool
	Comment, Reviewer        string         // a sent-back change's comment and the reviewer's name
	Payload                  *entity.Entity // name, slug and attributes as the requester left them
}
