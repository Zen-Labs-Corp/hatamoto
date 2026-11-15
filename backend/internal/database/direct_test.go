package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewDirectFallsBackWhenUnsetOrEqual(t *testing.T) {
	const main = "postgres://u:p@pooler:6432/db"
	for _, url := range []string{"", main} {
		d, err := newDirect(url, main, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.cfg != nil {
			t.Fatalf("url %q: expected the fallback, got a direct config", url)
		}
	}
}

func TestNewDirectStripsPoolKeys(t *testing.T) {
	d, err := newDirect("postgres://u:p@pg:5432/db?pool_max_conns=9&application_name=x", "postgres://u:p@pooler:6432/db", nil)
	if err != nil {
		t.Fatal(err)
	}
	cc := d.ConnConfig()
	if cc.Host != "pg" || cc.Port != 5432 || cc.Database != "db" {
		t.Fatalf("unexpected endpoint %s:%d/%s", cc.Host, cc.Port, cc.Database)
	}
	for k := range cc.RuntimeParams {
		if len(k) >= 5 && k[:5] == "pool_" {
			t.Fatalf("pool key %q would reach the server", k)
		}
	}
	if cc.RuntimeParams["application_name"] != "x" {
		t.Fatalf("application_name lost: %v", cc.RuntimeParams)
	}
	// The returned config is a copy.
	cc.Host = "changed"
	if d.ConnConfig().Host != "pg" {
		t.Fatal("ConnConfig leaked internal state")
	}
}

func TestNewDirectRejectsBadURL(t *testing.T) {
	if _, err := newDirect("postgres://u:p@h:notaport/db", "x", nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDirectPoolFallbackIsMainPool(t *testing.T) {
	// A lazily-connecting pool is enough: nothing is dialled.
	pc, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d, _ := newDirect("", "", pool)
	got, closeFn, err := d.Pool(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	closeFn()
	if got != pool {
		t.Fatal("fallback Pool must be the main pool")
	}
	if d.ConnConfig().Host != "127.0.0.1" {
		t.Fatal("fallback ConnConfig should mirror the main pool")
	}
}

func TestDirectPoolSeparateWhenSet(t *testing.T) {
	d, err := newDirect("postgres://u:p@127.0.0.1:1/db?pool_max_conns=9", "postgres://u:p@127.0.0.1:2/db", nil)
	if err != nil {
		t.Fatal(err)
	}
	pool, closeFn, err := d.Pool(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if pool.Config().MaxConns != 2 {
		t.Fatalf("MaxConns = %d, want 2", pool.Config().MaxConns)
	}
}
