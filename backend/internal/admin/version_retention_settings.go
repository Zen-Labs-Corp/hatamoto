package admin

import "context"

// VersionRetentionService is the version history limit: how many earlier
// versions of each item the daily prune keeps. The server implements it on the
// site config.
type VersionRetentionService interface {
	Keep(ctx context.Context) (int, error)
	SetKeep(ctx context.Context, n int) error
}
