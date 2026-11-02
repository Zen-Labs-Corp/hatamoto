package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/seo"
)

func policyServer(q *seoQuerier) *Server {
	return &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), seoStore: seo.NewStore(q)}
}

func TestLog404Policy(t *testing.T) {
	cases := []struct {
		name     string
		doc      string
		disabled bool
		ignore   []string
		days     int
	}{
		{"legacy document logs, 90 days", `{"separator":"-"}`, false, nil, 90},
		{"disabled", `{"monitor404":{"disabled":true,"retention_days":30}}`, true, nil, 30},
		{"patterns", `{"monitor404":{"ignore_patterns":["*.php","/wp-admin/*"],"retention_days":365}}`, false, []string{"*.php", "/wp-admin/*"}, 365},
		{"unknown retention", `{"monitor404":{"retention_days":7}}`, false, nil, 90},
	}
	for _, c := range cases {
		s := policyServer(&seoQuerier{row: seoRow{raw: []byte(c.doc)}})
		p := s.log404Policy()
		if p.Disabled != c.disabled || len(p.Ignore) != len(c.ignore) {
			t.Errorf("%s: policy %+v", c.name, p)
		}
		if d := s.retention404Days(context.Background()); d != c.days {
			t.Errorf("%s: retention %d, want %d", c.name, d, c.days)
		}
	}
}

func TestLog404PolicyNilStoreLogsEverything(t *testing.T) {
	s := &Server{}
	if p := s.log404Policy(); p.Disabled || len(p.Ignore) != 0 {
		t.Fatalf("policy %+v", p)
	}
	if d := s.retention404Days(context.Background()); d != 90 {
		t.Fatalf("retention %d", d)
	}
}

func TestLog404PolicyReadErrorKeepsDefaults(t *testing.T) {
	s := policyServer(&seoQuerier{row: seoRow{err: errors.New("db down")}})
	if p := s.log404Policy(); p.Disabled || len(p.Ignore) != 0 {
		t.Fatalf("policy %+v", p)
	}
	if d := s.retention404Days(context.Background()); d != 90 {
		t.Fatalf("retention %d", d)
	}
}

// The policy runs per 404: a warm cache must not query the database again.
func TestLog404PolicyUsesCache(t *testing.T) {
	q := &seoQuerier{row: seoRow{raw: []byte(`{"monitor404":{"disabled":true}}`)}}
	s := policyServer(q)
	for range 50 {
		s.log404Policy()
	}
	if q.queries != 1 {
		t.Fatalf("%d queries for 50 policy reads", q.queries)
	}
}

// blockingRow waits for its context, like a database that does not answer.
type blockingRow struct{ ctx context.Context }

func (r blockingRow) Scan(...any) error { <-r.ctx.Done(); return r.ctx.Err() }

type blockingQuerier struct{ seoQuerier }

func (q *blockingQuerier) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return blockingRow{ctx}
}

// A cold cache with a hung database must not hold the request: the read is
// deadlined and the policy falls back to logging everything.
func TestLog404PolicyColdReadIsBounded(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), seoStore: seo.NewStore(&blockingQuerier{})}
	done := make(chan redirect.Log404Policy, 1)
	go func() { done <- s.log404Policy() }()
	select {
	case p := <-done:
		if p.Disabled || len(p.Ignore) != 0 {
			t.Fatalf("policy %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("log404Policy blocked on a cold, unresponsive database")
	}
}
