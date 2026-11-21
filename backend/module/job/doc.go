// Package job lets a wrapper module run a recurring function inside the
// serving process, for example a nightly report.
//
//	func init() {
//		job.Register("example", "daily", 24*time.Hour, func(ctx context.Context) error {
//			return sendReport(ctx)
//		})
//	}
//
// The job id is module:<module>:<name>. The first run happens shortly after
// the first start. After that the job never runs twice within every, and
// runs at most every plus a tenth of it (capped at 5 minutes) plus a little
// jitter apart, across all servers sharing the database and across
// restarts. One run is capped at 10 minutes. A panic is recovered and an
// error is logged and shown on the Jobs screen. When the run cannot be
// claimed in the database the run is skipped.
//
// The job does not run when the worker is off (HATAMOTO_WORKER=off), in
// drain mode, when WORKER_DISABLE_JOBS lists its id, or when the module's
// migrations failed. It cannot be run from the admin. Register nothing and
// nothing changes.
package job
