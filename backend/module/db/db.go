package db

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
)

// ErrNotReady is returned before core has opened its database pool in this
// process, and after it has started to close it.
var ErrNotReady = modhost.ErrNotReady

// Pool returns core's database pool. In serve it is available from just
// after the database opens (before migrations) until shutdown; in a module
// command it is the same pool as cli.Env.DB. Call it where you use the pool
// rather than keeping the result, and never Close it.
func Pool() (*pgxpool.Pool, error) { return modhost.Pool() }
