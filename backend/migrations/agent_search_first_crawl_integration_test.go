//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

const relabelCDNEdgeVersion = 202610311110

// The sibling first-crawl table has the composite key (entity, agent).
func TestAgentSearchFirstCrawl_Table(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var cols string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		FROM pg_index i
		CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		WHERE i.indrelid = 'hatamoto_agent_search_first_crawl'::regclass AND i.indisprimary`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != "entity_id,agent_id" {
		t.Errorf("primary key = %q, want entity_id,agent_id", cols)
	}
}

func seedHit(t *testing.T, db *sql.DB, agent, ip, verification string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO hatamoto_agent_hits
		(occurred_at, agent_id, verification, ip, path, status, source)
		VALUES (now(), $1, $2, $3::inet, '/post/example-slug', 200, 'backend')`, agent, verification, ip); err != nil {
		t.Fatal(err)
	}
}

// Spoofed hits from a CDN edge address become unverifiable for every agent and
// the daily counters move by the same delta; running the migration again
// changes nothing.
func TestRelabelCDNEdgeSpoofed(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, relabelCDNEdgeVersion-1); err != nil {
		t.Fatalf("up: %v", err)
	}
	seedHit(t, db, "googlebot", "104.16.1.1", "spoofed")  // edge
	seedHit(t, db, "googlebot", "104.16.1.2", "spoofed")  // edge
	seedHit(t, db, "bingbot", "2606:4700::1", "spoofed")  // edge, other agent, IPv6
	seedHit(t, db, "googlebot", "203.0.113.9", "spoofed") // real forger
	seedHit(t, db, "googlebot", "104.16.1.3", "verified") // edge, not spoofed: untouched
	if _, err := db.Exec(`INSERT INTO hatamoto_agent_daily
		(day, agent_id, path, status, hits, verified_hits, spoofed_hits)
		VALUES (current_date, 'googlebot', '/post/example-slug', 200, 4, 1, 3),
		       (current_date, 'bingbot', '/post/example-slug', 200, 1, 0, 1)`); err != nil {
		t.Fatal(err)
	}

	counts := func() (g, b [3]int64) {
		t.Helper()
		for _, c := range []struct {
			agent string
			out   *[3]int64
		}{{"googlebot", &g}, {"bingbot", &b}} {
			if err := db.QueryRow(`SELECT verified_hits, spoofed_hits, unverifiable_hits FROM hatamoto_agent_daily
				WHERE agent_id = $1`, c.agent).Scan(&c.out[0], &c.out[1], &c.out[2]); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for run := 1; run <= 2; run++ {
		if run == 1 {
			if _, err := p.UpTo(ctx, relabelCDNEdgeVersion); err != nil {
				t.Fatalf("relabel: %v", err)
			}
		} else if _, err := p.UpByOne(ctx); err == nil {
			t.Fatal("expected no pending migration on the second run")
		}
		g, b := counts()
		if g != [3]int64{1, 1, 2} || b != [3]int64{0, 0, 1} {
			t.Fatalf("run %d: googlebot (v,s,u) = %v, bingbot = %v; want [1 1 2] and [0 0 1]", run, g, b)
		}
	}
	var spoofed, unverifiable int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE verification = 'spoofed'),
		count(*) FILTER (WHERE verification = 'unverifiable') FROM hatamoto_agent_hits`).Scan(&spoofed, &unverifiable); err != nil {
		t.Fatal(err)
	}
	if spoofed != 1 || unverifiable != 3 {
		t.Errorf("hits spoofed=%d unverifiable=%d, want 1 and 3", spoofed, unverifiable)
	}
	var ok bool
	if err := db.QueryRow(`SELECT bool_and(hits >= verified_hits + spoofed_hits + unverifiable_hits) FROM hatamoto_agent_daily`).Scan(&ok); err != nil || !ok {
		t.Errorf("hits invariant broken (%v)", err)
	}

	// Re-running the up function directly (a re-applied migration) is a no-op.
	if _, err := p.DownTo(ctx, relabelCDNEdgeVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if g, b := counts(); g != [3]int64{1, 1, 2} || b != [3]int64{0, 0, 1} {
		t.Errorf("after re-apply: googlebot %v bingbot %v", g, b)
	}
}
