package localeconv

import "context"

// Service is what the CLI and the admin use. Blocking failures come back as
// *BlockingError.
type Service interface {
	// Suggest reads the data and proposes a plan; the issues are warnings. It writes nothing.
	Suggest(ctx context.Context) (*Plan, []Issue, error)
	// DryRun analyses plan against the data. Errors in the report are not a Go error.
	DryRun(ctx context.Context, plan *Plan) (*Report, error)
	// Apply converts under the write lock. It returns *BlockingError when the
	// re-analysis finds blocking errors or the fingerprint is stale.
	Apply(ctx context.Context, plan *Plan, opts ApplyOpts) (*Record, *Report, error)
	// Revert undoes conversion id. It returns *BlockingError listing every
	// refusal unless force is set (and even then for already_reverted and newer_conversion).
	Revert(ctx context.Context, id int64, force bool, by string) (*RevertResult, error)
	// History lists conversions, newest first.
	History(ctx context.Context) ([]HistoryItem, error)
}
