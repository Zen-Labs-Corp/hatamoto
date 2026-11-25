//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const commentersOriginVersion = 202611251700

func TestCommentersPluginOrigin(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, commentersOriginVersion-1); err != nil {
		t.Fatal(err)
	}
	const emailID = "55555555-5555-5555-5555-555555555555"
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_commenters (id, email_hash, email_sealed) VALUES ($1, 'h', '\x00')`, emailID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, commentersOriginVersion); err != nil {
		t.Fatalf("up: %v", err)
	}

	var plugin, account *string
	if err := db.QueryRowContext(ctx, `SELECT origin_plugin, origin_account FROM hatamoto_commenters WHERE id = $1`, emailID).Scan(&plugin, &account); err != nil {
		t.Fatal(err)
	}
	if plugin != nil || account != nil {
		t.Errorf("existing email commenter origin = %v/%v, want NULL", plugin, account)
	}

	const insert = `INSERT INTO hatamoto_commenters (email_hash, email_sealed, origin_plugin, origin_account) VALUES ('plugin:' || gen_random_uuid(), ''::bytea, $1, $2)`
	for _, plug := range []string{"p1", "p2"} {
		if _, err := db.ExecContext(ctx, insert, plug, "a"); err != nil {
			t.Fatalf("insert %s: %v", plug, err)
		}
	}

	var pg *pgconn.PgError
	if _, err := db.ExecContext(ctx, insert, "p1", "a"); !errors.As(err, &pg) || pg.Code != "23505" {
		t.Errorf("duplicate origin: err = %v, want unique violation", err)
	}
	pg = nil
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_commenters (email_hash, email_sealed, origin_plugin) VALUES ('x', ''::bytea, 'p3')`); !errors.As(err, &pg) || pg.Code != "23514" {
		t.Errorf("half origin: err = %v, want check violation", err)
	}

	if _, err := p.DownTo(ctx, commentersOriginVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
