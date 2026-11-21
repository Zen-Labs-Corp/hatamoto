// Package migrate lets a wrapper module ship its own database migrations.
//
//	//go:embed migrations/*.sql
//	var files embed.FS
//
//	func init() {
//		sub, _ := fs.Sub(files, "migrations")
//		migrate.Register("reports", sub)
//	}
//
// The files are goose SQL migrations. They run after the core migrations,
// both in `hatamoto serve` and in `hatamoto migrate`, on the same advisory
// lock, and are recorded in the table module_<name>_goose_db_version, never
// in core's goose_db_version. `hatamoto migrate status` lists them.
//
// A failed module migration is logged and recorded (see Err); the server
// keeps running and that module's background services are not started.
// Register nothing and nothing changes.
package migrate
