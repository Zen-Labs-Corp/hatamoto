// Package service lets a wrapper module run a background function inside the
// serving process, for example one that reacts to published content.
//
//	func init() {
//		service.Register("reports", func(ctx context.Context) error {
//			<-ctx.Done() // do the work, return when ctx is cancelled
//			return nil
//		})
//	}
//
// Services start after the database is migrated and before the HTTP listener
// accepts connections, and stop through ctx when the server shuts down.
// Every serve process runs them: each replica, and both the old and the new
// process for a moment while an update switches over. Work that must happen
// once per install needs an advisory lock or a row claim in the database.
// Shutdown waits for them for a bounded time (10 s), then logs the ones
// still running and goes on. There is no restart loop: a service that returns
// early stays stopped. Register nothing and nothing changes.
package service
