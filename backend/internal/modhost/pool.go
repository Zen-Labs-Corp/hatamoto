package modhost

import (
	"errors"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotReady is returned when this process has not opened core's database
// pool yet, or has already closed it.
var ErrNotReady = errors.New("module: database pool is not available in this process")

var pool atomic.Pointer[pgxpool.Pool]

func init() { onReset(func() { pool.Store(nil) }) }

// SetPool publishes core's database pool to modules; core only. A nil pool
// clears it, so later Pool calls return ErrNotReady (core clears it before
// closing the pool).
func SetPool(p *pgxpool.Pool) { pool.Store(p) }

// Pool returns the pool core published, or ErrNotReady.
func Pool() (*pgxpool.Pool, error) {
	if p := pool.Load(); p != nil {
		return p, nil
	}
	return nil, ErrNotReady
}
