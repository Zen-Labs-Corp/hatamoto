// Package db gives a wrapper module core's own database pool.
//
//	pool, err := db.Pool()
//	if err != nil {
//		return err // db.ErrNotReady: no database in this process yet; fail closed
//	}
//	err = pool.QueryRow(ctx, `SELECT ...`).Scan(&v)
//
// The pool is a *pgxpool.Pool from github.com/jackc/pgx/v5, shared with core:
// same role, same connection limit. Never Close it. serve publishes it right
// after the database opens and clears it at shutdown, after module services
// have stopped and before it is closed; a module command sees the same pool as
// cli.Env.DB until the command returns. Elsewhere Pool returns ErrNotReady.
// Name your tables module_<name>_... (see package migrate).
//
// Tests: the package has no setter. Reach the pool through a variable you can
// replace, and give tests a pool of their own:
//
//	var pool = db.Pool // tests replace it
//
// Call nothing and nothing changes.
package db
