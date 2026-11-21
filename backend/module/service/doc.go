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
// Services start after the database is migrated and stop through ctx when
// the server shuts down. There is no restart loop: a service that returns
// early stays stopped. Register nothing and nothing changes.
package service
