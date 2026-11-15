package wpimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionSource hands out a connection that stays on one server session until
// release is called. The import lock is a session advisory lock, so it must
// not be taken through a transaction-mode pooler: the source has to return a
// real session (database.Direct does).
type SessionSource interface {
	Session(ctx context.Context) (conn *pgx.Conn, release func(), err error)
}

// PoolSource adapts a plain pool to SessionSource, for callers without a
// separate direct endpoint (tests, a database reached without a pooler).
type PoolSource struct{ Pool *pgxpool.Pool }

// Session checks a connection out of the pool.
func (p PoolSource) Session(ctx context.Context) (*pgx.Conn, func(), error) {
	c, err := p.Pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	return c.Conn(), c.Release, nil
}

// ErrImportRunning is returned when another import (the wizard's, or the
// command line's, in this process or another) already holds the site's import lock.
var ErrImportRunning = errors.New("another import is running")

// importLockKey derives the advisory-lock key from a constant and the target
// database, so two sites on one Postgres server do not block each other.
const importLockKey = `hashtextextended('hatamoto.import.wordpress:' || current_database(), 0)`

// AcquireImportLock takes the session-level advisory lock that makes an import
// exclusive for the target database, without waiting. Hold it for the whole
// job and call release when it ends. It returns ErrImportRunning when taken.
func AcquireImportLock(ctx context.Context, src SessionSource) (release func(), err error) {
	conn, rel, err := src.Session(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+importLockKey+`)`).Scan(&ok); err != nil {
		rel()
		return nil, fmt.Errorf("import lock: %w", err)
	}
	if !ok {
		rel()
		return nil, ErrImportRunning
	}
	return func() {
		bg := context.WithoutCancel(ctx)
		var unlocked bool
		if err := conn.QueryRow(bg, `SELECT pg_advisory_unlock(`+importLockKey+`)`).Scan(&unlocked); err != nil || !unlocked {
			// Closing the session frees the lock for sure.
			_ = conn.Close(bg)
		}
		rel()
	}, nil
}

// ImportRunning reports whether an import currently holds the import lock. It
// takes the lock without waiting and gives it straight back, so it never
// blocks an import for longer than the probe itself.
func ImportRunning(ctx context.Context, src SessionSource) (bool, error) {
	release, err := AcquireImportLock(ctx, src)
	if errors.Is(err, ErrImportRunning) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	release()
	return false, nil
}
