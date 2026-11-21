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
// Services start after the database is migrated, as the server starts and
// before it listens, and stop through ctx when the server shuts down.
// Shutdown waits for them for a bounded time (10 s), then logs the ones
// still running and goes on. There is no restart loop: a service that returns
// early stays stopped. Register nothing and nothing changes.
package service
