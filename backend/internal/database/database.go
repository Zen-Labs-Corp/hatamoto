// Package database provides PostgreSQL connection management with multi-tenant RLS support.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/config"
)

// DB wraps a pgx connection pool with tenant-aware operations.
type DB struct {
	pool   *pgxpool.Pool
	direct *Direct
	notes  []string
}

func socketOptions(cfg config.DatabaseConfig) SocketOptions {
	return SocketOptions{Disabled: cfg.UnixSocketOff, Dirs: cfg.SocketDirs}
}

// TransportNotes returns one boot log line per endpoint whose unix socket was
// used or refused; empty when nothing was tried.
func (db *DB) TransportNotes() []string {
	return db.notes
}

// New creates a new database connection pool.
func New(ctx context.Context, cfg config.DatabaseConfig) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	opts := socketOptions(cfg)
	mainTCP := poolConfig.ConnConfig.Copy()
	mainOut := preferUnixSocket(ctx, &poolConfig.ConnConfig.Config, opts)

	poolConfig.MaxConns = int32(cfg.MaxConnections)
	poolConfig.MinConns = int32(cfg.MinConnections)
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	db, err := NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	direct, directOut, err := newDirect(ctx, cfg.EffectiveDirectURL(), cfg.URL, db.pool, opts)
	if err != nil {
		db.Close()
		return nil, err
	}
	if direct.tcp == nil {
		direct.tcp = mainTCP // no separate direct endpoint: plugins follow the main URL
	}
	db.direct = direct
	for _, n := range []string{mainOut.note("db"), directOut.note("db direct")} {
		if n != "" {
			db.notes = append(db.notes, n)
		}
	}
	return db, nil
}

// NewWithConfig creates the pool from a ready pgx config, for callers that
// need a custom one (tests attach a pgx.QueryTracer to count statements).
func NewWithConfig(ctx context.Context, poolConfig *pgxpool.Config) (*DB, error) {
	poolConfig.AfterConnect = withJSONTypes(poolConfig.AfterConnect)
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{pool: pool, direct: &Direct{fallback: pool}}, nil
}

// Close closes the database connection pool.
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying connection pool.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Ping verifies the database connection.
func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

// GetConn returns a pooled connection. It sets no session state, so it is
// safe behind a transaction-mode pooler.
func (db *DB) GetConn(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := acquire(ctx, db.pool)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

type acquirer interface {
	Acquire(context.Context) (*pgxpool.Conn, error)
}

// AcquireFrom acquires a connection from pool, honouring the acquire bound
// carried by ctx (see WithAcquireBound).
func AcquireFrom(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	return acquire(ctx, pool)
}

func acquire(ctx context.Context, a acquirer) (*pgxpool.Conn, error) {
	if PoolBusyHit(ctx) {
		// The request already gave up on the pool and will answer 503: do not
		// queue behind it again.
		return nil, fmt.Errorf("failed to acquire connection: %w", ErrPoolBusy)
	}
	actx := ctx
	if bound := AcquireBound(ctx); bound > 0 {
		var cancel context.CancelFunc
		actx, cancel = context.WithTimeout(ctx, bound)
		defer cancel()
	}
	conn, err := a.Acquire(actx)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) && AcquireBound(ctx) > 0 {
			NotePoolBusy(ctx)
			return nil, fmt.Errorf("failed to acquire connection: %w", ErrPoolBusy)
		}
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	return conn, nil
}

// WithTx executes a function within a transaction with tenant context.
func (db *DB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.WithTxDetachedCommit(ctx, 0, fn)
}

// WithTxDetachedCommit is WithTx, but COMMIT runs on a context that ctx's
// cancellation cannot reach, so a commit cut off mid-flight cannot leave the
// outcome unknown. The commit is bounded by commitTimeout, which starts just
// before it; zero means plain WithTx behaviour (COMMIT on ctx).
func (db *DB) WithTxDetachedCommit(ctx context.Context, commitTimeout time.Duration, fn func(tx pgx.Tx) error) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("rollback failed: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	if commitTimeout <= 0 {
		return tx.Commit(ctx)
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	return tx.Commit(cctx)
}

// ExecWithTimeout executes a query with a timeout.
func (db *DB) ExecWithTimeout(ctx context.Context, timeout time.Duration, sql string, args ...any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, sql, args...)
	return err
}
