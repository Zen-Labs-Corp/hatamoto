package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Direct hands out connections that bypass a transaction-mode pooler. Work
// that needs real server sessions (session advisory locks, migrations,
// plugin roles) takes them from here instead of the main pool.
//
// When no separate direct URL is configured it falls back to the main pool,
// so installs without a pooler behave exactly as before.
type Direct struct {
	url      string          // empty: use the fallback pool
	cfg      *pgx.ConnConfig // parsed url, pool_* keys stripped; nil with the fallback
	fallback *pgxpool.Pool
}

// newDirect builds a Direct for url. An empty url, or one equal to mainURL,
// means "no separate direct endpoint".
func newDirect(url, mainURL string, fallback *pgxpool.Pool) (*Direct, error) {
	d := &Direct{fallback: fallback}
	if url == "" || url == mainURL {
		return d, nil
	}
	// pgxpool.ParseConfig consumes the pool_* keys, so they never reach the server.
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse direct database URL: %w", err)
	}
	mc, err := pgxpool.ParseConfig(mainURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}
	// Migrations, backups and plugin grants run over this URL and assume the
	// same role and database as the pooled one. Hosts and ports may differ.
	// The message carries no URL, so no password.
	if pc.ConnConfig.User != mc.ConnConfig.User || pc.ConnConfig.Database != mc.ConnConfig.Database {
		return nil, fmt.Errorf("DATABASE_DIRECT_URL must use the same user and database name as DATABASE_URL")
	}
	d.url = url
	d.cfg = pc.ConnConfig
	return d, nil
}

// Session returns a dedicated connection that stays on one server session
// until release is called. With a direct URL it is a new connection that
// release closes; otherwise it is a connection checked out of the main pool.
func (d *Direct) Session(ctx context.Context) (*pgx.Conn, func(), error) {
	if d.cfg == nil {
		pc, err := acquire(ctx, d.fallback)
		if err != nil {
			return nil, nil, err
		}
		return pc.Conn(), pc.Release, nil
	}
	// A blackholed direct host must not hang a request until the OS gives up.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, d.cfg.Copy())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to direct database: %w", err)
	}
	return conn, func() {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}, nil
}

// Pool returns a pool of at most maxConns direct connections and a function
// that releases it. Without a direct URL it is the main pool and the close
// function does nothing.
func (d *Direct) Pool(ctx context.Context, maxConns int32) (*pgxpool.Pool, func(), error) {
	if d.cfg == nil {
		return d.fallback, func() {}, nil
	}
	pc, err := pgxpool.ParseConfig(d.url)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse direct database URL: %w", err)
	}
	pc.MaxConns = maxConns
	pc.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create direct connection pool: %w", err)
	}
	return pool, pool.Close, nil
}

// ConnConfig returns a copy of the connection settings for the direct
// endpoint (the main pool's settings when none is configured).
func (d *Direct) ConnConfig() *pgx.ConnConfig {
	if d.cfg == nil {
		return d.fallback.Config().ConnConfig.Copy()
	}
	return d.cfg.Copy()
}

// Direct returns the direct-connection source for this database.
func (db *DB) Direct() *Direct {
	return db.direct
}
