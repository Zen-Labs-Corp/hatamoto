//go:build integration

package installstats

import (
	"context"
	"os"
	"strings"
	"testing"
)

const basicToDetailedMigration = "202611180900_install_stats_basic_to_detailed.sql"

func TestMigrationBasicToDetailed_Integration(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, level, consent, id string
		wantLevel                string
		wantWindow, wantNotice   bool
	}{
		{"basic becomes detailed", "basic", "yes", "'0123456789abcdef0123456789abcdef'", "detailed", true, true},
		{"detailed stays", "detailed", "yes", "'0123456789abcdef0123456789abcdef'", "detailed", false, true},
		{"off stays", "off", "no", "NULL", "off", false, false},
		{"unset stays", "unset", "unset", "NULL", "unset", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := scratchPool(t)
			if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_install_stats (id, consent, level, install_id) VALUES (TRUE, '`+c.consent+`', '`+c.level+`', `+c.id+`)`); err != nil {
				t.Fatal(err)
			}
			applyMigrations(t, pool, basicToDetailedMigration)
			var level string
			var window, notice bool
			if err := pool.QueryRow(ctx, `SELECT level, health_window_start IS NOT NULL, default_notice_pending FROM hatamoto_install_stats`).Scan(&level, &window, &notice); err != nil {
				t.Fatal(err)
			}
			if level != c.wantLevel || window != c.wantWindow || notice != c.wantNotice {
				t.Fatalf("level = %q window set = %v notice = %v, want %q / %v / %v", level, window, notice, c.wantLevel, c.wantWindow, c.wantNotice)
			}
		})
	}
}

func TestMigrationBasicToDetailedDownIsNoop(t *testing.T) {
	b, err := os.ReadFile("../../migrations/" + basicToDetailedMigration)
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("no Down section")
	}
	for _, line := range strings.Split(down, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "--") || strings.EqualFold(l, "SELECT 1;") {
			continue
		}
		t.Fatalf("Down must be a no-op, found %q", l)
	}
}
