// Package database provides PostgreSQL connection management with multi-tenant RLS support.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/config"
)

// DB wraps a pgx connection pool with tenant-aware operations.
type DB struct {
	pool *pgxpool.Pool
}

// New creates a new database connection pool.
func New(ctx context.Context, cfg config.DatabaseConfig) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	poolConfig.MaxConns = int32(cfg.MaxConnections)
	poolConfig.MinConns = int32(cfg.MinConnections)
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	return NewWithConfig(ctx, poolConfig)
}

// NewWithConfig creates the pool from a ready pgx config, for callers that
// need a custom one (tests attach a pgx.QueryTracer to count statements).
func NewWithConfig(ctx context.Context, poolConfig *pgxpool.Config) (*DB, error) {
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{pool: pool}, nil
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

// GetConn returns a connection with tenant context configured for RLS.
// This is the primary way to acquire database connections.
func (db *DB) GetConn(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}

	if err := db.setTenantContext(ctx, conn); err != nil {
		conn.Release()
		return nil, err
	}

	return conn, nil
}

// setTenantContext configures PostgreSQL session variables. Post-Path-B
// the only remaining session variable is app.current_user_id (used for
// audit-trail context in some triggers). app.current_tenant_id and
// app.is_superadmin are no longer set because RLS is gone (migration
// 075 dropped all policies). Kept as a method so existing call sites
// in GetConn remain unchanged.
func (db *DB) setTenantContext(ctx context.Context, conn *pgxpool.Conn) error {
	userID, hasUser := GetUserID(ctx)
	if !hasUser {
		return nil
	}
	_, err := conn.Exec(ctx, "SELECT set_config($1, $2, false)", "app.current_user_id", userID.String())
	if err != nil {
		return fmt.Errorf("failed to set app.current_user_id: %w", err)
	}
	return nil
}

// WithTx executes a function within a transaction with tenant context.
func (db *DB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
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

	return tx.Commit(ctx)
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
