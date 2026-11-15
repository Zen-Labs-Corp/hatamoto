package app

import "testing"

func TestDirectDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://pooled/db")
	t.Setenv("DATABASE_DIRECT_URL", "")
	if got := directDatabaseURL(); got != "postgres://pooled/db" {
		t.Fatalf("unset direct URL: got %q, want DATABASE_URL", got)
	}
	t.Setenv("DATABASE_DIRECT_URL", "postgres://direct/db")
	if got := directDatabaseURL(); got != "postgres://direct/db" {
		t.Fatalf("set direct URL: got %q", got)
	}
}
