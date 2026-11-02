package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

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
