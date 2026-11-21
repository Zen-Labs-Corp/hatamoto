package staticexport

import "context"

// Target is where an export is published.
type Target interface {
	// Key identifies the target in the run record. It never holds a secret.
	Key() string
	// Describe is the human form used in progress lines.
	Describe() string
	// Stage returns an empty directory to build into.
	Stage(ctx context.Context) (string, error)
	// Previous returns the manifest of the last export, if there is one.
	Previous(ctx context.Context) (Manifest, bool, error)
	// Publish makes the staged tree live. d is the difference between the
	// previous manifest and next.
	Publish(ctx context.Context, staged string, d Diff, next Manifest) error
	// Discard removes a staging directory, best effort.
	Discard(staged string)
}
