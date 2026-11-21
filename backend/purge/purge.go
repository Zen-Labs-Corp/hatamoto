package purge

import "hatamoto.dev/core/internal/purgestate"

// Request names the cached copies that are stale: the absolute URLs of
// changed public pages, or All when the owner cleared the cache.
type Request = purgestate.Request

// Purger tells an edge cache that copies are stale.
type Purger = purgestate.Purger

// Register sets the purger core calls after content changed. Call it from an
// init function. It panics when called after app.Main started, a second time,
// with a nil purger, or with a name that does not match ^[a-z][a-z0-9-]{0,31}$.
// Purge runs after the change is saved, in the background, with retries; an
// error is logged and never undoes or blocks the change.
func Register(name string, p Purger) { purgestate.Register(name, p) }
